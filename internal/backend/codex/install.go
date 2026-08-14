package codex

import (
	"context"
	"fmt"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
)

// The pinned install.
//
// Codex publishes one static archive per target triple, and publishes checksums
// only for a different set of artifacts (ADR-0022 P1). So the digest is not
// fetched, it is committed here: an origin that can serve a replaced archive can
// serve a matching checksum alongside it, and a value read once in review cannot
// be rewritten afterwards. The cost is that a version bump has to change two
// things, and one that changes only the version fails closed at the checksum.
const (
	// PromotedVersion is the version a Torio guest runs. It is a constant, not
	// a lookup: "latest" is not a pin, and a box whose agent version can change
	// underneath it cannot be reasoned about after the fact.
	PromotedVersion = "0.147.0"

	// releaseTagPrefix is what the vendor prefixes its release tags with. The
	// version is the part that moves.
	releaseTagPrefix = "rust-v"

	// downloadBaseURL is the release host. Only the archive comes from it; the
	// digest it is checked against does not.
	downloadBaseURL = "https://github.com/openai/codex/releases/download"

	// installDir holds the versioned binaries, root-owned, and so is what it
	// contains.
	installDir = "/usr/local/lib/torio/codex"
	// commandPath is the stable name on sudo's secure_path. It is a symlink,
	// root-owned, to a root-owned target.
	commandPath = "/usr/local/bin/codex"
	// stagingPath is where the archive lands before it is verified, under the
	// root-owned install directory so unverified bytes are never reachable by
	// the identity that will later run the verified ones.
	stagingPath = installDir + "/.codex.staging.tar.gz"
	// extractDir is where the verified archive is unpacked. It is 0700 root, and
	// it is removed before use rather than trusted to be empty.
	extractDir = installDir + "/.codex.staging.d"

	// versionMarker is what `codex --version` prints ahead of the version. It
	// leads, where the other backend's marker trails, so the parsers cannot be
	// shared without teaching each to accept the other's output.
	versionMarker = "codex-cli"
)

// pinnedDigests is the SHA-256 of each published archive at PromotedVersion.
//
// Recorded on 2026-08-14 from release rust-v0.147.0, computed locally over the
// downloaded archives and matching the digest GitHub reports for each asset.
// Changing PromotedVersion without changing these makes the install fail at the
// checksum, which is the intended failure.
var pinnedDigests = map[string]string{
	"x86_64-unknown-linux-musl":  "0246e2e773834e07f0fb5249ed6ebad12e4591e608f8c7bb97dd6a9690544c36",
	"aarch64-unknown-linux-musl": "eb677c80f666b1ab8b4b1d083b66e8d614b1281d960bb6f9fd8ca98f58b38b90",
}

// targetPath is the versioned binary this pin resolves to.
func targetPath() string { return installDir + "/codex-" + PromotedVersion }

// archiveURL is where one triple's archive is published.
func archiveURL(triple string) string {
	return downloadBaseURL + "/" + releaseTagPrefix + PromotedVersion + "/codex-" + triple + ".tar.gz"
}

// archiveMember is the one file the archive holds. It is named for the triple
// rather than for the program, so the install renames it on the way in.
func archiveMember(triple string) string { return extractDir + "/codex-" + triple }

// Install reconciles the pinned binary and the stable command path.
//
// It is idempotent in the way that matters for a box an operator reruns
// bootstrap on: when the command path already resolves to the pinned, verified
// target, nothing is downloaded. A drifted install is reported, never silently
// replaced, except for the one case where there is nothing to drift from, which
// is a target that does not exist yet.
func (codexBackend) Install(ctx context.Context, r backend.StepRunner) error {
	const name = "codex_install"
	target := targetPath()

	present, err := r.Probe(ctx, name, "sudo", "-n", "test", "-x", target)
	if err != nil {
		return err
	}
	if present.ExitCode != 0 {
		if !r.Reconcile() {
			return r.Fail(name, "no pinned codex binary at "+target,
				"run `torio vm bootstrap`, which downloads and verifies the pinned version")
		}
		if err := download(ctx, r, name); err != nil {
			return err
		}
	}

	if err := verifyTarget(ctx, r, name, target); err != nil {
		return err
	}
	return reconcileCommandPath(ctx, r, name, target)
}

// download fetches the pinned archive, proves it against the committed digest,
// and installs the single binary it holds root-owned.
//
// Nothing is extracted before the digest matches. Extraction is the first step
// that writes attacker-controlled names to disk, so it happens on the far side
// of the check rather than the near side.
func download(ctx context.Context, r backend.StepRunner, name string) error {
	triple, digest, err := guestTarget(ctx, r, name)
	if err != nil {
		return err
	}

	dl, err := r.Probe(ctx, name, "sudo", "-n", "--", "curl", "-fsSL", "-o", stagingPath, archiveURL(triple))
	if err != nil {
		return err
	}
	if dl.ExitCode != 0 {
		return r.Fail(name, "could not download the pinned codex archive",
			"check guest network and curl, then re-run bootstrap")
	}

	// The expected digest travels as stdin, never as an argv element. The check
	// that decides whether we execute these bytes should not also be a place a
	// value is interpolated into a command line.
	sum, err := r.ProbeInput(ctx, name, []byte(digest+"  "+stagingPath+"\n"),
		[]string{"sudo", "-n", "--", "sha256sum", "--strict", "--check", "-"})
	if err != nil {
		return err
	}
	if sum.ExitCode != 0 {
		// Remove the unverified bytes before returning, so a failed run cannot
		// leave something archive-shaped behind for a later one to adopt.
		_, _ = r.Probe(ctx, name, "sudo", "-n", "rm", "-f", stagingPath)
		return r.Fail(name, "the downloaded codex archive does not match the pinned digest",
			"do not use it; re-run bootstrap, and if it recurs treat the download path as untrusted")
	}

	if err := extract(ctx, r, name, triple); err != nil {
		return err
	}

	if rm, err := r.Probe(ctx, name, "sudo", "-n", "rm", "-f", stagingPath); err != nil {
		return err
	} else if rm.ExitCode != 0 {
		return r.Fail(name, "could not remove the download staging file", "remove "+stagingPath+" on the guest")
	}
	return nil
}

// extract unpacks the verified archive into a directory nothing else owns and
// installs its one member as the versioned target.
func extract(ctx context.Context, r backend.StepRunner, name, triple string) error {
	if _, err := r.Probe(ctx, name, "sudo", "-n", "rm", "-rf", extractDir); err != nil {
		return err
	}
	mk, err := r.Probe(ctx, name, "sudo", "-n", "install", "-d", "-o", "root", "-g", "root", "-m", "0700", extractDir)
	if err != nil {
		return err
	}
	if mk.ExitCode != 0 {
		return r.Fail(name, "could not create the extraction directory", "check write access to "+installDir)
	}

	tar, err := r.Probe(ctx, name, "sudo", "-n", "--", "tar", "-xzf", stagingPath, "-C", extractDir)
	if err != nil {
		return err
	}
	if tar.ExitCode != 0 {
		return r.Fail(name, "could not extract the verified codex archive", "check tar and disk space on the guest")
	}

	inst, err := r.Probe(ctx, name, "sudo", "-n", "install", "-o", "root", "-g", "root", "-m", "0755",
		archiveMember(triple), targetPath())
	if err != nil {
		return err
	}
	if inst.ExitCode != 0 {
		return r.Fail(name, "could not install the verified binary",
			"the archive did not hold the expected member, or "+installDir+" is not writable")
	}
	if _, err := r.Probe(ctx, name, "sudo", "-n", "rm", "-rf", extractDir); err != nil {
		return err
	}
	return nil
}

// verifyTarget proves the pinned binary is one the agent cannot rewrite. This is
// the property the whole install exists to establish: the command an operator
// may later run under sudo must not be a file the agent owns.
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
	const link = "codex_command_path"
	cur, err := r.Probe(ctx, link, "readlink", commandPath)
	if err != nil {
		return err
	}
	if trimmed(cur.Stdout) == target {
		r.Record(link, true, "already correct")
		return nil
	}
	if !r.Reconcile() {
		return r.Fail(link, "the codex command path does not point at "+target,
			"run `torio vm bootstrap`, which repoints "+commandPath)
	}
	ln, err := r.Probe(ctx, link, "sudo", "-n", "ln", "-sfn", target, commandPath)
	if err != nil {
		return err
	}
	if ln.ExitCode != 0 {
		return r.Fail(link, "could not install the codex command path", "check write access to "+commandPath)
	}
	r.Record(link, true, "installed")
	return nil
}

// guestTarget maps the guest's architecture onto a published triple and the
// digest pinned for it.
//
// An architecture nobody pinned a digest for stops here. Falling through would
// mean downloading bytes with nothing to check them against, which is the one
// thing this install exists to prevent.
func guestTarget(ctx context.Context, r backend.StepRunner, name string) (triple, digest string, err error) {
	res, err := r.Probe(ctx, name, "uname", "-m")
	if err != nil {
		return "", "", err
	}
	arch := trimmed(res.Stdout)
	switch arch {
	case "aarch64":
		triple = "aarch64-unknown-linux-musl"
	case "x86_64":
		triple = "x86_64-unknown-linux-musl"
	default:
		return "", "", r.Fail(name, "unsupported guest architecture "+arch,
			"Torio's guest is a pinned Linux image on arm64 or x86_64")
	}
	digest, ok := pinnedDigests[triple]
	if !ok || !isHexDigest(digest) {
		return "", "", r.Fail(name, "no pinned digest for "+triple,
			"the pin must carry a sha256 for every architecture a guest can be")
	}
	return triple, digest, nil
}

// VerifyVersion proves the documented stable command path answers, as the
// backend identity, with the pinned version.
//
// Equality, not containment: the pin exists so that what runs is knowable, and
// "the output mentioned the version somewhere" is a weaker claim than it looks.
func (codexBackend) VerifyVersion(ctx context.Context, r backend.StepRunner) error {
	const name = versionCheck
	res, err := r.Probe(ctx, name, "sudo", "-n", "-u", User, "-H", "--", "codex", "--version")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return r.Fail(name, "`codex --version` exited non-zero", "confirm the command path and install on the guest")
	}
	version, ok := parseVersion(string(res.Stdout))
	if !ok {
		return r.Fail(name, "`codex --version` produced no recognizable version",
			"a clean exit is not proof; inspect the install on the guest")
	}
	if version != PromotedVersion {
		return r.Fail(name, fmt.Sprintf("codex version %q, pinned %q", version, PromotedVersion),
			"version drift: reconcile the pinned install, do not paper over it")
	}
	if pinned := r.PinnedVersion(); pinned != "" && version != pinned {
		return r.Fail(name, fmt.Sprintf("codex version %q, run pinned %q", version, pinned),
			"version drift against the requested pin")
	}
	r.Record(name, true, version)
	return nil
}

// parseVersion reads the version from `codex --version`, whose first line is
// "codex-cli <semver>". The marker leads here and trails on the other backend,
// so position is checked and not only presence: a line carrying both words in
// the other order is the other program's output, or nobody's.
func parseVersion(out string) (string, bool) {
	fields := strings.Fields(firstLine(out))
	if len(fields) < 2 || fields[0] != versionMarker {
		return "", false
	}
	return fields[1], true
}
