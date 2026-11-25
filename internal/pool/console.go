package pool

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"time"
)

// console writes plain "YYYY-MM-DD HH:MM:SS [who] message" lines: status to
// out, warnings and errors to err.
type console struct {
	mu       sync.Mutex
	out, err io.Writer
	now      func() time.Time
}

const timeLayout = "2006-01-02 15:04:05"

func (c *console) line(w io.Writer, who, format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fmt.Fprintf(w, "%s [%s] %s\n", c.now().Format(timeLayout), who, fmt.Sprintf(format, args...))
}

func (c *console) info(who, format string, args ...any) { c.line(c.out, who, format, args...) }
func (c *console) warn(who, format string, args ...any) { c.line(c.err, who, format, args...) }

// tail keeps the last n complete lines written to it.
type tail struct {
	n     int
	mu    sync.Mutex
	lines []string
	part  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.part = append(t.part, p...)
	for {
		i := bytes.IndexByte(t.part, '\n')
		if i < 0 {
			break
		}
		t.lines = append(t.lines, string(bytes.TrimRight(t.part[:i], "\r")))
		t.part = t.part[i+1:]
		if len(t.lines) > t.n {
			t.lines = t.lines[len(t.lines)-t.n:]
		}
	}
	return len(p), nil
}

// Lines returns the kept lines, including an unterminated final one.
func (t *tail) Lines() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]string(nil), t.lines...)
	if len(t.part) > 0 {
		out = append(out, string(t.part))
		if len(out) > t.n {
			out = out[len(out)-t.n:]
		}
	}
	return out
}
