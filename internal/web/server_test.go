package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/ludwighansson/vncman/internal/console"
)

var testAssets = fstest.MapFS{
	"vnc.html":  {Data: []byte("<html>\n<head>\n<title>noVNC</title>\n<link href=\"app/styles/base.css\">\n</head>\n<body></body>\n</html>\n")},
	"app/ui.js": {Data: []byte("export default {};\n")},
}

func catDialer(ctx context.Context) (*exec.Cmd, error) {
	return exec.CommandContext(ctx, "cat"), nil
}

func start(t *testing.T) *Server {
	t.Helper()
	s, err := New(0, testAssets)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve() }()
	t.Cleanup(s.Close)
	return s
}

func get(t *testing.T, url, host string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestConsolePage(t *testing.T) {
	s := start(t)
	const title = "web</script><b>01 · node1"
	token, err := s.Add(Console{Title: title, Dial: catDialer})
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 32 {
		t.Errorf("token %q should be 128 bits of hex", token)
	}

	resp, body := get(t, s.URL(token), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
	if !strings.Contains(body, `<head>`+"\n"+`    <base href="/novnc/">`) {
		t.Errorf("missing <base> right after <head>:\n%s", body)
	}
	if strings.Contains(body, "</script><b>") {
		t.Errorf("title was not escaped:\n%s", body)
	}
	quoted, _ := json.Marshal(title) // escapes <, > and &
	if !strings.Contains(body, string(quoted)) || strings.Contains(string(quoted), "<") {
		t.Errorf("missing title script:\n%s", body)
	}
	for header, want := range map[string]string{
		"Cache-Control":           "no-store",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "frame-ancestors 'none'",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s: got %q, want %q", header, got, want)
		}
	}
	if !strings.Contains(s.URL(token), "?path=%2Fws%2F"+token) {
		t.Errorf("URL should carry the WebSocket path: %s", s.URL(token))
	}
}

func TestAccessControl(t *testing.T) {
	s := start(t)
	token, _ := s.Add(Console{Title: "vm", Dial: catDialer})

	if resp, _ := get(t, s.URL(token), "attacker.example:80"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host header: got %d, want 403", resp.StatusCode)
	}
	if resp, _ := get(t, s.origin+"/console/0123456789abcdef0123456789abcdef", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown token: got %d, want 404", resp.StatusCode)
	}
	if resp, _ := get(t, s.origin+"/", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("index: got %d, want 404", resp.StatusCode)
	}

	s.Remove(token)
	if resp, _ := get(t, s.URL(token), ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("removed token: got %d, want 404", resp.StatusCode)
	}
}

func TestStaticFiles(t *testing.T) {
	s := start(t)
	resp, body := get(t, s.origin+"/novnc/app/ui.js", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "javascript") {
		t.Errorf("ui.js: got %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if body != "export default {};\n" {
		t.Errorf("ui.js body: %q", body)
	}
	if _, body := get(t, s.origin+"/novnc/defaults.json", ""); !strings.Contains(body, `"autoconnect": true`) {
		t.Errorf("defaults.json: %q", body)
	}
}

func TestWebSocketRelay(t *testing.T) {
	s := start(t)
	var mu sync.Mutex
	var counts []int
	s.SetCallbacks(func(_ string, n int) {
		mu.Lock()
		counts = append(counts, n)
		mu.Unlock()
	}, func(_ string, err error) { t.Errorf("relay error: %v", err) })
	token, _ := s.Add(Console{Title: "vm", Dial: catDialer})
	wsURL := "ws://" + s.host + "/ws/" + token
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Another website's page must not be able to connect.
	_, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {"https://attacker.example"}},
	})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Origin: got err %v, response %v", err, resp)
	}

	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {s.origin}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageBinary, []byte("RFB 003.008\n")); err != nil {
		t.Fatal(err)
	}
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "RFB 003.008\n" {
		t.Errorf("got %q", data)
	}
	c.Close(websocket.StatusNormalClosure, "")

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(counts)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(counts) != 2 || counts[0] != 1 || counts[1] != 0 {
		t.Errorf("got connection counts %v, want [1 0]", counts)
	}
}

// The embedded noVNC release must keep working with the page rewriting.
func TestEmbeddedNoVNC(t *testing.T) {
	s, err := New(0, console.NoVNC())
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve() }()
	defer s.Close()
	token, _ := s.Add(Console{Title: "web01 · node1", Dial: catDialer})

	_, body := get(t, s.URL(token), "")
	for _, want := range []string{`<base href="/novnc/">`, `import UI from "./app/ui.js"`, `"web01 · node1"`} {
		if !strings.Contains(body, want) {
			t.Errorf("console page lacks %q", want)
		}
	}
	for _, path := range []string{"app/ui.js", "core/rfb.js", "app/styles/base.css", "vendor/pako/lib/zlib/inflate.js", "package.json"} {
		if resp, _ := get(t, s.origin+"/novnc/"+path, ""); resp.StatusCode != http.StatusOK {
			t.Errorf("%s: got %d", path, resp.StatusCode)
		}
	}
}
