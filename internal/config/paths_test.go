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
}

func TestResolvePathsHonorsXDGOverrides(t *testing.T) {
	cfgHome := t.TempDir()
	p, err := ResolvePaths(Options{
		Getenv:  envFunc(map[string]string{"XDG_CONFIG_HOME": cfgHome}),
		HomeDir: homeFunc("/nonexistent-should-not-be-used"),
	})
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if want := filepath.Join(cfgHome, appDir); p.ConfigDir != want {
		t.Errorf("ConfigDir = %q, want %q", p.ConfigDir, want)
	}
}

func TestResolvePathsRejectsRelativeXDG(t *testing.T) {
	_, err := ResolvePaths(Options{
		Getenv:  envFunc(map[string]string{"XDG_CONFIG_HOME": "relative/path"}),
		HomeDir: homeFunc(t.TempDir()),
	})
	if err == nil {
		t.Errorf("relative XDG_CONFIG_HOME: want error, got nil")
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
// determine the default trusted config location.
func TestResolvePathsRejectsRelativeHomeFallback(t *testing.T) {
	p, err := ResolvePaths(Options{
		Getenv:  envFunc(nil), // XDG unset -> fallback to HOME
		HomeDir: func() (string, error) { return "relative", nil },
	})
	if err == nil {
		t.Fatalf("relative HOME with XDG unset must be rejected, got ConfigDir=%q", p.ConfigDir)
	}
	if filepath.IsAbs(p.ConfigDir) {
		// Defensive: on the error path ConfigDir should be empty, never a
		// resolved relative path masquerading as usable.
		t.Errorf("error path returned a populated ConfigDir %q", p.ConfigDir)
	}
	if strings.HasPrefix(p.ConfigDir, "relative") {
		t.Errorf("must not return a relative default path: ConfigDir=%q", p.ConfigDir)
	}
}

// TestExplicitConfigBypassesInvalidXDG proves the documented precedence: an
// explicit --config consults no XDG base at all, so a relative/malformed XDG
// variable — and even a relative HOME — must not fail the invocation.
func TestExplicitConfigBypassesInvalidXDG(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.json")
	p, err := ResolvePaths(Options{
		ConfigPath: cfg,
		Getenv:     envFunc(map[string]string{"XDG_CONFIG_HOME": "relative"}),
		HomeDir:    func() (string, error) { return "relative", nil },
	})
	if err != nil {
		t.Fatalf("an explicit --config must not consult XDG, got %v", err)
	}
	if p.ConfigFile != cfg {
		t.Errorf("ConfigFile = %q, want %q", p.ConfigFile, cfg)
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
