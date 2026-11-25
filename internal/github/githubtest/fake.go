// Package githubtest provides a fake github.Port.
package githubtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"sync"

	"github.com/arteven/gh-runners-msb/internal/github"
)

// Registration is one JIT registration the Fake handed out.
type Registration struct {
	ID      int64
	Name    string
	Labels  []string
	Encoded string
}

// Fake records registrations and deletions and refuses to delete busy Runners.
type Fake struct {
	mu      sync.Mutex
	nextID  int64
	regs    []Registration
	busy    map[int64]bool
	deleted []int64
}

var _ github.Port = (*Fake)(nil)

func (f *Fake) GenerateJITConfig(_ context.Context, name string, labels []string) (github.JITConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	buf := make([]byte, 8)
	rand.Read(buf)
	enc := fmt.Sprintf("fake-jit-%d-%s", f.nextID, hex.EncodeToString(buf))
	f.regs = append(f.regs, Registration{ID: f.nextID, Name: name, Labels: slices.Clone(labels), Encoded: enc})
	return github.JITConfig{Encoded: enc, RunnerID: f.nextID}, nil
}

func (f *Fake) DeleteRunner(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy[id] {
		return fmt.Errorf("delete runner %d: %w", id, github.ErrBusy)
	}
	f.deleted = append(f.deleted, id) // unknown IDs act like a 404: success
	return nil
}

// SetBusy makes DeleteRunner return ErrBusy for id while busy is true.
func (f *Fake) SetBusy(id int64, busy bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy == nil {
		f.busy = map[int64]bool{}
	}
	f.busy[id] = busy
}

// Registrations returns the registrations so far, oldest first.
func (f *Fake) Registrations() []Registration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.regs)
}

// Deleted returns the IDs deleted so far, in order.
func (f *Fake) Deleted() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.deleted)
}
