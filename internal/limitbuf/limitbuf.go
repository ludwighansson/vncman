// Package limitbuf provides a writer that keeps only the start of what is
// written to it, for capturing a child process's error output.
package limitbuf

import "sync"

// Buffer keeps the first Max bytes written to it and discards the rest.
type Buffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

// New returns a buffer that keeps up to max bytes.
func New(max int) *Buffer { return &Buffer{max: max} }

// Write implements io.Writer. It never fails.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - len(b.buf); room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// String returns what was kept.
func (b *Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
