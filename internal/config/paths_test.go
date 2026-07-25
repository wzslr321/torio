package config

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// envFunc builds a Getenv stub from a map so tests never touch the real env.
func envFunc(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func homeFunc(dir string) func() (string, error) {
	return func() (string, error) { return dir, nil }
}

func TestResolvePathsUsesXDGDefaults(t *testing.T) {
	home := t.TempDir()
	p, err := ResolvePaths(Options{
		Getenv:  envFunc(nil),
		HomeDir: homeFunc(home),
	})
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	wantConfigDir := filepath.Join(home, ".config", appDir)
	if p.ConfigDir != wantConfigDir {
		t.Errorf("ConfigDir = %q, want %q", p.ConfigDir, wantConfigDir)
	}
	wantConfigFile := filepath.Join(wantConfigDir, configFileName)
	if p.ConfigFile != wantConfigFile {
		t.Errorf("ConfigFile = %q, want %q", p.ConfigFile, wantConfigFile)
	}
	wantStateDir := filepath.Join(home, ".local", "state", appDir)
	if p.StateDir != wantStateDir {
		t.Errorf("StateDir = %q, want %q", p.StateDir, wantStateDir)
	}
}

func TestResolvePathsHonorsXDGOverrides(t *testing.T) {
	cfgHome := t.TempDir()
	stateHome := t.TempDir()
	p, err := ResolvePaths(Options{
		Getenv: envFunc(map[string]string{
			"XDG_CONFIG_HOME": cfgHome,
			"XDG_STATE_HOME":  stateHome,
		}),
		HomeDir: homeFunc("/nonexistent-should-not-be-used"),
	})
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if want := filepath.Join(cfgHome, appDir); p.ConfigDir != want {
		t.Errorf("ConfigDir = %q, want %q", p.ConfigDir, want)
	}
	if want := filepath.Join(stateHome, appDir); p.StateDir != want {
		t.Errorf("StateDir = %q, want %q", p.StateDir, want)
	}
}

func TestResolvePathsRejectsRelativeXDG(t *testing.T) {
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME"} {
		_, err := ResolvePaths(Options{
			Getenv:  envFunc(map[string]string{key: "relative/path"}),
			HomeDir: homeFunc(t.TempDir()),
		})
		if err == nil {
			t.Errorf("%s relative value: want error, got nil", key)
		}
	}
}

func TestResolvePathsExplicitConfigIsCanonicalAndAbsolute(t *testing.T) {
	dir := t.TempDir()
	// A path with a redundant "." and "//" must canonicalize.
	explicit := filepath.Join(dir, ".", "sub", "..", "my.json")
	p, err := ResolvePaths(Options{
		ConfigPath: explicit,
		Getenv:     envFunc(nil),
		HomeDir:    homeFunc(t.TempDir()),
	})
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	want := filepath.Join(dir, "my.json")
	if p.ConfigFile != want {
		t.Errorf("ConfigFile = %q, want canonical %q", p.ConfigFile, want)
	}
	if !filepath.IsAbs(p.ConfigFile) {
		t.Errorf("ConfigFile %q is not absolute", p.ConfigFile)
	}
}

func TestResolvePathsExplicitStateDirCanonical(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "state", "..", "state2")
	p, err := ResolvePaths(Options{
		StateDir: explicit,
		Getenv:   envFunc(nil),
		HomeDir:  homeFunc(t.TempDir()),
	})
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if want := filepath.Join(dir, "state2"); p.StateDir != want {
		t.Errorf("StateDir = %q, want %q", p.StateDir, want)
	}
}

func TestResolvePathsRelativeExplicitIsMadeAbsolute(t *testing.T) {
	p, err := ResolvePaths(Options{
		ConfigPath: "rel.json",
		Getenv:     envFunc(nil),
		HomeDir:    homeFunc(t.TempDir()),
	})
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if !filepath.IsAbs(p.ConfigFile) {
		t.Errorf("relative --config must be made absolute, got %q", p.ConfigFile)
	}
}

func TestResolvePathsErrorsWhenHomeNeededButMissing(t *testing.T) {
	_, err := ResolvePaths(Options{
		Getenv:  envFunc(nil),
		HomeDir: func() (string, error) { return "", errNoHome },
	})
	if err == nil {
		t.Fatalf("want error when HOME unavailable and XDG unset")
	}
}

// TestResolvePathsRejectsRelativeHomeFallback locks the fail-closed policy for
// the default (XDG-unset) path: a non-absolute HOME must be rejected, not
// silently canonicalized against the working directory. Otherwise the CWD could
// determine the default trusted config/state location.
func TestResolvePathsRejectsRelativeHomeFallback(t *testing.T) {
	p, err := ResolvePaths(Options{
		Getenv:  envFunc(nil), // XDG unset -> fallback to HOME
		HomeDir: func() (string, error) { return "relative", nil },
	})
	if err == nil {
		t.Fatalf("relative HOME with XDG unset must be rejected, got ConfigDir=%q StateDir=%q", p.ConfigDir, p.StateDir)
	}
	if filepath.IsAbs(p.ConfigDir) {
		// Defensive: on the error path ConfigDir should be empty, never a
		// resolved relative path masquerading as usable.
		t.Errorf("error path returned a populated ConfigDir %q", p.ConfigDir)
	}
	if strings.HasPrefix(p.ConfigDir, "relative") || strings.HasPrefix(p.StateDir, "relative") {
		t.Errorf("must not return a relative default path: ConfigDir=%q StateDir=%q", p.ConfigDir, p.StateDir)
	}
}

// TestExplicitOverridesBypassInvalidXDG proves the documented precedence: when
// both --config and --state-dir are given, no XDG base is consulted, so a
// relative/malformed (but unused) XDG variable — and even a relative HOME — must
// not fail the fully explicit invocation.
func TestExplicitOverridesBypassInvalidXDG(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	state := filepath.Join(t.TempDir(), "state")
	p, err := ResolvePaths(Options{
		ConfigPath: cfg,
		StateDir:   state,
		Getenv: envFunc(map[string]string{
			"XDG_CONFIG_HOME": "relative",
			"XDG_STATE_HOME":  "relative",
		}),
		HomeDir: func() (string, error) { return "relative", nil },
	})
	if err != nil {
		t.Fatalf("fully explicit overrides must not consult XDG, got %v", err)
	}
	if p.ConfigFile != cfg {
		t.Errorf("ConfigFile = %q, want %q", p.ConfigFile, cfg)
	}
	if p.StateDir != state {
		t.Errorf("StateDir = %q, want %q", p.StateDir, state)
	}
}

// TestOnlyConfigOverrideStillValidatesStateXDG proves overrides are resolved
// independently: a --config override does not excuse an invalid XDG_STATE_HOME
// (the non-overridden base is still required and validated).
func TestOnlyConfigOverrideStillValidatesStateXDG(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	_, err := ResolvePaths(Options{
		ConfigPath: cfg,
		Getenv:     envFunc(map[string]string{"XDG_STATE_HOME": "relative"}),
		HomeDir:    func() (string, error) { return "relative", nil },
	})
	if err == nil {
		t.Fatalf("invalid state XDG must still fail when only --config is overridden")
	}
}

// TestOnlyStateOverrideStillValidatesConfigXDG is the mirror: a --state-dir
// override does not excuse an invalid XDG_CONFIG_HOME.
func TestOnlyStateOverrideStillValidatesConfigXDG(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	_, err := ResolvePaths(Options{
		StateDir: state,
		Getenv:   envFunc(map[string]string{"XDG_CONFIG_HOME": "relative"}),
		HomeDir:  func() (string, error) { return "relative", nil },
	})
	if err == nil {
		t.Fatalf("invalid config XDG must still fail when only --state-dir is overridden")
	}
}

// TestOneOverrideWithValidNonOverriddenXDG proves the mixed case succeeds when
// the still-required base is valid: --state-dir explicit, config from a valid
// XDG_CONFIG_HOME.
func TestOneOverrideWithValidNonOverriddenXDG(t *testing.T) {
	cfgHome := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	p, err := ResolvePaths(Options{
		StateDir: state,
		Getenv:   envFunc(map[string]string{"XDG_CONFIG_HOME": cfgHome}),
		HomeDir:  func() (string, error) { return "relative", nil }, // unused: XDG set
	})
	if err != nil {
		t.Fatalf("mixed override with valid config XDG must succeed, got %v", err)
	}
	if want := filepath.Join(cfgHome, appDir); p.ConfigDir != want {
		t.Errorf("ConfigDir = %q, want %q", p.ConfigDir, want)
	}
	if p.StateDir != state {
		t.Errorf("StateDir = %q, want %q", p.StateDir, state)
	}
}

func TestVersionLockPathIsContainedInConfigDir(t *testing.T) {
	home := t.TempDir()
	p, err := ResolvePaths(Options{Getenv: envFunc(nil), HomeDir: homeFunc(home)})
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	vlp, err := VersionLockPath(p)
	if err != nil {
		t.Fatalf("VersionLockPath: %v", err)
	}
	want := filepath.Join(p.ConfigDir, versionLockFileName)
	if vlp != want {
		t.Errorf("VersionLockPath = %q, want %q", vlp, want)
	}
}

func TestContainedJoinRejectsTraversal(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{
		"../escape.json",
		"sub/child.json",
		"..",
		"",
		filepath.Join("a", "..", "..", "b"),
	} {
		if _, err := containedJoin(base, name); err == nil {
			t.Errorf("containedJoin(%q) = nil error, want rejection", name)
		}
	}
}

func TestContainedJoinAcceptsPlainName(t *testing.T) {
	base := t.TempDir()
	got, err := containedJoin(base, "config.json")
	if err != nil {
		t.Fatalf("containedJoin: %v", err)
	}
	if got != filepath.Join(base, "config.json") {
		t.Errorf("containedJoin = %q", got)
	}
	if !strings.HasPrefix(got, base) {
		t.Errorf("result %q not under base %q", got, base)
	}
}

// TestResolvePathsDefaultsUsingRealEnvHelpersCompile guards that the zero-value
// Options (nil Getenv/HomeDir) falls back to the real os helpers rather than
// panicking on a nil func. It only asserts no panic and an absolute config dir.
func TestResolvePathsDefaultsUsingRealEnvHelpers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("XDG fallbacks target Unix hosts")
	}
	p, err := ResolvePaths(Options{})
	if err != nil {
		t.Skipf("no HOME in test env: %v", err)
	}
	if !filepath.IsAbs(p.ConfigDir) {
		t.Errorf("ConfigDir %q not absolute", p.ConfigDir)
	}
}
