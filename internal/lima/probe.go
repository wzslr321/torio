package lima

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// versionPattern matches limactl's real `--version` output, verified against
// an installed Lima 2.2.0 on darwin/arm64: "limactl version 2.2.0\n"
// (archive/pre-v1:docs/spike-results/evidence/etap-0d-lima-adapter/limactl-version.txt).
//
// The captured group is a semver version — a "MAJOR.MINOR.PATCH" core with
// the standard optional pre-release ("-…") and build ("+…") metadata — not an
// arbitrary \S+ token. Lima is released under semver tags, and the probed
// value feeds an exact VersionLock.Lima comparison, so accepting non-version
// junk (e.g. "2", "v2.2.0") and handing it back as a "version" would be
// meaningless at best and a fail-open at worst. Anything off-grammar is
// treated as malformed output.
var versionPattern = regexp.MustCompile(`^limactl version ([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$`)

// ProbeResult is the outcome of a successful feature/version probe.
type ProbeResult struct {
	// Version is the parsed limactl version string (e.g. "2.2.0").
	Version string
	// Pinned reports whether a non-empty VersionLock.Lima pin was supplied
	// and matched. False when no pin was given.
	Pinned bool
}

// Probe detects limactl availability, reads its version, and — if pinned is
// non-empty — confirms it matches VersionLock.Lima. An empty pinned means
// unpinned: any parseable version is accepted. Probe fails closed on a
// missing binary, non-zero exit, unparseable output, or version mismatch; it
// never installs or updates anything.
func (a *Adapter) Probe(ctx context.Context, pinned string) (ProbeResult, error) {
	const op = "probe"

	res, err := a.runRaw(ctx, "--version")
	if err != nil {
		return ProbeResult{}, classifyRunErr(op, err)
	}
	if res.ExitCode != 0 {
		return ProbeResult{}, commandFailed(op, res.ExitCode, res.Stderr)
	}

	version, ok := parseVersion(string(res.Stdout))
	if !ok {
		return ProbeResult{}, &Error{Op: op, Kind: KindMalformedOutput, Err: fmt.Errorf("unrecognized `limactl --version` output")}
	}

	if pinned != "" && version != pinned {
		return ProbeResult{}, &Error{Op: op, Kind: KindVersionMismatch, Err: fmt.Errorf("version-lock pins lima %q, found %q", pinned, version)}
	}

	return ProbeResult{Version: version, Pinned: pinned != ""}, nil
}

func parseVersion(out string) (string, bool) {
	m := versionPattern.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return "", false
	}
	return m[1], true
}
