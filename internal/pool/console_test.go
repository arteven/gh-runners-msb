package pool

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

func TestConsoleLines(t *testing.T) {
	var out, errb bytes.Buffer
	c := &console{out: &out, err: &errb, now: func() time.Time {
		return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	}}
	c.info("slot 2", "runner finished; replacing VM")
	c.warn("pool", "oops %d", 7)
	if got, want := out.String(), "2026-01-02 03:04:05 [slot 2] runner finished; replacing VM\n"; got != want {
		t.Errorf("out = %q, want %q", got, want)
	}
	if got, want := errb.String(), "2026-01-02 03:04:05 [pool] oops 7\n"; got != want {
		t.Errorf("err = %q, want %q", got, want)
	}
}

func TestTailKeepsLastLines(t *testing.T) {
	tl := &tail{n: 3}
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(tl, "line %d\n", i)
	}
	tl.Write([]byte("par"))
	tl.Write([]byte("tial"))
	got := tl.Lines()
	want := []string{"line 4", "line 5", "partial"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
