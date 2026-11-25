package github

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/cli/go-gh/v2/pkg/repository"
)

// rewrite sends every request to the httptest server.
type rewrite struct{ target *url.URL }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

type recorded struct {
	method, path, auth string
	body               []byte
}

func setup(t *testing.T, status int, respBody string, tokens ...string) (*adapter, func() []recorded) {
	t.Helper()
	var mu sync.Mutex
	var got []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, recorded{r.Method, r.URL.Path, r.Header.Get("Authorization"), b})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	i := 0
	tokenFor := func(string) string {
		tok := tokens[min(i, len(tokens)-1)]
		i++
		return tok
	}
	a := newAdapter(repository.Repository{Host: "github.com", Owner: "o", Name: "r"}, tokenFor, rewrite{u})
	return a, func() []recorded { mu.Lock(); defer mu.Unlock(); return append([]recorded(nil), got...) }
}

func TestGenerateJITConfigRequestAndResponse(t *testing.T) {
	a, reqs := setup(t, 201, `{"runner":{"id":42,"name":"n"},"encoded_jit_config":"ENC"}`, "t1")
	jit, err := a.GenerateJITConfig(t.Context(), "msb-gh-aaaaaa-1-deadbeef", []string{"self-hosted", "msb-docker"})
	if err != nil {
		t.Fatal(err)
	}
	if jit.Encoded != "ENC" || jit.RunnerID != 42 {
		t.Errorf("jit = %+v", jit)
	}
	r := reqs()[0]
	if r.method != "POST" || r.path != "/repos/o/r/actions/runners/generate-jitconfig" {
		t.Errorf("request = %s %s", r.method, r.path)
	}
	var body map[string]any
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatal(err)
	}
	want := `{"labels":["self-hosted","msb-docker"],"name":"msb-gh-aaaaaa-1-deadbeef","runner_group_id":1,"work_folder":"_work"}`
	gotJSON, _ := json.Marshal(body) // map keys marshal sorted
	if string(gotJSON) != want {
		t.Errorf("body = %s\nwant   %s", gotJSON, want)
	}
}

func TestGenerateJITConfigErrors(t *testing.T) {
	a, _ := setup(t, 403, `{"message":"nope"}`, "t")
	if _, err := a.GenerateJITConfig(t.Context(), "n", nil); err == nil {
		t.Error("want error on 403")
	}
	a, _ = setup(t, 201, `{}`, "t")
	if _, err := a.GenerateJITConfig(t.Context(), "n", nil); err == nil {
		t.Error("want error on empty response")
	}
}

func TestDeleteRunnerStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		status   int
		wantNil  bool
		wantBusy bool
	}{
		{204, true, false}, {404, true, false}, {422, false, true}, {500, false, false},
	} {
		a, reqs := setup(t, tc.status, `{"message":"x"}`, "t")
		err := a.DeleteRunner(t.Context(), 42)
		if (err == nil) != tc.wantNil {
			t.Errorf("%d: err = %v", tc.status, err)
		}
		if errors.Is(err, ErrBusy) != tc.wantBusy {
			t.Errorf("%d: ErrBusy = %v, want %v (err %v)", tc.status, errors.Is(err, ErrBusy), tc.wantBusy, err)
		}
		if r := reqs()[0]; r.method != "DELETE" || r.path != "/repos/o/r/actions/runners/42" {
			t.Errorf("%d: request = %s %s", tc.status, r.method, r.path)
		}
	}
}

func TestTokenReResolvedBeforeEveryCall(t *testing.T) {
	a, reqs := setup(t, 204, ``, "t1", "t2")
	for range 2 {
		if err := a.DeleteRunner(t.Context(), 1); err != nil {
			t.Fatal(err)
		}
	}
	got := reqs()
	if len(got) != 2 || got[0].auth != "token t1" || got[1].auth != "token t2" {
		t.Errorf("auth headers = %q, %q", got[0].auth, got[1].auth)
	}
}
