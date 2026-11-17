// Package web serves noVNC consoles on a loopback port (ADR 0004, 0006).
package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/ludwighansson/vncman/internal/relay"
)

func init() {
	// noVNC's bell sound; Go's MIME table doesn't know this extension.
	_ = mime.AddExtensionType(".oga", "audio/ogg")
}

// novncDefaults replaces noVNC's defaults.json. Users can still change these
// settings in noVNC's settings panel.
const novncDefaults = `{"autoconnect": true, "resize": "scale", "reconnect": true, "reconnect_delay": 2000}` + "\n"

// Console is a console the browser may open.
type Console struct {
	Title string       // browser tab title
	Dial  relay.Dialer // reaches the VM's VNC server
}

type entry struct {
	Console
	ctx    context.Context
	cancel context.CancelFunc
	active int
}

// Server serves noVNC consoles. Only consoles added with Add can be opened,
// each through its own random token.
type Server struct {
	ln     net.Listener
	host   string // the only accepted Host header
	origin string // the only accepted WebSocket Origin
	assets fs.FS
	page   []byte
	srv    *http.Server

	mu       sync.Mutex
	consoles map[string]*entry
	onChange func(token string, active int)
	onError  func(token string, err error)
}

// New listens on 127.0.0.1:port (0 picks a free port) and prepares to serve
// noVNC from assets. Call Serve to start serving.
func New(port int, assets fs.FS) (*Server, error) {
	page, err := fs.ReadFile(assets, "vnc.html")
	if err != nil {
		return nil, fmt.Errorf("reading noVNC: %w", err)
	}
	if !bytes.Contains(page, []byte("<head>")) || !bytes.Contains(page, []byte("</head>")) {
		return nil, errors.New("noVNC's vnc.html has no <head> element")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("starting the web server: %w", err)
	}
	host := ln.Addr().String()
	s := &Server{
		ln:       ln,
		host:     host,
		origin:   "http://" + host,
		assets:   assets,
		page:     page,
		consoles: make(map[string]*entry),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /console/{token}", s.handleConsole)
	mux.HandleFunc("GET /ws/{token}", s.handleWebSocket)
	mux.HandleFunc("GET /novnc/defaults.json", handleDefaults)
	mux.Handle("GET /novnc/", http.StripPrefix("/novnc/", http.FileServerFS(assets)))
	s.srv = &http.Server{Handler: s.guard(mux), ReadHeaderTimeout: 10 * time.Second}
	return s, nil
}

// Serve serves requests until Close is called.
func (s *Server) Serve() error {
	if err := s.srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Close ends all consoles and stops the server.
func (s *Server) Close() {
	s.mu.Lock()
	for token, e := range s.consoles {
		e.cancel()
		delete(s.consoles, token)
	}
	s.mu.Unlock()
	_ = s.srv.Close()
}

// SetCallbacks sets functions that are called when a console's number of
// connections changes, and when its relay fails.
func (s *Server) SetCallbacks(onChange func(token string, active int), onError func(token string, err error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange, s.onError = onChange, onError
}

// Add registers a console and returns its token.
func (s *Server) Add(c Console) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.consoles[token] = &entry{Console: c, ctx: ctx, cancel: cancel}
	s.mu.Unlock()
	return token, nil
}

// Remove revokes a console's token and ends its connections.
func (s *Server) Remove(token string) {
	s.mu.Lock()
	e := s.consoles[token]
	delete(s.consoles, token)
	s.mu.Unlock()
	if e != nil {
		e.cancel()
	}
}

// URL returns the address of a console's page.
func (s *Server) URL(token string) string {
	return s.origin + "/console/" + token + "?path=" + url.QueryEscape("/ws/"+token)
}

func (s *Server) lookup(token string) *entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consoles[token]
}

// guard sets protective headers on every response and rejects requests for
// any other host name, which defeats DNS rebinding.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		if r.Host != s.host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleDefaults(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(novncDefaults))
}

// handleConsole serves noVNC's page with a <base> element, so its relative
// URLs load from /novnc/, and a script that keeps the tab title on the VM.
func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	e := s.lookup(r.PathValue("token"))
	if e == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(s.render(e.Title))
}

func (s *Server) render(title string) []byte {
	// json.Marshal escapes <, > and &, so the title can't end the script.
	quoted, _ := json.Marshal(title)
	script := fmt.Sprintf(`<script>
    (() => {
        const title = %s;
        const keep = () => { if (document.title !== title) document.title = title; };
        keep();
        new MutationObserver(keep).observe(document.querySelector("title"),
            { childList: true, characterData: true, subtree: true });
    })();
    </script>
</head>`, quoted)
	page := bytes.Replace(s.page, []byte("<head>"), []byte("<head>\n    <base href=\"/novnc/\">"), 1)
	return bytes.Replace(page, []byte("</head>"), []byte(script), 1)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	e := s.lookup(token)
	if e == nil {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Origin") != s.origin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
	if err != nil {
		return
	}
	// Large clipboard pastes arrive as single messages.
	c.SetReadLimit(1 << 20)
	conn := websocket.NetConn(e.ctx, c, websocket.MessageBinary)

	s.adjust(token, e, +1)
	defer s.adjust(token, e, -1)
	if err := relay.Pipe(e.ctx, conn, e.Dial); err != nil {
		s.mu.Lock()
		onError := s.onError
		s.mu.Unlock()
		if onError != nil {
			onError(token, err)
		}
	}
}

func (s *Server) adjust(token string, e *entry, delta int) {
	s.mu.Lock()
	e.active += delta
	active, onChange := e.active, s.onChange
	s.mu.Unlock()
	if onChange != nil {
		onChange(token, active)
	}
}
