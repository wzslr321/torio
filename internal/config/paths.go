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
	// registryFileName is the project registry document. It sits in the config
	// root rather than in an instance's directory, because the registry is
	// shared by every instance under that root.
	registryFileName = "projects.json"
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

// InstancePrefix is what a derived instance name starts with. It is
// DefaultInstance plus a separator so every box Torio derives is recognizable
// as one in `limactl list`, alongside whatever else the operator runs.
const InstancePrefix = DefaultInstance + "-"

// ValidInstanceName reports whether name is a well-formed instance name.
//
// It exists for the one caller that meets instance names it did not derive:
// a status poll reads them out of `limactl list --json`, which is external
// tool output. A name from there reaches a `limactl` argv element and a
// rendered line, so it is held to the same slug rule as a name Torio derives
// rather than trusted for having been printed by a neighbouring process.
func ValidInstanceName(name string) bool {
	return instancePattern.MatchString(name)
}

// InstanceForBackend returns the instance that runs the named backend.
//
// The mapping is derived rather than recorded because the alternative — a table
// of instance names the operator maintains — makes the operator responsible for
// a fact Torio can compute, and gives two places to disagree about which box
// runs which agent.
//
// Every backend derives its own name. Bare DefaultInstance is deliberately not
// among them: it was the instance of the backend that has since been removed,
// and re-pointing it at a live agent would hand a box provisioned for one
// identity to another. It stays reachable only through InstanceEnvKey, where
// naming a box directly is the operator's own decision.
//
// The caller resolves the backend name; this function only shapes it.
func InstanceForBackend(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("no backend name to derive an instance from")
	}
	derived := InstancePrefix + name
	if !instancePattern.MatchString(derived) {
		return "", fmt.Errorf("backend %q does not derive a valid instance name", name)
	}
	return derived, nil
}

// ResolveInstance returns the managed instance name for this invocation.
//
// The environment wins over everything. TORIO_INSTANCE names a box directly and
// is the only way to reach one whose name Torio did not derive — a test VM, a
// second box running the same backend — so a flag must not be able to redirect
// an invocation that already named its target. When it is unset, an explicit
// Instance (which the CLI derives from --backend) is used, and with neither the
// answer is DefaultInstance, which is the pre-ADR-0001 behaviour exactly.
//
// A set-but-malformed value is an error rather than a silent fall back to the
// default: falling back would send a command meant for a test VM to the
// operator's daily one, which is the failure this whole mechanism exists to
// prevent. The error states the rule and never echoes the value.
func ResolveInstance(opts Options) (string, error) {
	raw := strings.TrimSpace(opts.getenv(InstanceEnvKey))
	if raw == "" {
		if derived := strings.TrimSpace(opts.Instance); derived != "" {
			if !instancePattern.MatchString(derived) {
				return "", errors.New("derived instance name is not a valid instance name")
			}
			return derived, nil
		}
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
	// Instance is the instance this invocation derived for itself, empty when
	// it derived none. It is consulted only when TORIO_INSTANCE is unset (see
	// ResolveInstance), and every construction of Options within one invocation
	// must carry the same value — the registry resolves its own paths, and one
	// that disagreed about the instance would read a different document than
	// the command it serves.
	Instance string
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
	// RegistryFile is the project registry document, which is shared by every
	// instance under the same config root. It is deliberately not inside
	// ConfigDir: a project is something the operator attached, not something an
	// instance owns, so switching which box a command talks to must not switch
	// which projects exist (ADR-0009 revisited).
	//
	// It may not exist. A registry that has never been written is read from
	// RootConfigFile's legacy `projects` array instead (see ResolveRegistry).
	RegistryFile string
	// ConfigFile is the resolved config document path. It defaults to
	// ConfigDir/config.json but is the canonical explicit path when --config
	// was given. It may not exist (absent default config is a valid first run).
	ConfigFile string
	// RootConfigFile is the default instance's config document — the same as
	// ConfigFile whenever that instance is the one selected. It is resolved
	// because it is where the registry lived before it was shared, and an
	// installation that has not migrated yet keeps its projects there whichever
	// instance a command selects.
	RootConfigFile string
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
	// Resolved first: a malformed instance name must stop the invocation before
	// any location is derived from it.
	instance, err := ResolveInstance(opts)
	if err != nil {
		return Paths{}, err
	}
	return resolvePathsFor(instance, opts)
}

// ResolveInstancePaths computes the Paths of a named instance's own documents.
//
// It ignores both TORIO_INSTANCE and an explicit --config, and that is the
// whole reason it exists. A status poll asks about a specific box, so the
// document that answers is the one that box owns: resolving through the
// invocation's own selection would read one instance's document while reporting
// on another's, and an explicit --config would answer for every box with the
// same file. Everything else about resolution — the XDG base, the trusted
// directory, the contained joins — is unchanged.
func ResolveInstancePaths(instance string, opts Options) (Paths, error) {
	if !ValidInstanceName(instance) {
		return Paths{}, errors.New("instance name is not a valid instance name")
	}
	opts.ConfigPath = ""
	return resolvePathsFor(instance, opts)
}

// resolvePathsFor derives every location from an instance name that has already
// been resolved and validated.
func resolvePathsFor(instance string, opts Options) (Paths, error) {
	var p Paths
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
		p.RootConfigFile = abs
		p.explicitConfig = true
		rf, err := containedJoin(p.ConfigDir, registryFileName)
		if err != nil {
			return Paths{}, err
		}
		p.RegistryFile = rf
	} else {
		configHome, err := opts.xdgBase("XDG_CONFIG_HOME", ".config")
		if err != nil {
			return Paths{}, err
		}
		p.ConfigDir = filepath.Join(configHome, appDir)
		// The registry is resolved from the root, before the instance can move
		// ConfigDir underneath it. Every instance under this root reads and
		// writes the same document.
		rf, err := containedJoin(p.ConfigDir, registryFileName)
		if err != nil {
			return Paths{}, err
		}
		p.RegistryFile = rf
		root, err := containedJoin(p.ConfigDir, configFileName)
		if err != nil {
			return Paths{}, err
		}
		p.RootConfigFile = root
		// A named instance gets its own document. What it holds is what the
		// instance owns and nothing else: which backend the box was
		// provisioned for, and the settings a command against it runs under.
		// The separation is derived, never something to remember (ADR-0001).
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
