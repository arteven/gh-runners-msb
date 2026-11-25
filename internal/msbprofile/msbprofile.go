// Package msbprofile works out which Backend (local or remote) msb's active
// profile selects, by mirroring msb v0.8.2's own selection rules.
//
// Delete this package once superradcompany/microsandbox#2174 ships a public
// configured-backend getter (ADR-0004).
//
// It reads only JSON files and the environment, never the SDK. The upstream
// authority is the SDK's config/backend.rs, config/persistence.rs,
// config/mod.rs and context/profile.rs; SDK-bump PRs diff those files.
//
// Not mirrored: the managed file's allowed_backends policy (msb's
// require_backend). Nothing here needs it, and msb enforces it itself.
package msbprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// DefaultURL is the hosted msb API, used when a remote Backend names no URL.
const DefaultURL = "https://api.microsandbox.dev"

// Kind is the type of Backend.
type Kind string

const (
	Local  Kind = "local"
	Remote Kind = "remote"
)

// Source is what chose the Backend. msb reports managed and saved alike as
// "active_profile"; they are split here so the banner can say which it was.
type Source string

const (
	SourceManaged Source = "managed"
	SourceBackend Source = "MSB_BACKEND"
	SourceProfile Source = "MSB_PROFILE"
	SourceSaved   Source = "saved"
	SourceDefault Source = "default"
)

// Backend is the resolved result.
type Backend struct {
	Kind    Kind
	Source  Source
	Profile string // empty when no profile applies
	URL     string // remote only
	apiKey  string // remote only; read through APIKey
}

// APIKey returns the remote API key (empty on local).
func (b Backend) APIKey() string { return b.apiKey }

// String is the banner text. It never includes the key, and because it is the
// Stringer, %v, %+v and %s are safe too.
func (b Backend) String() string {
	var how string
	switch {
	case b.Profile != "":
		how = fmt.Sprintf("profile %q, %s", b.Profile, b.Source)
	case b.Source == SourceDefault:
		how = "default"
	default:
		how = string(b.Source)
	}
	s := fmt.Sprintf("%s (%s)", b.Kind, how)
	if b.Kind == Remote {
		s += " → " + b.URL
	}
	return s
}

// GoString keeps %#v from printing the key.
func (b Backend) GoString() string { return b.String() }

// Input is everything Resolve reads.
type Input struct {
	// Env is os.LookupEnv in production. Everything environmental goes through
	// it, including `env:` key references.
	Env func(string) (string, bool)
	// ManagedPath is DefaultManagedPath() in production.
	ManagedPath string

	// managedOwner is the uid that must own the managed file; 0 (root) is the
	// production rule. Same-package tests set their own euid.
	managedOwner uint32
}

// DefaultManagedPath is where msb looks for the machine-wide managed file.
func DefaultManagedPath() string {
	if runtime.GOOS == "darwin" {
		return "/Library/Application Support/microsandbox/managed.json"
	}
	return "/etc/microsandbox/managed.json"
}

// Sentinel error kinds, matched with errors.Is.
var (
	ErrConfig         = errors.New("unreadable or malformed config")
	ErrUnsafeManaged  = errors.New("unsafe managed file")
	ErrConfigVersion  = errors.New("unsupported config version")
	ErrUnknownProfile = errors.New("unknown profile")
	ErrNoKey          = errors.New("no API key")
	ErrKeyring        = errors.New("keyring key reference")
	ErrEnvKey         = errors.New("bad env key reference")
	ErrBadBackend     = errors.New("bad backend selection")
)

// Error names the source of the problem: a file path, an env var, or
// `profile "x"`. Unwrap returns the sentinel Kind.
type Error struct {
	Source string
	Kind   error
	Detail string
}

func (e *Error) Error() string { return e.Source + ": " + e.Detail }
func (e *Error) Unwrap() error { return e.Kind }

func errf(source string, kind error, format string, args ...any) *Error {
	return &Error{Source: source, Kind: kind, Detail: fmt.Sprintf(format, args...)}
}

type profile struct {
	Kind      string `json:"kind"`
	Backend   string `json:"backend"` // msb accepts "backend" as an alias of "kind"
	URL       string `json:"url"`
	APIKeyRef string `json:"api_key_ref"`
}

func (p profile) kind() (Kind, bool) {
	k := p.Kind
	if k == "" {
		k = p.Backend
	}
	switch k {
	case "local":
		return Local, true
	case "remote", "cloud":
		return Remote, true
	}
	return "", false
}

// patch is the part of msb's config we read. Unknown keys are ignored.
type patch struct {
	// ActiveProfile distinguishes absent from null: a managed null or "" is an
	// explicit clear, which still overrides MSB_PROFILE and the saved profile.
	ActiveProfile    *string
	ActiveProfileSet bool
	Profiles         map[string]profile
}

// Resolve applies msb's selection rules to the environment and files in in.
// There is no fallback to local: any problem is an error.
func Resolve(in Input) (Backend, error) {
	env := func(k string) (string, bool) {
		if in.Env == nil {
			return "", false
		}
		return in.Env(k)
	}

	userPath := configPath(env)
	user, err := loadConfig(userPath, "config")
	if err != nil {
		return Backend{}, err
	}
	managed, err := loadManaged(in.ManagedPath, in.managedOwner)
	if err != nil {
		return Backend{}, err
	}

	// Managed profiles replace user profiles of the same name, whole.
	profiles := map[string]profile{}
	for n, p := range user.Profiles {
		profiles[n] = p
	}
	for n, p := range managed.Profiles {
		profiles[n] = p
	}

	// Start from the saved selection, then layer overrides: MSB_PROFILE, then
	// MSB_BACKEND, then managed (describe() in config/backend.rs).
	var profName string
	source := SourceDefault
	if user.ActiveProfile != nil && strings.TrimSpace(*user.ActiveProfile) != "" {
		profName = *user.ActiveProfile
		source = SourceSaved
	}
	var kind string
	kindSet := false

	if v, _ := env("MSB_PROFILE"); strings.TrimSpace(v) != "" {
		profName, source = v, SourceProfile
	}
	if v, ok := env("MSB_BACKEND"); ok {
		kind, kindSet = v, true
		source = SourceBackend
	}
	if managed.ActiveProfileSet {
		profName = ""
		if managed.ActiveProfile != nil {
			profName = *managed.ActiveProfile
		}
		if strings.TrimSpace(profName) != "" {
			kindSet = false // a named managed profile beats MSB_BACKEND
			source = SourceManaged
		} else if !kindSet {
			source = SourceManaged
		}
	}
	profName = strings.TrimSpace(profName)

	var wantKind Kind // "" = unspecified
	if kindSet {
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "local":
			wantKind = Local
		case "remote", "cloud":
			wantKind = Remote
		default:
			return Backend{}, errf("MSB_BACKEND", ErrBadBackend,
				"must be 'local', 'remote' or legacy 'cloud', got %q", kind)
		}
	}

	// select() in config/backend.rs.
	if wantKind == Local {
		return Backend{Kind: Local, Source: source}, nil
	}
	apiKey, _ := env("MSB_API_KEY")
	apiKey = strings.TrimSpace(apiKey)
	if wantKind == Remote && apiKey != "" {
		url, _ := env("MSB_API_URL")
		return Backend{Kind: Remote, Source: source, URL: orDefault(url), apiKey: apiKey}, nil
	}
	if profName != "" {
		p, ok := profiles[profName]
		if !ok {
			return Backend{}, errf(fmt.Sprintf("profile %q", profName), ErrUnknownProfile,
				"active profile not found in config %s", userPath)
		}
		k, ok := p.kind()
		if !ok {
			return Backend{}, errf(userPath, ErrConfig, "profile %q: kind must be 'local', 'remote' or 'cloud'", profName)
		}
		if wantKind == Remote && k != Remote {
			return Backend{}, errf("MSB_BACKEND", ErrBadBackend,
				"MSB_BACKEND=remote cannot select local profile %q", profName)
		}
		if k == Local {
			return Backend{Kind: Local, Source: source, Profile: profName}, nil
		}
		key, err := resolveKeyRef(profName, p.APIKeyRef, env)
		if err != nil {
			return Backend{}, err
		}
		// MSB_API_URL is deliberately ignored: a profile owns its endpoint.
		return Backend{Kind: Remote, Source: source, Profile: profName, URL: orDefault(p.URL), apiKey: key}, nil
	}
	if wantKind == Remote {
		return Backend{}, errf("MSB_BACKEND", ErrNoKey,
			"MSB_BACKEND=remote requires a non-empty MSB_API_KEY or a remote profile")
	}
	// A bare MSB_API_KEY is credential material, not backend intent.
	return Backend{Kind: Local, Source: source}, nil
}

func orDefault(url string) string {
	if url = strings.TrimSpace(url); url != "" {
		return url
	}
	return DefaultURL
}

// resolveKeyRef mirrors resolve_api_key_ref in context/profile.rs.
func resolveKeyRef(name, ref string, env func(string) (string, bool)) (string, error) {
	src := fmt.Sprintf("profile %q", name)
	switch {
	case ref == "":
		return "", errf(src, ErrNoKey, "remote profile has no api_key_ref")
	case strings.HasPrefix(ref, "env:"):
		v := strings.TrimSpace(strings.TrimPrefix(ref, "env:"))
		if v == "" {
			return "", errf(src, ErrEnvKey, "api_key_ref 'env:' must name an env var")
		}
		val, ok := env(v)
		if !ok {
			return "", errf(src, ErrEnvKey, "env var %q not set", v)
		}
		if val = strings.TrimSpace(val); val == "" {
			return "", errf(src, ErrEnvKey, "env var %q must not be empty", v)
		}
		return val, nil
	case strings.HasPrefix(ref, "inline:"):
		key := strings.TrimSpace(strings.TrimPrefix(ref, "inline:"))
		if key == "" {
			return "", errf(src, ErrNoKey, "api_key_ref 'inline:' must include an API key")
		}
		return key, nil
	case strings.HasPrefix(ref, "keyring:"):
		return "", errf(src, ErrKeyring,
			"api_key_ref 'keyring:' resolution is not yet wired — use 'env:' or 'inline:' for now")
	}
	return "", errf(src, ErrConfig, "api_key_ref must start with 'env:', 'inline:', or 'keyring:'")
}

// configPath mirrors config_path() in config/mod.rs: MSB_CONFIG_PATH, else
// $MSB_HOME/config.json, else $HOME/.microsandbox/config.json.
func configPath(env func(string) (string, bool)) string {
	if p, _ := env("MSB_CONFIG_PATH"); p != "" {
		return p
	}
	home, _ := env("MSB_HOME")
	if home == "" {
		h, _ := env("HOME")
		if h == "" {
			h = "."
		}
		home = filepath.Join(h, ".microsandbox")
	}
	return filepath.Join(home, "config.json")
}

// parseObject reads a JSON object and checks its version (omitted means 1).
func parseObject(raw []byte, path, what string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		if err == nil {
			err = errors.New("not a JSON object")
		}
		return nil, errf(path, ErrConfig, "invalid %s: %v", what, err)
	}
	if v, ok := obj["version"]; ok {
		var n json.Number
		if json.Unmarshal(v, &n) != nil || n.String() != "1" {
			return nil, errf(path, ErrConfigVersion,
				"unsupported %s version %s (only 1 is supported); upgrade the extension: gh extension upgrade runners-msb", what, v)
		}
	}
	return obj, nil
}

func decodePatch(obj map[string]json.RawMessage, path string) (patch, error) {
	var p patch
	if raw, ok := obj["active_profile"]; ok {
		p.ActiveProfileSet = true
		if err := json.Unmarshal(raw, &p.ActiveProfile); err != nil {
			return p, errf(path, ErrConfig, "active_profile must be a string or null")
		}
	}
	if raw, ok := obj["profiles"]; ok {
		if err := json.Unmarshal(raw, &p.Profiles); err != nil {
			return p, errf(path, ErrConfig, "invalid profiles: %v", err)
		}
		for n, pr := range p.Profiles {
			if _, ok := pr.kind(); !ok {
				return p, errf(path, ErrConfig, "profile %q: kind must be 'local', 'remote' or 'cloud'", n)
			}
		}
	}
	return p, nil
}

// loadConfig reads the user config; a missing file is an empty config.
func loadConfig(path, what string) (patch, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return patch{}, nil
	}
	if err != nil {
		return patch{}, errf(path, ErrConfig, "reading %s: %v", what, err)
	}
	obj, err := parseObject(raw, path, what)
	if err != nil {
		return patch{}, err
	}
	return decodePatch(obj, path)
}

// loadManaged reads the machine-wide managed file; a missing file is none. An
// existing one and its parent dir must be owned by owner and not group/world
// writable (persistence.rs validate_permissions).
func loadManaged(path string, owner uint32) (patch, error) {
	if path == "" {
		return patch{}, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return patch{}, nil
	}
	if err != nil {
		return patch{}, errf(path, ErrConfig, "reading managed config: %v", err)
	}
	for _, p := range []string{path, filepath.Dir(path)} {
		fi, err := os.Stat(p)
		if err != nil {
			return patch{}, errf(path, ErrUnsafeManaged, "checking permissions on %s: %v", p, err)
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return patch{}, errf(path, ErrUnsafeManaged, "cannot check ownership of %s on this platform", p)
		}
		if st.Uid != owner || fi.Mode().Perm()&0o022 != 0 {
			return patch{}, errf(path, ErrUnsafeManaged,
				"unsafe permissions on %s: expected owner uid %d and no group or world write access, found uid %d and mode %04o; ask an administrator to correct ownership and permissions",
				p, owner, st.Uid, fi.Mode().Perm())
		}
	}
	obj, err := parseObject(raw, path, "managed config")
	if err != nil {
		return patch{}, err
	}
	var overrides map[string]json.RawMessage
	if o, ok := obj["overrides"]; ok {
		if err := json.Unmarshal(o, &overrides); err != nil {
			return patch{}, errf(path, ErrConfig, "invalid overrides: %v", err)
		}
	}
	return decodePatch(overrides, path)
}
