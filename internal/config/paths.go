package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// appDir is the per-application subdirectory under the XDG base dirs. All
	// Torio config and state live under it so nothing pollutes the base.
	appDir = "torio"
	// configFileName is the default config document within the config dir.
	configFileName = "config.json"
	// instancesDir groups the config of every non-default instance. It sits
	// inside the trusted config dir, so ADR-0001's path rules cover it without
	// a second boundary to reason about.
	instancesDir = "instances"

	// DefaultInstance is the Lima instance Torio manages when the operator
	// selects none. It is also the only place the literal is allowed to appear
	// in production code (ADR-0001).
	DefaultInstance = "torio"
	// InstanceEnvKey selects the managed instance. It is not a credential: it
	// picks which VM this invocation talks to, and is available to anyone who
	// can already run `torio` with arbitrary arguments.
	InstanceEnvKey = "TORIO_INSTANCE"
)

// instancePattern is the project-ID rule reused deliberately. An instance name
// reaches both a `limactl` argv element and a config path segment, so the
// conservative slug that already guards project identifiers is exactly the
// right shape: no separators, no leading or trailing dash, nothing that could
// traverse a path or be read as a flag.
var instancePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// ResolveInstance returns the managed instance name for this invocation.
//
// Unset gives DefaultInstance, which is the pre-ADR-0001 behaviour exactly. A
// set-but-malformed value is an error rather than a silent fall back to the
// default: falling back would send a command meant for a test VM to the
// operator's daily one, which is the failure this whole mechanism exists to
// prevent. The error states the rule and never echoes the value.
func ResolveInstance(opts Options) (string, error) {
	raw := strings.TrimSpace(opts.getenv(InstanceEnvKey))
	if raw == "" {
		return DefaultInstance, nil
	}
	if !instancePattern.MatchString(raw) {
		return "", fmt.Errorf(
			"%s must be 1-64 characters of lowercase letters, digits and dashes, starting and ending alphanumeric",
			InstanceEnvKey)
	}
	return raw, nil
}

// errNoHome is returned when the home directory is required (XDG unset) but
// cannot be determined. Callers fail closed rather than guessing a location.
var errNoHome = errors.New("config: cannot determine home directory and XDG base is unset")

// Options are the raw inputs to path/config resolution: the explicit CLI
// override plus injectable environment/home accessors (nil uses the real OS
// helpers). Keeping them injectable makes resolution fully testable without
// mutating process-global state.
type Options struct {
	// ConfigPath is the explicit --config value ("" if not given).
	ConfigPath string
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

// Paths are resolved, canonical config locations. ConfigDir is the trusted
// directory that holds operator-authored config.
//
// There is no state directory: Torio writes no runtime state on the host. The
// one that existed served the version-lock manifest, which was never wired and
// is gone (ADR-0001).
type Paths struct {
	// Instance is the managed Lima instance this invocation targets. It is
	// resolved here because this is where trusted inputs are resolved, and
	// because the config location depends on it.
	Instance string
	// ConfigDir is the trusted directory holding the config document.
	ConfigDir string
	// ConfigFile is the resolved config document path. It defaults to
	// ConfigDir/config.json but is the canonical explicit path when --config
	// was given. It may not exist (absent default config is a valid first run).
	ConfigFile string
	// explicitConfig records whether ConfigFile came from --config. An explicit
	// path that does not exist is an error; an absent default is a valid first
	// run (see Load).
	explicitConfig bool
}

// ResolvePaths computes canonical Paths from the CLI override and the
// environment. It performs no filesystem reads or writes; it only derives and
// canonicalizes locations.
//
// An explicit --config override is a trusted direct input and bypasses XDG
// entirely, so a malformed XDG base or HOME never gates a fully explicit
// invocation. XDG_CONFIG_HOME is consulted only when the override is absent,
// falling back to the XDG-specified $HOME/.config. A value that is actually
// used is still validated: a set-but-non-absolute XDG base (or non-absolute
// HOME fallback) is rejected fail-closed rather than silently ignored or
// coerced against CWD.
func ResolvePaths(opts Options) (Paths, error) {
	var p Paths

	// Resolved first: a malformed instance name must stop the invocation before
	// any location is derived from it.
	instance, err := ResolveInstance(opts)
	if err != nil {
		return Paths{}, err
	}
	p.Instance = instance

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
		// A named instance gets its own registry. Sharing one would let
		// `project list` show the daily projects while talking to a test VM,
		// and `project add` write a test project into the real registry — so
		// the separation is derived, never something to remember (ADR-0001).
		if instance != DefaultInstance {
			// One contained segment at a time: containedJoin validates a single
			// file name, which is exactly the guarantee wanted here — the
			// instance name must not be able to introduce a separator even if
			// the pattern above were ever loosened.
			group, err := containedJoin(p.ConfigDir, instancesDir)
			if err != nil {
				return Paths{}, err
			}
			dir, err := containedJoin(group, instance)
			if err != nil {
				return Paths{}, err
			}
			p.ConfigDir = dir
		}
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
