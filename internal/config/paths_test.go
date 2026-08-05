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

// The operator selects the managed instance (ADR-0001). Unset must stay exactly
// the pre-ADR behaviour, because every existing setup depends on it.
func TestResolveInstanceDefaultsToTorio(t *testing.T) {
	got, err := ResolveInstance(Options{Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatalf("ResolveInstance() error = %v", err)
	}
	if got != DefaultInstance {
		t.Fatalf("instance = %q, want %q", got, DefaultInstance)
	}
}

func TestResolveInstanceAcceptsASlug(t *testing.T) {
	for _, name := range []string{"torio-test", "t", "a1", "torio2", "x-y-z"} {
		got, err := ResolveInstance(Options{Getenv: envOnly(InstanceEnvKey, name)})
		if err != nil {
			t.Errorf("ResolveInstance(%q) error = %v", name, err)
			continue
		}
		if got != name {
			t.Errorf("instance = %q, want %q", got, name)
		}
	}
}

// A malformed value must not fall back to the default. Falling back would send
// a command meant for a test VM to the operator's daily one — the exact failure
// the mechanism exists to prevent. The value reaches a limactl argv element and
// a config path segment, so traversal, flags and separators all fail closed.
func TestResolveInstanceRejectsAnythingUnsafe(t *testing.T) {
	for _, name := range []string{
		"Torio",       // uppercase: Lima names are not case-folded here
		"-torio",      // could be read as a flag
		"torio-",      // trailing dash
		"tor io",      // whitespace
		"../etc",      // traversal
		"torio/test",  // separator: would escape the instance config dir
		"torio\\test", // separator on the other slash
		"torio;rm",    // shell syntax, even though argv is never a shell string
		".",           // current directory
		"..",          // parent directory
		"torio\ntest", // newline
		strings.Repeat("a", 65),
	} {
		got, err := ResolveInstance(Options{Getenv: envOnly(InstanceEnvKey, name)})
		if err == nil {
			t.Errorf("ResolveInstance(%q) = %q, want an error", name, got)
		}
		if got != "" {
			t.Errorf("ResolveInstance(%q) returned %q alongside an error; it must not fall back", name, got)
		}
	}
}

// The error names the rule, never the value: an environment variable is
// arbitrary operator input and this package does not echo such things.
func TestResolveInstanceErrorDoesNotEchoTheValue(t *testing.T) {
	const bad = "NOT-a-valid-instance-NAME"
	_, err := ResolveInstance(Options{Getenv: envOnly(InstanceEnvKey, bad)})
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), bad) {
		t.Errorf("error echoed the value: %v", err)
	}
	if !strings.Contains(err.Error(), InstanceEnvKey) {
		t.Errorf("error does not name the variable: %v", err)
	}
}

// A named instance gets its own registry, derived rather than remembered.
// Sharing one would let `project list` show the daily projects while talking to
// a test VM.
func TestNamedInstanceGetsItsOwnConfigDir(t *testing.T) {
	base := t.TempDir()

	def, err := ResolvePaths(Options{Getenv: envOnly("XDG_CONFIG_HOME", base)})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	named, err := ResolvePaths(Options{Getenv: func(k string) string {
		switch k {
		case "XDG_CONFIG_HOME":
			return base
		case InstanceEnvKey:
			return "torio-test"
		}
		return ""
	}})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}

	if def.ConfigFile == named.ConfigFile {
		t.Fatalf("both instances resolved to the same config file %q", def.ConfigFile)
	}
	if def.Instance != DefaultInstance || named.Instance != "torio-test" {
		t.Fatalf("instances = %q / %q", def.Instance, named.Instance)
	}
	// The instance directory stays inside the already-trusted config dir, so
	// ADR-0001's path rules cover it without a second boundary.
	if !strings.HasPrefix(named.ConfigDir, filepath.Join(base, appDir)+string(filepath.Separator)) {
		t.Errorf("named instance config dir %q escaped the trusted config dir", named.ConfigDir)
	}
}

// --config is an explicit trusted input and keeps winning, whatever instance is
// selected. The instance is still resolved, because internal/lima needs it.
func TestExplicitConfigWinsOverTheInstance(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "elsewhere.json")
	p, err := ResolvePaths(Options{
		ConfigPath: explicit,
		Getenv:     envOnly(InstanceEnvKey, "torio-test"),
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	if p.ConfigFile != explicit {
		t.Errorf("config file = %q, want the explicit path %q", p.ConfigFile, explicit)
	}
	if p.Instance != "torio-test" {
		t.Errorf("instance = %q, want it resolved even with --config", p.Instance)
	}
}

func envOnly(key, value string) func(string) string {
	return func(k string) string {
		if k == key {
			return value
		}
		return ""
	}
}
