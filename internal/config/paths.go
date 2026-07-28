package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// appDir is the per-application subdirectory under the XDG base dirs. All
	// Torio config and state live under it so nothing pollutes the base.
	appDir = "torio"
	// configFileName is the default config document within the config dir.
	configFileName = "config.json"
)

// errNoHome is returned when the home directory is required (XDG unset) but
// cannot be determined. Callers fail closed rather than guessing a location.
var errNoHome = errors.New("config: cannot determine home directory and XDG base is unset")

// Options are the raw inputs to path/config resolution: the explicit CLI
// overrides plus injectable environment/home accessors (nil uses the real OS
// helpers). Keeping them injectable makes resolution fully testable without
// mutating process-global state.
type Options struct {
	// ConfigPath is the explicit --config value ("" if not given).
	ConfigPath string
	// StateDir is the explicit --state-dir value ("" if not given).
	StateDir string
	// Getenv reads environment variables. If nil, os.Getenv is used.
	Getenv func(string) string
	// HomeDir returns the user home directory. If nil, os.UserHomeDir is used.
	HomeDir func() (string, error)
}

func (o Options) getenv(k string) string {
	if o.Getenv != nil {
		return o.Getenv(k)
	}
	return os.Getenv(k)
}

func (o Options) homeDir() (string, error) {
	if o.HomeDir != nil {
		return o.HomeDir()
	}
	return os.UserHomeDir()
}

// Paths are resolved, canonical config/state locations. ConfigDir and StateDir
// are the trusted directories that hold, respectively, operator-authored config
// and runtime state written by later slices.
type Paths struct {
	// ConfigDir is the trusted directory holding the config document.
	ConfigDir string
	// ConfigFile is the resolved config document path. It defaults to
	// ConfigDir/config.json but is the canonical explicit path when --config
	// was given. It may not exist (absent default config is a valid first run).
	ConfigFile string
	// StateDir is the trusted runtime state directory.
	StateDir string
	// explicitConfig records whether ConfigFile came from --config. An explicit
	// path that does not exist is an error; an absent default is a valid first
	// run (see Load).
	explicitConfig bool
}

// ResolvePaths computes canonical Paths from CLI overrides and the environment.
// It performs no filesystem reads or writes; it only derives and canonicalizes
// locations.
//
// Each location is resolved independently and lazily: an explicit --config or
// --state-dir override is a trusted direct input and bypasses XDG entirely, so
// an unused (even malformed) XDG base or HOME never gates a fully explicit
// invocation. Only a base that is actually needed — because its override is
// absent — is consulted, in which case XDG_CONFIG_HOME / XDG_STATE_HOME take
// precedence and fall back to the XDG-specified $HOME/.config and
// $HOME/.local/state. Every value that is actually used is still validated:
// a set-but-non-absolute XDG base (or non-absolute HOME fallback) is rejected
// fail-closed rather than silently ignored or coerced against CWD.
func ResolvePaths(opts Options) (Paths, error) {
	var p Paths

	// State directory: explicit override bypasses XDG.
	if opts.StateDir != "" {
		abs, err := canonical(opts.StateDir)
		if err != nil {
			return Paths{}, fmt.Errorf("resolve --state-dir: %w", err)
		}
		p.StateDir = abs
	} else {
		stateHome, err := opts.xdgBase("XDG_STATE_HOME", filepath.Join(".local", "state"))
		if err != nil {
			return Paths{}, err
		}
		p.StateDir = filepath.Join(stateHome, appDir)
	}

	// Config file (and its trusted directory): explicit override bypasses XDG.
	// With an explicit --config, the trusted config directory is the file's
	// parent, so anything contained in the config dir resolves alongside it.
	if opts.ConfigPath != "" {
		abs, err := canonical(opts.ConfigPath)
		if err != nil {
			return Paths{}, fmt.Errorf("resolve --config: %w", err)
		}
		p.ConfigFile = abs
		p.ConfigDir = filepath.Dir(abs)
		p.explicitConfig = true
	} else {
		configHome, err := opts.xdgBase("XDG_CONFIG_HOME", ".config")
		if err != nil {
			return Paths{}, err
		}
		p.ConfigDir = filepath.Join(configHome, appDir)
		cf, err := containedJoin(p.ConfigDir, configFileName)
		if err != nil {
			return Paths{}, err
		}
		p.ConfigFile = cf
	}

	return p, nil
}

// xdgBase resolves an XDG base directory: the env var if set (which must be
// absolute), otherwise $HOME joined with the spec fallback.
func (o Options) xdgBase(envKey, fallbackRel string) (string, error) {
	if v := strings.TrimSpace(o.getenv(envKey)); v != "" {
		if !filepath.IsAbs(v) {
			return "", fmt.Errorf("config: %s must be an absolute path, got %q", envKey, v)
		}
		return filepath.Clean(v), nil
	}
	home, err := o.homeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", errNoHome
	}
	// The home fallback must be absolute for the same fail-closed reason as the
	// XDG bases: a relative home would let the working directory determine the
	// default trusted config/state location. Reject it rather than canonicalize
	// it against CWD.
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("config: home directory must be an absolute path, got %q", home)
	}
	return filepath.Join(home, fallbackRel), nil
}

// canonical makes p absolute (relative to the current working directory) and
// lexically clean. It does not resolve symlinks: the target need not exist, and
// explicit operator overrides are trusted inputs whose canonical form is the
// cleaned absolute path.
func canonical(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("empty path")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// containedJoin joins a single trusted directory with a plain file name and
// guarantees the result stays within base. name must be a bare file name: it
// may not be empty, contain a path separator, or be "..". This is the intended
// containment policy for files the config layer locates inside a trusted
// directory — it defends against traversal structurally, not by string
// cleanup, so a name like "../x" is rejected rather than normalized away.
func containedJoin(base, name string) (string, error) {
	if name == "" || name == "." || name == ".." ||
		strings.ContainsRune(name, filepath.Separator) ||
		strings.ContainsRune(name, '/') {
		return "", fmt.Errorf("config: %q is not a valid contained file name", name)
	}
	joined := filepath.Join(base, name)
	rel, err := filepath.Rel(base, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("config: %q escapes the trusted directory", name)
	}
	return joined, nil
}
