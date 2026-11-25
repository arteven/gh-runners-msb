// Package github is the Pool's only seam to GitHub.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
	"github.com/cli/go-gh/v2/pkg/repository"
)

// ErrBusy is returned by DeleteRunner when GitHub refuses (422) because the
// Runner is running a job.
var ErrBusy = errors.New("runner is busy")

// JITConfig is a just-in-time Runner registration.
type JITConfig struct {
	Encoded  string
	RunnerID int64
}

// Port is what the Pool needs from GitHub.
type Port interface {
	// GenerateJITConfig registers an ephemeral Runner called name.
	GenerateJITConfig(ctx context.Context, name string, labels []string) (JITConfig, error)
	// DeleteRunner deregisters a Runner. It returns nil on 2xx and 404, and an
	// error satisfying errors.Is(err, ErrBusy) on 422.
	DeleteRunner(ctx context.Context, id int64) error
}

const (
	jitTimeout    = 60 * time.Second
	deleteTimeout = 30 * time.Second
	// Group 1 is the default runner group; _work is the runner's work folder.
	runnerGroupID = 1
	workFolder    = "_work"
)

type adapter struct {
	repo      repository.Repository
	tokenFor  func(host string) string
	transport http.RoundTripper // nil in production; tests redirect requests
}

// New returns the go-gh backed Port for repo.
func New(repo repository.Repository) Port {
	return newAdapter(repo, func(host string) string {
		tok, _ := auth.TokenForHost(host)
		return tok
	}, nil)
}

func newAdapter(repo repository.Repository, tokenFor func(string) string, rt http.RoundTripper) *adapter {
	return &adapter{repo: repo, tokenFor: tokenFor, transport: rt}
}

// client is rebuilt for every call so the token is re-resolved each time
// (gh may refresh it). Log options are left unset so go-gh's own GH_DEBUG
// handling traces the calls.
func (a *adapter) client() (*api.RESTClient, error) {
	return api.NewRESTClient(api.ClientOptions{
		Host:      a.repo.Host,
		AuthToken: a.tokenFor(a.repo.Host),
		Transport: a.transport,
	})
}

func (a *adapter) path(suffix string) string {
	return fmt.Sprintf("repos/%s/%s/actions/runners/%s", a.repo.Owner, a.repo.Name, suffix)
}

func (a *adapter) GenerateJITConfig(ctx context.Context, name string, labels []string) (JITConfig, error) {
	ctx, cancel := context.WithTimeout(ctx, jitTimeout)
	defer cancel()
	c, err := a.client()
	if err != nil {
		return JITConfig{}, err
	}
	body, err := json.Marshal(map[string]any{
		"name":            name,
		"runner_group_id": runnerGroupID,
		"labels":          labels,
		"work_folder":     workFolder,
	})
	if err != nil {
		return JITConfig{}, err
	}
	var resp struct {
		Runner struct {
			ID int64 `json:"id"`
		} `json:"runner"`
		Encoded string `json:"encoded_jit_config"`
	}
	if err := c.DoWithContext(ctx, http.MethodPost, a.path("generate-jitconfig"), bytes.NewReader(body), &resp); err != nil {
		return JITConfig{}, fmt.Errorf("generate JIT config: %w", err)
	}
	if resp.Encoded == "" || resp.Runner.ID == 0 {
		return JITConfig{}, errors.New("generate JIT config: response lacks encoded_jit_config or runner id")
	}
	return JITConfig{Encoded: resp.Encoded, RunnerID: resp.Runner.ID}, nil
}

func (a *adapter) DeleteRunner(ctx context.Context, id int64) error {
	ctx, cancel := context.WithTimeout(ctx, deleteTimeout)
	defer cancel()
	c, err := a.client()
	if err != nil {
		return err
	}
	err = c.DoWithContext(ctx, http.MethodDelete, a.path(fmt.Sprint(id)), nil, nil)
	var he *api.HTTPError
	if errors.As(err, &he) {
		switch he.StatusCode {
		case http.StatusNotFound:
			return nil
		case http.StatusUnprocessableEntity:
			return fmt.Errorf("delete runner %d: %w", id, ErrBusy)
		}
	}
	if err != nil {
		return fmt.Errorf("delete runner %d: %w", id, err)
	}
	return nil
}
