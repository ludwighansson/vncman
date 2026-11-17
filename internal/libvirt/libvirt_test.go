package libvirt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ludwighansson/vncman/internal/ssh"
)

// A plain libvirt VM, as on node1.
const plainXML = `<domain type='kvm' id='4'>
  <name>web01</name>
  <uuid>610964c9-5469-4c62-8737-233c94242fae</uuid>
  <devices>
    <graphics type='vnc' port='5903' autoport='yes' listen='127.0.0.1'>
      <listen type='address' address='127.0.0.1'/>
    </graphics>
  </devices>
</domain>
`

// A Nova instance listening on all addresses.
const novaXML = `<domain type='kvm' id='7'>
  <name>instance-0000a1b2</name>
  <uuid>45BD23E4-39FD-4C00-A5F7-EDF0D3ED2AE1</uuid>
  <metadata>
    <nova:instance xmlns:nova="http://openstack.org/xmlns/libvirt/nova/1.1">
      <nova:package version="27.1.0"/>
      <nova:name>web-prod-1</nova:name>
      <nova:flavor name="m1.large"><nova:memory>8192</nova:memory></nova:flavor>
      <nova:owner>
        <nova:user uuid="u1">alice</nova:user>
        <nova:project uuid="p1">acme-prod</nova:project>
      </nova:owner>
    </nova:instance>
  </metadata>
  <devices>
    <graphics type='vnc' port='5901' autoport='yes' listen='0.0.0.0'>
      <listen type='address' address='0.0.0.0'/>
    </graphics>
  </devices>
</domain>
`

const spiceXML = `<domain type='kvm'>
  <name>desktop</name>
  <uuid>8b7dfe49-7529-4047-9879-882c5c6a0999</uuid>
  <devices><graphics type='spice' port='5930' autoport='yes'/></devices>
</domain>
`

const socketXML = `<domain type='kvm'>
  <name>socket-vm</name>
  <uuid>ac007125-f8f3-470b-b000-3637f8f83b58</uuid>
  <metadata>
    <other:instance xmlns:other="http://example.com/not-nova"><other:name>ignored</other:name></other:instance>
  </metadata>
  <devices>
    <graphics type='vnc'><listen type='socket' socket='/run/libvirt/qemu/vnc.sock'/></graphics>
  </devices>
</domain>
`

const mgmtXML = `<domain type='kvm'>
  <name>mgmt-vm</name>
  <uuid>34a2193e-1934-40a4-b0d2-77415c80658b</uuid>
  <devices><graphics type='vnc' port='5905' autoport='yes' listen='10.0.0.5'/></devices>
</domain>
`

func TestParseDomains(t *testing.T) {
	domains, err := ParseDomains(strings.NewReader(plainXML + novaXML + spiceXML + socketXML + mgmtXML))
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 5 {
		t.Fatalf("got %d domains, want 5", len(domains))
	}
	byName := make(map[string]Domain)
	for _, d := range domains {
		byName[d.Name] = d
	}

	plain := byName["web01"]
	if plain.Nova != nil || plain.DisplayName() != "web01" {
		t.Errorf("plain VM: got Nova %+v, display name %q", plain.Nova, plain.DisplayName())
	}
	if plain.VNC == nil || *plain.VNC != (VNC{Host: "127.0.0.1", Port: 5903}) {
		t.Errorf("plain VM: got VNC %+v", plain.VNC)
	}

	nova := byName["instance-0000a1b2"]
	if nova.UUID != "45bd23e4-39fd-4c00-a5f7-edf0d3ed2ae1" {
		t.Errorf("UUID not normalised: %q", nova.UUID)
	}
	if nova.DisplayName() != "web-prod-1" || nova.Project() != "acme-prod" || nova.Flavor() != "m1.large" {
		t.Errorf("nova VM: got %q / %q / %q", nova.DisplayName(), nova.Project(), nova.Flavor())
	}
	if nova.VNC == nil || *nova.VNC != (VNC{Host: "127.0.0.1", Port: 5901}) {
		t.Errorf("wildcard listen should connect to 127.0.0.1, got %+v", nova.VNC)
	}

	if d := byName["desktop"]; d.VNC != nil || d.Graphics != "spice" {
		t.Errorf("spice VM: got VNC %+v, graphics %q", d.VNC, d.Graphics)
	}
	if d := byName["socket-vm"]; d.VNC != nil || d.Graphics != "vnc on a UNIX socket" || d.Nova != nil {
		t.Errorf("socket VM: got VNC %+v, graphics %q, nova %+v", d.VNC, d.Graphics, d.Nova)
	}
	if d := byName["mgmt-vm"]; d.VNC == nil || *d.VNC != (VNC{Host: "10.0.0.5", Port: 5905}) {
		t.Errorf("management address VM: got %+v", d.VNC)
	}
}

// fakeNode answers virsh commands the way a node with the given access
// rules would.
type fakeNode struct {
	access   string // "open", "nopasswd" or "passwd"
	password string
	domains  map[string]string // UUID → XML
	order    []string
	calls    []string
	stdins   []string
}

func newFakeNode(access string) *fakeNode {
	return &fakeNode{
		access:   access,
		password: "secret",
		domains: map[string]string{
			"610964c9-5469-4c62-8737-233c94242fae": plainXML,
			"45bd23e4-39fd-4c00-a5f7-edf0d3ed2ae1": novaXML,
		},
		order: []string{"610964c9-5469-4c62-8737-233c94242fae", "45bd23e4-39fd-4c00-a5f7-edf0d3ed2ae1"},
	}
}

func remoteErr(stderr string) ([]byte, []byte, error) {
	return nil, []byte(stderr), &ssh.RemoteError{Code: 1, Stderr: stderr}
}

func (f *fakeNode) Run(_ context.Context, remote string, stdin []byte) ([]byte, []byte, error) {
	f.calls = append(f.calls, remote)
	f.stdins = append(f.stdins, string(stdin))

	var args string
	switch {
	case strings.HasPrefix(remote, "sudo -n "):
		if f.access == "passwd" {
			return remoteErr("sudo: a password is required\n")
		}
		args = strings.TrimPrefix(remote, "sudo -n ")
	case strings.HasPrefix(remote, "sudo -S -p '' "):
		if f.access == "passwd" && string(stdin) != f.password+"\n" {
			return remoteErr("Sorry, try again.\nsudo: no password was provided\nsudo: 1 incorrect password attempt\n")
		}
		args = strings.TrimPrefix(remote, "sudo -S -p '' ")
	default:
		if f.access != "open" {
			return remoteErr("error: failed to connect to the hypervisor\nerror: Failed to connect socket to '/var/run/libvirt/libvirt-sock-ro': Permission denied\n")
		}
		args = remote
	}

	args = strings.TrimPrefix(args, DefaultVirshForTest+" ")
	if args == "list --uuid --state-running" {
		return []byte(strings.Join(f.order, "\n") + "\n\n"), nil, nil
	}
	var out strings.Builder
	var missing []string
	for _, cmd := range strings.Split(strings.Trim(args, "'"), "; ") {
		uuid := strings.TrimPrefix(cmd, "dumpxml ")
		if xml, ok := f.domains[uuid]; ok {
			out.WriteString(xml)
		} else {
			missing = append(missing, "error: failed to get domain '"+uuid+"'")
		}
	}
	if len(missing) > 0 {
		msg := strings.Join(missing, "\n")
		return []byte(out.String()), []byte(msg), &ssh.RemoteError{Code: 1, Stderr: msg}
	}
	return []byte(out.String()), nil, nil
}

const DefaultVirshForTest = "virsh -r -c qemu:///system"

func TestListWithoutSudo(t *testing.T) {
	node := newFakeNode("open")
	l := NewLister(node, DefaultVirshForTest, SudoAuto)
	domains, err := l.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 2 || domains[0].DisplayName() != "web-prod-1" || domains[1].DisplayName() != "web01" {
		t.Fatalf("got %+v", domains)
	}
	for _, c := range node.calls {
		if strings.Contains(c, "sudo") {
			t.Errorf("used sudo on an open node: %q", c)
		}
	}
	if !strings.Contains(node.calls[1], "'dumpxml 610964c9-5469-4c62-8737-233c94242fae; dumpxml 45bd23e4-39fd-4c00-a5f7-edf0d3ed2ae1'") {
		t.Errorf("dumpxml should be one quoted virsh invocation, got %q", node.calls[1])
	}
}

func TestListEscalatesToSudo(t *testing.T) {
	node := newFakeNode("nopasswd")
	l := NewLister(node, DefaultVirshForTest, SudoAuto)
	if _, err := l.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"virsh", "sudo -n", "sudo -n"}
	if len(node.calls) != len(want) {
		t.Fatalf("got calls %q", node.calls)
	}
	for i, prefix := range want {
		if !strings.HasPrefix(node.calls[i], prefix) {
			t.Errorf("call %d: got %q, want prefix %q", i, node.calls[i], prefix)
		}
	}
}

func TestListAsksForPassword(t *testing.T) {
	node := newFakeNode("passwd")
	l := NewLister(node, DefaultVirshForTest, SudoAuto)
	ctx := context.Background()

	if _, err := l.List(ctx); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("got %v, want ErrPasswordRequired", err)
	}
	l.SetPassword("wrong")
	if _, err := l.List(ctx); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("got %v, want ErrBadPassword", err)
	}
	// The rejected password is forgotten.
	if _, err := l.List(ctx); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("got %v, want ErrPasswordRequired after a rejected password", err)
	}
	l.SetPassword("secret")
	domains, err := l.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 2 {
		t.Fatalf("got %d domains", len(domains))
	}
	for i, c := range node.calls {
		if strings.Contains(c, "secret") || strings.Contains(c, "wrong") {
			t.Errorf("password appears in a command line: %q", c)
		}
		if strings.HasPrefix(c, "sudo -S") && node.stdins[i] == "" {
			t.Errorf("sudo -S got no password on stdin")
		}
	}
	// Once resolved, later commands go straight to sudo -S.
	node.calls = nil
	if _, err := l.Lookup(ctx, "610964c9-5469-4c62-8737-233c94242fae"); err != nil {
		t.Fatal(err)
	}
	if len(node.calls) != 1 || !strings.HasPrefix(node.calls[0], "sudo -S") {
		t.Errorf("got calls %q", node.calls)
	}
}

func TestSudoModes(t *testing.T) {
	ctx := context.Background()

	never := NewLister(newFakeNode("nopasswd"), DefaultVirshForTest, SudoNever)
	_, err := never.List(ctx)
	var lerr *Error
	if !errors.As(err, &lerr) || !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("sudo=never: got %v", err)
	}

	node := newFakeNode("open")
	always := NewLister(node, DefaultVirshForTest, SudoAlways)
	if _, err := always.List(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(node.calls[0], "sudo -n ") {
		t.Errorf("sudo=always should start with sudo, got %q", node.calls[0])
	}
}

type scriptedRunner func(remote string) ([]byte, []byte, error)

func (s scriptedRunner) Run(_ context.Context, remote string, _ []byte) ([]byte, []byte, error) {
	return s(remote)
}

func TestSSHFailureDoesNotEscalate(t *testing.T) {
	calls := 0
	runner := scriptedRunner(func(string) ([]byte, []byte, error) {
		calls++
		return nil, nil, ssh.ErrNotConnected
	})
	_, err := NewLister(runner, DefaultVirshForTest, SudoAuto).List(context.Background())
	if !errors.Is(err, ssh.ErrNotConnected) || calls != 1 {
		t.Errorf("got %v after %d calls", err, calls)
	}
}

func TestSudoRefusedReportsBothSteps(t *testing.T) {
	runner := scriptedRunner(func(remote string) ([]byte, []byte, error) {
		if strings.HasPrefix(remote, "sudo") {
			return remoteErr("Sorry, user ops is not allowed to execute '/usr/bin/virsh' as root on node1.")
		}
		return remoteErr("error: Permission denied")
	})
	_, err := NewLister(runner, DefaultVirshForTest, SudoAuto).List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "without sudo: error: Permission denied") ||
		!strings.Contains(err.Error(), "with sudo: Sorry, user ops is not allowed") {
		t.Errorf("got %v", err)
	}
}

func TestDomainStoppedDuringRefresh(t *testing.T) {
	node := newFakeNode("open")
	node.order = append(node.order, "a5b13a45-9589-4543-844d-b003296c780d") // listed, then gone
	domains, err := NewLister(node, DefaultVirshForTest, SudoAuto).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 2 {
		t.Errorf("got %d domains, want the 2 still running", len(domains))
	}
}

func TestParseUUIDsRejectsGarbage(t *testing.T) {
	if _, err := parseUUIDs([]byte("610964c9-5469-4c62-8737-233c94242fae\nWelcome to node1!\n")); err == nil {
		t.Error("expected an error for a non-UUID line")
	}
	if _, err := NewLister(newFakeNode("open"), DefaultVirshForTest, SudoAuto).Lookup(context.Background(), "x; reboot"); err == nil {
		t.Error("expected an error for an invalid UUID")
	}
}
