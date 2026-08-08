package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
)

// The pinned install. Claude Code ships one self-contained binary per platform,
// published with a per-version manifest carrying a SHA-256 for each — so unlike
// the Hermes path, which runs a vendor script whose *content* is not pinned,
// this install has a checksum to verify and verifies it.
const (
	// PromotedVersion is the version a Torio guest runs. It is a constant, not
	// a lookup: "latest" is not a pin, and a box whose agent version can change
	// underneath it cannot be reasoned about after the fact.
	PromotedVersion = "2.1.220"

	// downloadBaseURL is the vendor's own release root, the one its installer
	// uses. Both the manifest and the binary are fetched from below it.
	downloadBaseURL = "https://downloads.claude.ai/claude-code-releases"

	// installDir holds the versioned binaries. It is root-owned, and so is what
	// it contains: this is where this backend diverges from the Hermes shim,
	// which points a root path at a file the agent's own uid can rewrite.
	installDir = "/usr/local/lib/torio/claude-code"
	// commandPath is the stable name on sudo's secure_path. It is a symlink,
	// root-owned, to a root-owned target.
	commandPath = "/usr/local/bin/claude"
	// stagingPath is where a download lands before it is verified. It is under
	// the root-owned install directory so an unverified binary is never
	// reachable by the identity that will later run the verified one.
	stagingPath = installDir + "/.claude.staging"

	// versionMarker is what `claude --version` prints alongside the version. A
	// clean exit is not proof; output that does not identify the program is not
	// proof either.
	versionMarker = "(Claude Code)"
)

// targetPath is the versioned binary this pin resolves to.
func targetPath() string { return installDir + "/claude-" + PromotedVersion }

// Install reconciles the pinned binary and the stable command path.
//
// It is idempotent in the way that matters for a box an operator reruns
// bootstrap on: when the command path already resolves to the pinned, verified
// target, nothing is downloaded. A drifted install is reported, never silently
// replaced — except for the one case where there is nothing to drift from,
// which is a target that does not exist yet.
func (claudeBackend) Install(ctx context.Context, r backend.StepRunner) error {
	const name = "claude_install"
	target := targetPath()

	present, err := r.Probe(ctx, name, "sudo", "-n", "test", "-x", target)
	if err != nil {
		return err
	}
	if present.ExitCode != 0 {
		if err := download(ctx, r, name, target); err != nil {
			return err
		}
	}

	if err := verifyTarget(ctx, r, name, target); err != nil {
		return err
	}
	return reconcileCommandPath(ctx, r, name, target)
}

// download fetches the manifest, verifies the published checksum against the
// downloaded bytes, and installs the result root-owned.
//
// The manifest is fetched by the guest and parsed here: the network stays on
// the guest side of the boundary, as it does for every other install, and the
// host does the strict decoding it would have to do anyway before trusting a
// checksum from it.
func download(ctx context.Context, r backend.StepRunner, name, target string) error {
	platform, err := guestPlatform(ctx, r, name)
	if err != nil {
		return err
	}

	man, err := r.Probe(ctx, name, "sudo", "-n", "--",
		"curl", "-fsSL", downloadBaseURL+"/"+PromotedVersion+"/manifest.json")
	if err != nil {
		return err
	}
	if man.ExitCode != 0 {
		return r.Fail(name, "could not fetch the release manifest", "check guest network and curl, then re-run bootstrap")
	}
	checksum, err := manifestChecksum(man.Stdout, platform)
	if err != nil {
		return r.Fail(name, err.Error(), "the pinned version must publish a checksum for this platform")
	}

	dl, err := r.Probe(ctx, name, "sudo", "-n", "--",
		"curl", "-fsSL", "-o", stagingPath, downloadBaseURL+"/"+PromotedVersion+"/"+platform+"/claude")
	if err != nil {
		return err
	}
	if dl.ExitCode != 0 {
		return r.Fail(name, "could not download the pinned claude binary", "check guest network and curl, then re-run bootstrap")
	}

	// The expected digest travels as stdin, never as an argv element: it is
	// derived from a document the guest fetched, and the check that decides
	// whether we execute those bytes should not also be a place they are
	// interpolated into a command line.
	sum, err := r.ProbeInput(ctx, name, []byte(checksum+"  "+stagingPath+"\n"),
		[]string{"sudo", "-n", "--", "sha256sum", "--strict", "--check", "-"})
	if err != nil {
		return err
	}
	if sum.ExitCode != 0 {
		// Remove the unverified bytes before returning, so a failed run cannot
		// leave something executable-shaped behind for a later one to adopt.
		_, _ = r.Probe(ctx, name, "sudo", "-n", "rm", "-f", stagingPath)
		return r.Fail(name, "the downloaded claude binary does not match the published checksum",
			"do not use it; re-run bootstrap, and if it recurs treat the download path as untrusted")
	}

	inst, err := r.Probe(ctx, name, "sudo", "-n", "install", "-o", "root", "-g", "root", "-m", "0755", stagingPath, target)
	if err != nil {
		return err
	}
	if inst.ExitCode != 0 {
		return r.Fail(name, "could not install the verified binary", "check write access to "+installDir)
	}
	if rm, err := r.Probe(ctx, name, "sudo", "-n", "rm", "-f", stagingPath); err != nil {
		return err
	} else if rm.ExitCode != 0 {
		return r.Fail(name, "could not remove the download staging file", "remove "+stagingPath+" on the guest")
	}
	return nil
}

// verifyTarget proves the pinned binary is one the agent cannot rewrite. This
// is the property the whole install exists to establish: the command an
// operator may later run under sudo must not be a file the agent owns.
func verifyTarget(ctx context.Context, r backend.StepRunner, name, target string) error {
	kind, err := r.Probe(ctx, name, "sudo", "-n", "stat", "-c", "%F", target)
	if err != nil {
		return err
	}
	if kind.ExitCode != 0 || trimmed(kind.Stdout) != "regular file" {
		return r.Fail(name, "the pinned binary is not a regular file",
			"a symlink here would move the real bytes somewhere unowned; re-run bootstrap")
	}
	og, err := r.Probe(ctx, name, "sudo", "-n", "stat", "-c", "%U:%G %a", target)
	if err != nil {
		return err
	}
	owner, group, mode, ok := parseOwnershipMode(og.Stdout)
	if og.ExitCode != 0 || !ok {
		return r.Fail(name, "could not read the pinned binary's ownership/mode", "verify "+target+" on the guest")
	}
	if owner != "root" || group != "root" {
		return r.Fail(name, fmt.Sprintf("pinned binary is owned by %s:%s, want root:root", owner, group),
			"a binary the agent owns is a binary the agent can replace before the operator runs it")
	}
	if writable, parsed := modeGrantsForeignWrite(mode); !parsed || writable {
		return r.Fail(name, "pinned binary mode "+mode+" is group- or world-writable",
			"reinstall it 0755 root:root; anything wider defeats the pin")
	}
	r.Record(name, true, "root:root "+mode+" "+PromotedVersion)
	return nil
}

// reconcileCommandPath points the stable name at the pinned target. A correct
// link is left alone; a wrong one is repointed, because the link is Torio's own
// and carries no content of its own to lose.
func reconcileCommandPath(ctx context.Context, r backend.StepRunner, name, target string) error {
	const link = "claude_command_path"
	cur, err := r.Probe(ctx, link, "readlink", commandPath)
	if err != nil {
		return err
	}
	if trimmed(cur.Stdout) == target {
		r.Record(link, true, "already correct")
		return nil
	}
	ln, err := r.Probe(ctx, link, "sudo", "-n", "ln", "-sfn", target, commandPath)
	if err != nil {
		return err
	}
	if ln.ExitCode != 0 {
		return r.Fail(link, "could not install the claude command path", "check write access to "+commandPath)
	}
	r.Record(link, true, "installed")
	return nil
}

// guestPlatform maps the guest's architecture onto a published platform name.
//
// The guest image is a pinned glibc Ubuntu, so the musl variants are not
// reachable from here; an unrecognized architecture fails closed rather than
// guessing, because the wrong binary would be caught by the checksum anyway and
// the error would then be unreadable.
func guestPlatform(ctx context.Context, r backend.StepRunner, name string) (string, error) {
	res, err := r.Probe(ctx, name, "uname", "-m")
	if err != nil {
		return "", err
	}
	switch arch := trimmed(res.Stdout); arch {
	case "aarch64":
		return "linux-arm64", nil
	case "x86_64":
		return "linux-x64", nil
	default:
		return "", r.Fail(name, "unsupported guest architecture "+arch,
			"Torio's guest is a pinned Linux image on arm64 or x86_64")
	}
}

// manifestChecksum reads the published SHA-256 for one platform out of the
// release manifest. The decode is strict and the document is small: anything
// unexpected is a manifest we do not understand, and a checksum we do not
// understand is worse than none.
func manifestChecksum(doc []byte, platform string) (string, error) {
	var m struct {
		Version   string `json:"version"`
		Platforms map[string]struct {
			Checksum string `json:"checksum"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(doc), &m); err != nil {
		return "", fmt.Errorf("the release manifest is not readable JSON")
	}
	if m.Version != PromotedVersion {
		return "", fmt.Errorf("the release manifest declares version %q, pinned %q", m.Version, PromotedVersion)
	}
	p, ok := m.Platforms[platform]
	if !ok || p.Checksum == "" {
		return "", fmt.Errorf("the release manifest publishes no checksum for platform %q", platform)
	}
	if !isHexDigest(p.Checksum) {
		return "", fmt.Errorf("the published checksum for %q is not a sha256 digest", platform)
	}
	return p.Checksum, nil
}

// VerifyVersion proves the documented stable command path answers, as the
// backend identity, with the pinned version.
//
// Equality, not containment: the pin exists so that what runs is knowable, and
// "the output mentioned the version somewhere" is a weaker claim than it looks.
func (claudeBackend) VerifyVersion(ctx context.Context, r backend.StepRunner) error {
	const name = versionCheck
	res, err := r.Probe(ctx, name, "sudo", "-n", "-u", User, "-H", "--", "claude", "--version")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return r.Fail(name, "`claude --version` exited non-zero", "confirm the command path and install on the guest")
	}
	version, ok := parseVersion(string(res.Stdout))
	if !ok {
		return r.Fail(name, "`claude --version` produced no recognizable version",
			"a clean exit is not proof; inspect the install on the guest")
	}
	if version != PromotedVersion {
		return r.Fail(name, fmt.Sprintf("claude version %q, pinned %q", version, PromotedVersion),
			"version drift: reconcile the pinned install, do not paper over it")
	}
	if pinned := r.PinnedVersion(); pinned != "" && version != pinned {
		return r.Fail(name, fmt.Sprintf("claude version %q, run pinned %q", version, pinned),
			"version drift against the requested pin")
	}
	r.Record(name, true, version)
	return nil
}

// parseVersion reads the version from `claude --version`, whose first line is
// "<semver> (Claude Code)". The marker is required: output that does not
// identify the program is not evidence about the program.
func parseVersion(out string) (string, bool) {
	line := strings.TrimSpace(firstLine(out))
	if !strings.Contains(line, versionMarker) {
		return "", false
	}
	version := strings.TrimSpace(strings.Split(line, " ")[0])
	if version == "" {
		return "", false
	}
	return version, true
}
