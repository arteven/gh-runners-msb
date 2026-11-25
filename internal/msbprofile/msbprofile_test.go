package msbprofile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	remoteWork = `{"kind":"remote","api_key_ref":"inline:work-key"}`
	remoteURL  = `{"kind":"remote","url":"https://work.example","api_key_ref":"inline:work-key"}`
)

func cfg(active string, profiles string) string {
	s := `{"version":1`
	if active != "" {
		s += fmt.Sprintf(`,"active_profile":%q`, active)
	}
	if profiles != "" {
		s += `,"profiles":` + profiles
	}
	return s + "}"
}

func managedFile(active string, profiles string) string {
	inner := ""
	if active != "-" {
		inner = fmt.Sprintf(`"active_profile":%s`, active)
	}
	if profiles != "" {
		if inner != "" {
			inner += ","
		}
		inner += `"profiles":` + profiles
	}
	return `{"version":1,"overrides":{` + inner + `}}`
}

type fixture struct {
	config  string // contents of config.json; "" = absent
	managed string // contents of managed.json; "" = absent
	env     map[string]string
}

// run lays the fixture out in a temp dir and resolves it.
func (f fixture) run(t *testing.T) (Backend, error) {
	t.Helper()
	dir := t.TempDir()
	managedDir := filepath.Join(dir, "etc")
	if err := os.Mkdir(managedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.json")
	if f.config != "" {
		if err := os.WriteFile(cfgPath, []byte(f.config), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	managedPath := filepath.Join(managedDir, "managed.json")
	if f.managed != "" {
		if err := os.WriteFile(managedPath, []byte(f.managed), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"MSB_CONFIG_PATH": cfgPath}
	for k, v := range f.env {
		env[k] = v
	}
	return Resolve(Input{
		Env:          func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		ManagedPath:  managedPath,
		managedOwner: uint32(os.Geteuid()),
	})
}

func TestResolve(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		f    fixture
		want Backend
		key  string
	}{
		{"nothing set", fixture{}, Backend{Kind: Local, Source: SourceDefault}, ""},
		{"saved local profile",
			fixture{config: cfg("dev", `{"dev":{"kind":"local"}}`)},
			Backend{Kind: Local, Source: SourceSaved, Profile: "dev"}, ""},
		{"saved remote profile, no url, inline key",
			fixture{config: cfg("work", `{"work":`+remoteWork+`}`)},
			Backend{Kind: Remote, Source: SourceSaved, Profile: "work", URL: DefaultURL}, "work-key"},
		{"profile url beats MSB_API_URL",
			fixture{config: cfg("work", `{"work":`+remoteURL+`}`), env: map[string]string{"MSB_API_URL": "https://other.example"}},
			Backend{Kind: Remote, Source: SourceSaved, Profile: "work", URL: "https://work.example"}, "work-key"},
		{"MSB_PROFILE beats saved",
			fixture{config: cfg("dev", `{"dev":{"kind":"local"},"work":`+remoteWork+`}`), env: map[string]string{"MSB_PROFILE": "work"}},
			Backend{Kind: Remote, Source: SourceProfile, Profile: "work", URL: DefaultURL}, "work-key"},
		{"MSB_BACKEND=local beats MSB_PROFILE",
			fixture{config: cfg("", `{"work":`+remoteWork+`}`), env: map[string]string{"MSB_BACKEND": "local", "MSB_PROFILE": "work"}},
			Backend{Kind: Local, Source: SourceBackend}, ""},
		{"MSB_BACKEND=remote with key, default url",
			fixture{env: map[string]string{"MSB_BACKEND": "remote", "MSB_API_KEY": " k1 "}},
			Backend{Kind: Remote, Source: SourceBackend, URL: DefaultURL}, "k1"},
		{"MSB_BACKEND=remote with key and MSB_API_URL, MSB_PROFILE ignored",
			fixture{config: cfg("", `{"work":`+remoteURL+`}`), env: map[string]string{
				"MSB_BACKEND": "remote", "MSB_API_KEY": "k1", "MSB_API_URL": " https://env.example ", "MSB_PROFILE": "work"}},
			Backend{Kind: Remote, Source: SourceBackend, URL: "https://env.example"}, "k1"},
		{"MSB_BACKEND=remote, no key, saved remote profile",
			fixture{config: cfg("work", `{"work":`+remoteWork+`}`), env: map[string]string{"MSB_BACKEND": "remote"}},
			Backend{Kind: Remote, Source: SourceBackend, Profile: "work", URL: DefaultURL}, "work-key"},
		{"MSB_BACKEND=Cloud means remote",
			fixture{env: map[string]string{"MSB_BACKEND": " Cloud ", "MSB_API_KEY": "k"}},
			Backend{Kind: Remote, Source: SourceBackend, URL: DefaultURL}, "k"},
		{"managed active_profile beats MSB_BACKEND and MSB_PROFILE",
			fixture{
				config:  cfg("", `{"mine":{"kind":"local"}}`),
				managed: managedFile(`"corp"`, `{"corp":`+remoteURL+`}`),
				env:     map[string]string{"MSB_BACKEND": "local", "MSB_PROFILE": "mine"}},
			Backend{Kind: Remote, Source: SourceManaged, Profile: "corp", URL: "https://work.example"}, "work-key"},
		{"managed profile replaces the user profile of the same name",
			fixture{
				config:  cfg("work", `{"work":{"kind":"local"}}`),
				managed: managedFile("-", `{"work":`+remoteWork+`}`)},
			Backend{Kind: Remote, Source: SourceSaved, Profile: "work", URL: DefaultURL}, "work-key"},
		{"managed clear keeps explicit MSB_BACKEND and drops the saved profile",
			fixture{
				config:  cfg("saved", `{"saved":{"kind":"local"}}`),
				managed: managedFile(`null`, ""),
				env:     map[string]string{"MSB_BACKEND": "remote", "MSB_API_KEY": "k"}},
			Backend{Kind: Remote, Source: SourceBackend, URL: DefaultURL}, "k"},
		{"MSB_API_KEY alone stays local",
			fixture{env: map[string]string{"MSB_API_KEY": "k"}},
			Backend{Kind: Local, Source: SourceDefault}, ""},
		{"backend is an alias of kind",
			fixture{config: cfg("w", `{"w":{"backend":"cloud","api_key_ref":"inline:k"}}`)},
			Backend{Kind: Remote, Source: SourceSaved, Profile: "w", URL: DefaultURL}, "k"},
		{"env: key reference",
			fixture{config: cfg("w", `{"w":{"kind":"remote","api_key_ref":"env: MY_KEY"}}`), env: map[string]string{"MY_KEY": " secret "}},
			Backend{Kind: Remote, Source: SourceSaved, Profile: "w", URL: DefaultURL}, "secret"},
		{"unknown config keys are ignored",
			fixture{config: `{"version":1,"runtime":{"x":1},"profiles":{"a":{"kind":"local","extra":true}},"active_profile":"a"}`},
			Backend{Kind: Local, Source: SourceSaved, Profile: "a"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.f.run(t)
			if err != nil {
				t.Fatal(err)
			}
			if got.apiKey != tc.key || got.APIKey() != tc.key {
				t.Errorf("api key = %q, want %q", got.APIKey(), tc.key)
			}
			got.apiKey, tc.want.apiKey = "", ""
			if got != tc.want {
				t.Errorf("got %#v\nwant %#v", got, tc.want)
			}
		})
	}
}

func TestConfigPathRules(t *testing.T) {
	t.Parallel()
	local := `{"active_profile":"w","profiles":{"w":{"kind":"remote","api_key_ref":"inline:k","url":"https://%s"}}}`
	write := func(path, host string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(fmt.Sprintf(local, host)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	msbHome := filepath.Join(root, "msbhome")
	explicit := filepath.Join(root, "explicit", "c.json")
	write(filepath.Join(home, ".microsandbox", "config.json"), "from-home")
	write(filepath.Join(msbHome, "config.json"), "from-msb-home")
	write(explicit, "from-explicit")

	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"HOME fallback", map[string]string{"HOME": home}, "https://from-home"},
		{"MSB_HOME beats HOME", map[string]string{"HOME": home, "MSB_HOME": msbHome}, "https://from-msb-home"},
		{"MSB_CONFIG_PATH beats MSB_HOME", map[string]string{"HOME": home, "MSB_HOME": msbHome, "MSB_CONFIG_PATH": explicit}, "https://from-explicit"},
		{"empty MSB_HOME falls through", map[string]string{"HOME": home, "MSB_HOME": ""}, "https://from-home"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Resolve(Input{Env: func(k string) (string, bool) { v, ok := tc.env[k]; return v, ok }})
			if err != nil {
				t.Fatal(err)
			}
			if got.URL != tc.want {
				t.Errorf("URL = %q, want %q", got.URL, tc.want)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		f      fixture
		kind   error
		src    string
		detail string
	}{
		{name: "config bad JSON", f: fixture{config: `{nope`}, kind: ErrConfig, src: "config.json"},
		{name: "config not an object", f: fixture{config: `[1]`}, kind: ErrConfig, src: "config.json"},
		{name: "config bad profile kind", f: fixture{config: cfg("", `{"x":{"kind":"vm"}}`)}, kind: ErrConfig, src: "config.json"},
		{name: "config version 2", f: fixture{config: `{"version":2}`}, kind: ErrConfigVersion, src: "config.json",
			detail: "gh extension upgrade runners-msb"},
		{name: "managed bad JSON", f: fixture{managed: `{nope`}, kind: ErrConfig, src: "managed.json"},
		{name: "managed version 2", f: fixture{managed: `{"version":2}`}, kind: ErrConfigVersion, src: "managed.json",
			detail: "gh extension upgrade runners-msb"},
		{name: "unknown profile via MSB_PROFILE", f: fixture{env: map[string]string{"MSB_PROFILE": "ghost"}},
			kind: ErrUnknownProfile, src: `profile "ghost"`},
		{name: "unknown profile via saved", f: fixture{config: cfg("ghost", "")},
			kind: ErrUnknownProfile, src: `profile "ghost"`},
		{name: "MSB_BACKEND bogus", f: fixture{env: map[string]string{"MSB_BACKEND": "bogus"}},
			kind: ErrBadBackend, src: "MSB_BACKEND"},
		{name: "MSB_BACKEND empty", f: fixture{env: map[string]string{"MSB_BACKEND": ""}},
			kind: ErrBadBackend, src: "MSB_BACKEND"},
		{name: "MSB_BACKEND=remote selects a local profile",
			f:    fixture{config: cfg("dev", `{"dev":{"kind":"local"}}`), env: map[string]string{"MSB_BACKEND": "remote"}},
			kind: ErrBadBackend, src: "MSB_BACKEND"},
		{name: "remote without key or profile", f: fixture{env: map[string]string{"MSB_BACKEND": "remote"}},
			kind: ErrNoKey, src: "MSB_BACKEND"},
		{name: "remote with blank key", f: fixture{env: map[string]string{"MSB_BACKEND": "remote", "MSB_API_KEY": "  "}},
			kind: ErrNoKey, src: "MSB_BACKEND"},
		{name: "profile without api_key_ref",
			f:    fixture{config: cfg("w", `{"w":{"kind":"remote"}}`)},
			kind: ErrNoKey, src: `profile "w"`},
		{name: "empty inline key",
			f:    fixture{config: cfg("w", `{"w":{"kind":"remote","api_key_ref":"inline:  "}}`)},
			kind: ErrNoKey, src: `profile "w"`},
		{name: "keyring ref",
			f:    fixture{config: cfg("w", `{"w":{"kind":"remote","api_key_ref":"keyring:svc:name"}}`)},
			kind: ErrKeyring, src: `profile "w"`, detail: "use 'env:' or 'inline:'"},
		{name: "env ref with no name",
			f:    fixture{config: cfg("w", `{"w":{"kind":"remote","api_key_ref":"env:"}}`)},
			kind: ErrEnvKey, src: `profile "w"`},
		{name: "env ref unset",
			f:    fixture{config: cfg("w", `{"w":{"kind":"remote","api_key_ref":"env:NOPE"}}`)},
			kind: ErrEnvKey, src: `profile "w"`},
		{name: "env ref blank",
			f:    fixture{config: cfg("w", `{"w":{"kind":"remote","api_key_ref":"env:BLANK"}}`), env: map[string]string{"BLANK": "  "}},
			kind: ErrEnvKey, src: `profile "w"`},
		{name: "unknown ref prefix",
			f:    fixture{config: cfg("w", `{"w":{"kind":"remote","api_key_ref":"vault:x"}}`)},
			kind: ErrConfig, src: `profile "w"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.f.run(t)
			assertErr(t, err, tc.kind, tc.src, tc.detail)
		})
	}
}

func assertErr(t *testing.T, err error, kind error, src, detail string) {
	t.Helper()
	if !errors.Is(err, kind) {
		t.Fatalf("err = %v, want kind %v", err, kind)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("err %v is not *Error", err)
	}
	if !strings.Contains(e.Source, src) {
		t.Errorf("Source = %q, want it to contain %q", e.Source, src)
	}
	if !strings.Contains(e.Detail, detail) {
		t.Errorf("Detail = %q, want it to contain %q", e.Detail, detail)
	}
}

func TestManagedFilePermissions(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, fileMode, dirMode os.FileMode) string {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "etc")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, "managed.json")
		if err := os.WriteFile(p, []byte(`{"version":1}`), 0o644); err != nil {
			t.Fatal(err)
		}
		// chmod explicitly: the umask would mask group-write on create.
		if err := os.Chmod(p, fileMode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, dirMode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	resolve := func(path string, owner uint32) error {
		_, err := Resolve(Input{
			Env:          func(string) (string, bool) { return "", false },
			ManagedPath:  path,
			managedOwner: owner,
		})
		return err
	}
	euid := uint32(os.Geteuid())

	if err := resolve(setup(t, 0o644, 0o755), euid); err != nil {
		t.Errorf("safe managed file: %v", err)
	}
	for _, tc := range []struct {
		name      string
		file, dir os.FileMode
		owner     uint32
	}{
		{"group-writable file", 0o664, 0o755, euid},
		{"world-writable file", 0o646, 0o755, euid},
		{"group-writable parent dir", 0o644, 0o775, euid},
		{"wrong owner", 0o644, 0o755, euid + 1},
	} {
		err := resolve(setup(t, tc.file, tc.dir), tc.owner)
		if !errors.Is(err, ErrUnsafeManaged) {
			t.Errorf("%s: err = %v, want ErrUnsafeManaged", tc.name, err)
		}
	}
}

func TestMissingManagedFileIsNoManagedFile(t *testing.T) {
	t.Parallel()
	got, err := Resolve(Input{
		Env:         func(string) (string, bool) { return "", false },
		ManagedPath: filepath.Join(t.TempDir(), "nope", "managed.json"),
	})
	if err != nil || got.Kind != Local || got.Source != SourceDefault {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestString(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		b    Backend
		want string
	}{
		{Backend{Kind: Local, Source: SourceDefault}, "local (default)"},
		{Backend{Kind: Local, Source: SourceSaved, Profile: "dev"}, `local (profile "dev", saved)`},
		{Backend{Kind: Local, Source: SourceBackend}, "local (MSB_BACKEND)"},
		{Backend{Kind: Remote, Source: SourceBackend, URL: DefaultURL}, "remote (MSB_BACKEND) → https://api.microsandbox.dev"},
		{Backend{Kind: Remote, Source: SourceSaved, Profile: "work", URL: "https://w.example"}, `remote (profile "work", saved) → https://w.example`},
	} {
		if got := tc.b.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
	}
}

func TestFormattingNeverLeaksTheKey(t *testing.T) {
	t.Parallel()
	b := Backend{Kind: Remote, Source: SourceSaved, Profile: "w", URL: DefaultURL, apiKey: "sekret"}
	for _, verb := range []string{"%v", "%+v", "%s", "%#v"} {
		for _, arg := range []any{b, &b} {
			if got := fmt.Sprintf(verb, arg); strings.Contains(got, "sekret") {
				t.Errorf("%s of %T leaked the key: %s", verb, arg, got)
			}
		}
	}
}
