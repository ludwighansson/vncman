//go:build unix

package platform

import (
	"strings"
	"testing"
)

func TestLaunch(t *testing.T) {
	if err := launch("true"); err != nil {
		t.Errorf("true: %v", err)
	}
	err := launch("sh", "-c", "echo 'no method available for opening' >&2; exit 3")
	if err == nil || !strings.Contains(err.Error(), "no method available") {
		t.Errorf("failing opener: got %v", err)
	}
	if err := launch("vncman-no-such-opener"); err == nil {
		t.Error("missing opener: expected an error")
	}
}
