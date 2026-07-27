package lima

import (
	"context"
	"fmt"
	"strings"

	"github.com/wzslr321/torio/internal/execx"
)

// Bootstrap reconciles and verifies the already-created Torio target so an
// operator has a usable Remote Second Brain V1 path: a stable non-interactive
// `hermes` command and the V1 guest filesystem layout on native ext4.
//
// It is deliberately narrow. It operates ONLY on the existing InstanceName after
// a verified Running precondition, through the same typed limactl/execx boundary
// as the rest of the adapter (fixed argv, no `sh -c`, no concatenated command
// strings, bounded+redacted output). It never creates, recreates, deletes, or
// re-images the VM; never installs a model/provider; never accepts secrets; and
// never creates gateway/serve services.
//
// When the pinned Hermes Agent launcher is missing, bootstrap reconciles a
// Gate-0-pinned install (PromotedHermesCommit at hermesAgentDir) using the
// upstream install.sh script downloaded to a hermes-writable path — never
// curl|bash pipe. Residual risk: install.sh content is not checksum-pinned;
// the verifiable postcondition is git HEAD == PromotedHermesCommit and an
// executable launcher at hermesTarget. Then it reconciles the PATH shim.
//
// The intended guest identity is the dedicated non-root `hermes` service user
// (uid distinct from the Lima login user): it owns the persistent profile under
// /home/hermes/.hermes and the Second Brain vault under /home/hermes/brain, and
// is a member of torio-projects (not the docker group — rootful Docker for
// hermes is forbidden per ADR-0015). `limactl shell` logs in as the Lima user,
// so the documented stable path reaches hermes explicitly via
// `sudo -u hermes -- hermes …`; the bare `hermes` name resolves through a fixed
// symlink on sudo's secure_path.
//
// Reconcile is idempotent and limited to the narrowly declared Hermes install and
// PATH/shim setup:
//   - when the pinned launcher is missing, install Hermes Agent at the Gate-0
//     commit via the upstream install script (typed argv, no pipe), then verify
//     git HEAD and launcher executability;
//   - ensure /usr/local/bin/hermes is a symlink to the pinned launcher, but only
//     after confirming the launcher exists (a missing launcher after reconcile is
//     reported as drift, never papered over with a dangling shim).
//
// Verification proves (never merely trusts an exit code) every postcondition and
// fails closed on any mismatch or unverifiable state:
//   - the hermes user exists;
//   - group torio-projects exists and hermes is a member;
//   - the operator (Lima login user) is a member of torio-projects;
//   - hermes is NOT in the docker group;
//   - uname -m == aarch64;
//   - `hermes --version` works through the documented stable command path;
//   - git --version works;
//   - each required path is a directory with the expected owner, group, and mode
//     on native ext4;
//   - no macOS host-share mount (9p/virtiofs/fuse/nfs/cifs) is present.
//
// A rerun is success only when all postconditions are proven. A drift
// (architecture/version/image/mount/ownership) is reported, not repaired.
func (a *Adapter) Bootstrap(ctx context.Context, opts BootstrapOptions) (BootstrapReport, error) {
	rep := BootstrapReport{Instance: InstanceName}

	// Precondition: the target must exist and be Running. Not-found, Stopped and
	// ambiguous states each fail closed with their own kind so the CLI can map
	// them precisely (missing/stopped/ambiguous are all preconditions).
	rec, err := a.currentInstance(ctx, bootstrapOp)
	if err != nil {
		return rep, err
	}
	if rec == nil {
		return rep, &Error{Op: bootstrapOp, Kind: KindNotFound, Err: fmt.Errorf("instance %q does not exist; run init first", InstanceName)}
	}
	st, ok := mapLimaStatus(rec.Status)
	if !ok {
		return rep, &Error{Op: bootstrapOp, Kind: KindMalformedOutput, Err: fmt.Errorf("unrecognized lima status %q", rec.Status)}
	}
	switch st {
	case StateRunning:
		// proceed
	case StateStopped:
		return rep, &Error{Op: bootstrapOp, Kind: KindNotRunning, Err: fmt.Errorf("instance %q is stopped; run `torio vm start` first", InstanceName)}
	default:
		return rep, &Error{Op: bootstrapOp, Kind: KindAmbiguousState, Err: fmt.Errorf("instance %q is in ambiguous state %q", InstanceName, rec.Status)}
	}

	if err := validateOperatorUser(opts.OperatorUser); err != nil {
		return rep, &Error{Op: bootstrapOp, Kind: KindVerificationFailed, Err: err}
	}

	// --- Verify guest identity layout (read-only, fail closed) ---
	if err := a.verifyHermesUser(ctx, &rep); err != nil {
		return rep, err
	}
	if err := a.verifyTorioProjectsGroup(ctx, &rep); err != nil {
		return rep, err
	}
	if err := a.verifyHermesInTorioProjects(ctx, &rep); err != nil {
		return rep, err
	}
	if err := a.verifyOperatorInTorioProjects(ctx, &rep, opts.OperatorUser); err != nil {
		return rep, err
	}
	if err := a.verifyHermesNotInDocker(ctx, &rep); err != nil {
		return rep, err
	}

	// --- Reconcile (idempotent, narrow) ---
	if err := a.reconcileHermesInstall(ctx, &rep); err != nil {
		return rep, err
	}
	if err := a.reconcileHermesShim(ctx, &rep); err != nil {
		return rep, err
	}

	// --- Verify runtime and filesystem postconditions ---
	if err := a.verifyArch(ctx, &rep); err != nil {
		return rep, err
	}
	if err := a.verifyHermes(ctx, &rep, opts.HermesVersion); err != nil {
		return rep, err
	}
	if err := a.verifyGit(ctx, &rep); err != nil {
		return rep, err
	}
	if err := a.verifyPaths(ctx, &rep); err != nil {
		return rep, err
	}
	if err := a.verifyNoHostMounts(ctx, &rep); err != nil {
		return rep, err
	}

	return rep, nil
}

const bootstrapOp = "bootstrap"

// The intended guest identity and the persistent Hermes locations. These are
// exported so the CLI can surface the Remote Second Brain V1 connection handoff
// without duplicating string literals.
const (
	// HermesUser is the dedicated non-root guest identity that owns the
	// persistent profile and Second Brain vault.
	HermesUser = "hermes"
	// HermesHome is the persistent Hermes home on the VM's Linux filesystem.
	HermesHome = "/home/hermes"
	// HermesProfilePath is the persistent Hermes application profile / state
	// directory (NOT the Second Brain vault).
	HermesProfilePath = "/home/hermes/.hermes"
	// HermesBrainPath is the persistent Second Brain vault directory.
	HermesBrainPath = "/home/hermes/brain"
	// HermesWorkspacePath is the persistent shared workspace directory.
	HermesWorkspacePath = "/home/hermes/projects"
)

// The fixed, repository-controlled reconcile targets. These are constants (not
// caller input) so the guest changes are a small, auditable, fixed set — never a
// general remote-script transport.
const (
	dockerGroup        = "docker"
	torioProjectsGroup = "torio-projects"
	requiredArch       = "aarch64"
	hermesAgentDir     = "/home/hermes/hermes-agent"
	hermesTarget       = "/home/hermes/hermes-agent/venv/bin/hermes" // pinned launcher (owned by hermes)
	hermesShimPath     = "/usr/local/bin/hermes"                     // on sudo secure_path
	hermesInstallScriptPath = "/home/hermes/.torio-hermes-install.sh"
	hermesInstallScriptURL  = "https://hermes-agent.nousresearch.com/install.sh"
)

// bootstrapPathSpec is one required directory and its expected ownership/mode.
type bootstrapPathSpec struct {
	path   string
	owner  string
	group  string
	modes  []string // accepted stat -c %a values (0710 may appear as 710)
	setgid bool     // when true, mode must have the setgid bit (2xxx)
}

// bootstrapRequiredPaths are the persistent Hermes directories that must resolve
// on the VM's native Linux filesystem with the V1 layout (ADR-0015 / Gate 0
// FINDINGS). Owned paths are inspected via sudo.
var bootstrapRequiredPaths = []bootstrapPathSpec{
	{path: HermesHome, owner: HermesUser, group: torioProjectsGroup, modes: []string{"710", "0710"}},
	{path: HermesProfilePath, owner: HermesUser, group: HermesUser, modes: []string{"750", "0750"}},
	{path: HermesBrainPath, owner: HermesUser, group: HermesUser, modes: []string{"750", "0750"}},
	{path: HermesWorkspacePath, owner: HermesUser, group: torioProjectsGroup, modes: []string{"2770"}, setgid: true},
}

// hostShareFSTypes is the findmnt -t filter for macOS host-share filesystems. A
// broad host mount over the guest is an ADR-0003 violation and fails closed.
const hostShareFSTypes = "9p,virtiofs,fuse,fuse.virtiofs,nfs,cifs"

// nativeFSTypes are the accepted on-VM block-backed filesystem types for the
// required paths. ext4 is the verified target (etap-0b); the near neighbours are
// admitted so a benign reformat is not a false drift, while every host-share
// type is still rejected.
var nativeFSTypes = map[string]bool{"ext4": true, "ext3": true, "ext2": true, "xfs": true, "btrfs": true}

// BootstrapOptions carries the pins and operator identity the caller wants
// enforced. The adapter does not import internal/config; the CLI passes pinned
// values through.
type BootstrapOptions struct {
	// OperatorUser is the Lima login identity; required and validated against the
	// strict allowlist before any guest work.
	OperatorUser string
	// HermesVersion, if non-empty, is the pinned Hermes version the observed
	// `hermes --version` must contain; a mismatch is reported as drift. Empty is
	// unpinned: the observed version is reported but not enforced.
	HermesVersion string
}

// CheckResult is one bootstrap postcondition or reconcile outcome. Detail is a
// short, already-redacted, derived value (a parsed version, an fstype, a
// reconcile note) — never a raw output blob.
type CheckResult struct {
	Name   string
	OK     bool
	Detail string
}

// BootstrapReport is the structured outcome. On success every check is OK; on
// failure it carries the checks recorded up to (and including) the failing one,
// so the CLI can surface a precise, redacted diagnostic in error.details.
type BootstrapReport struct {
	Instance string
	Checks   []CheckResult
}

func (r *BootstrapReport) record(name string, ok bool, detail string) {
	r.Checks = append(r.Checks, CheckResult{Name: name, OK: ok, Detail: boundDetail(detail)})
}

// verifyFailed records a failed check and returns the fail-closed adapter error
// with an actionable, redacted remediation message.
func (a *Adapter) verifyFailed(rep *BootstrapReport, name, detail, remediation string) *Error {
	rep.record(name, false, detail)
	return &Error{Op: bootstrapOp, Kind: KindVerificationFailed, Err: fmt.Errorf("%s: %s (%s)", name, detail, remediation)}
}

// guestProbe runs a fixed guest argv through the typed SSH boundary and returns
// a usable, non-truncated result. A transport failure (binary/timeout/cancel) is
// returned as the adapter's already-classified error. Truncated output is
// untrustworthy and fails closed as a verification failure.
func (a *Adapter) guestProbe(ctx context.Context, rep *BootstrapReport, name string, argv ...string) (execx.Result, error) {
	res, err := a.SSH(ctx, argv)
	if err != nil {
		return execx.Result{}, err // already a classified *lima.Error (timeout/cancel/binary)
	}
	if res.StdoutTruncated || res.StderrTruncated {
		return execx.Result{}, a.verifyFailed(rep, name, "guest output was truncated", "re-run with a smaller probe or inspect the guest manually")
	}
	return res, nil
}

// reconcileHermesInstall ensures the Gate-0-pinned Hermes Agent tree exists at
// hermesAgentDir with HEAD == PromotedHermesCommit and an executable launcher.
// When the launcher is already present, only the git pin is verified. When it is
// missing, bootstrap runs apt-get deps, downloads install.sh to a hermes-writable
// path (never curl|bash pipe), runs it with fixed flags, removes the script, and
// verifies launcher + commit. install.sh content is not checksum-pinned; the
// verifiable postcondition is git HEAD and launcher path.
func (a *Adapter) reconcileHermesInstall(ctx context.Context, rep *BootstrapReport) error {
	const name = "hermes_install"
	present, err := a.guestProbe(ctx, rep, name, "sudo", "-n", "test", "-x", hermesTarget)
	if err != nil {
		return err
	}
	if present.ExitCode == 0 {
		return a.verifyHermesGitPin(ctx, rep, name)
	}

	if err := a.installHermesDeps(ctx, rep, name); err != nil {
		return err
	}
	dl, err := a.guestProbe(ctx, rep, name,
		"sudo", "-n", "-u", HermesUser, "--",
		"curl", "-fsSL", "-o", hermesInstallScriptPath, hermesInstallScriptURL,
	)
	if err != nil {
		return err
	}
	if dl.ExitCode != 0 {
		return a.verifyFailed(rep, name, "could not download hermes install script", "check guest network and curl")
	}
	run, err := a.guestProbe(ctx, rep, name,
		"sudo", "-n", "-u", HermesUser, "--",
		"bash", hermesInstallScriptPath,
		"--non-interactive", "--skip-setup", "--skip-browser",
		"--dir", hermesAgentDir,
		"--hermes-home", HermesProfilePath,
		"--commit", PromotedHermesCommit,
	)
	if err != nil {
		return err
	}
	if run.ExitCode != 0 {
		return a.verifyFailed(rep, name, "hermes install script exited non-zero", "inspect the install script output on the guest")
	}
	rm, err := a.guestProbe(ctx, rep, name, "sudo", "-n", "rm", "-f", hermesInstallScriptPath)
	if err != nil {
		return err
	}
	if rm.ExitCode != 0 {
		return a.verifyFailed(rep, name, "could not remove downloaded install script", "remove "+hermesInstallScriptPath+" on the guest")
	}
	execOK, err := a.guestProbe(ctx, rep, name, "sudo", "-n", "test", "-x", hermesTarget)
	if err != nil {
		return err
	}
	if execOK.ExitCode != 0 {
		return a.verifyFailed(rep, name, "launcher not executable after install", "re-run bootstrap or inspect the hermes install on the guest")
	}
	return a.verifyHermesGitPin(ctx, rep, name)
}

func (a *Adapter) installHermesDeps(ctx context.Context, rep *BootstrapReport, name string) error {
	upd, err := a.guestProbe(ctx, rep, name, "sudo", "-n", "apt-get", "update", "-y")
	if err != nil {
		return err
	}
	if upd.ExitCode != 0 {
		return a.verifyFailed(rep, name, "apt-get update failed", "fix guest apt sources and re-run bootstrap")
	}
	inst, err := a.guestProbe(ctx, rep, name,
		"sudo", "-n", "apt-get", "install", "-y", "--no-install-recommends",
		"ripgrep", "ffmpeg", "build-essential", "python3-dev", "libffi-dev",
		"curl", "ca-certificates", "git",
	)
	if err != nil {
		return err
	}
	if inst.ExitCode != 0 {
		return a.verifyFailed(rep, name, "apt-get install of hermes build deps failed", "fix guest apt and re-run bootstrap")
	}
	return nil
}

func (a *Adapter) verifyHermesGitPin(ctx context.Context, rep *BootstrapReport, name string) error {
	head, err := a.guestProbe(ctx, rep, name,
		"sudo", "-n", "-u", HermesUser, "--",
		"git", "-C", hermesAgentDir, "rev-parse", "HEAD",
	)
	if err != nil {
		return err
	}
	observed := strings.TrimSpace(string(head.Stdout))
	if head.ExitCode != 0 || observed == "" {
		return a.verifyFailed(rep, name, "could not read hermes agent git HEAD", "confirm the install at "+hermesAgentDir)
	}
	if observed != PromotedHermesCommit {
		return a.verifyFailed(rep, name,
			fmt.Sprintf("hermes agent commit %q != pinned %q", observed, PromotedHermesCommit),
			"reconcile the pinned hermes install; do not paper over commit drift")
	}
	rep.record(name, true, "commit="+PromotedHermesCommit)
	return nil
}

func (a *Adapter) reconcileHermesShim(ctx context.Context, rep *BootstrapReport) error {
	const name = "hermes_shim"
	// Never create a dangling shim: confirm the pinned launcher exists first. A
	// missing launcher is drift, not something to repair.
	present, err := a.guestProbe(ctx, rep, name, "sudo", "-n", "test", "-x", hermesTarget)
	if err != nil {
		return err
	}
	if present.ExitCode != 0 {
		return a.verifyFailed(rep, name, "pinned hermes launcher not found at "+hermesTarget, "the hermes install drifted; re-provision the agent before bootstrap")
	}
	link, err := a.guestProbe(ctx, rep, name, "readlink", hermesShimPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(link.Stdout)) == hermesTarget {
		rep.record(name, true, "shim already correct")
		return nil
	}
	ln, err := a.guestProbe(ctx, rep, name, "sudo", "-n", "ln", "-sfn", hermesTarget, hermesShimPath)
	if err != nil {
		return err
	}
	if ln.ExitCode != 0 {
		return a.verifyFailed(rep, name, "could not install the hermes shim", "check write access to "+hermesShimPath)
	}
	rep.record(name, true, "shim installed")
	return nil
}

func (a *Adapter) verifyHermesUser(ctx context.Context, rep *BootstrapReport) error {
	const name = "hermes_user"
	res, err := a.guestProbe(ctx, rep, name, "id", "-u", HermesUser)
	if err != nil {
		return err
	}
	uid := strings.TrimSpace(string(res.Stdout))
	if res.ExitCode != 0 || uid == "" {
		return a.verifyFailed(rep, name, "hermes user not found", "provision the hermes service user on the guest")
	}
	rep.record(name, true, "uid="+uid)
	return nil
}

func (a *Adapter) verifyTorioProjectsGroup(ctx context.Context, rep *BootstrapReport) error {
	const name = "torio_projects_group"
	res, err := a.guestProbe(ctx, rep, name, "getent", "group", torioProjectsGroup)
	if err != nil {
		return err
	}
	line := strings.TrimSpace(string(res.Stdout))
	if res.ExitCode != 0 || line == "" {
		return a.verifyFailed(rep, name, "group torio-projects not found", "create the torio-projects group on the guest")
	}
	rep.record(name, true, torioProjectsGroup)
	return nil
}

func (a *Adapter) verifyHermesInTorioProjects(ctx context.Context, rep *BootstrapReport) error {
	const name = "hermes_torio_projects"
	res, err := a.guestProbe(ctx, rep, name, "id", "-nG", HermesUser)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return a.verifyFailed(rep, name, "cannot read hermes group membership", "confirm the hermes user exists on the guest")
	}
	if !hasGroup(string(res.Stdout), torioProjectsGroup) {
		return a.verifyFailed(rep, name, "hermes is not in torio-projects", "add hermes to the torio-projects group on the guest")
	}
	rep.record(name, true, "member")
	return nil
}

func (a *Adapter) verifyOperatorInTorioProjects(ctx context.Context, rep *BootstrapReport, operator string) error {
	const name = "operator_torio_projects"
	res, err := a.guestProbe(ctx, rep, name, "id", "-nG", operator)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return a.verifyFailed(rep, name, "cannot read operator group membership", "confirm the Lima login user exists on the guest")
	}
	if !hasGroup(string(res.Stdout), torioProjectsGroup) {
		return a.verifyFailed(rep, name, "operator is not in torio-projects", "add the Lima login user to torio-projects on the guest")
	}
	rep.record(name, true, "member")
	return nil
}

func (a *Adapter) verifyHermesNotInDocker(ctx context.Context, rep *BootstrapReport) error {
	const name = "hermes_not_in_docker"
	res, err := a.guestProbe(ctx, rep, name, "id", "-nG", HermesUser)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return a.verifyFailed(rep, name, "cannot read hermes group membership", "confirm the hermes user exists on the guest")
	}
	if hasGroup(string(res.Stdout), dockerGroup) {
		return a.verifyFailed(rep, name, "hermes is in the docker group", "remove hermes from the docker group; rootful Docker for hermes is forbidden (ADR-0015)")
	}
	rep.record(name, true, "not a member")
	return nil
}

func (a *Adapter) verifyArch(ctx context.Context, rep *BootstrapReport) error {
	const name = "arch"
	res, err := a.guestProbe(ctx, rep, name, "uname", "-m")
	if err != nil {
		return err
	}
	arch := strings.TrimSpace(string(res.Stdout))
	if res.ExitCode != 0 || arch != requiredArch {
		return a.verifyFailed(rep, name, fmt.Sprintf("arch %q, want %q", arch, requiredArch), "the target VM must be Linux arm64")
	}
	rep.record(name, true, arch)
	return nil
}

func (a *Adapter) verifyHermes(ctx context.Context, rep *BootstrapReport, pinned string) error {
	const name = "hermes_version"
	// The documented stable command path: as the hermes user, via the bare
	// `hermes` name resolved by the shim on sudo's secure_path.
	res, err := a.guestProbe(ctx, rep, name, "sudo", "-n", "-u", HermesUser, "--", "hermes", "--version")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return a.verifyFailed(rep, name, "`hermes --version` exited non-zero", "confirm the hermes shim and install on the guest")
	}
	version, okv := parseHermesVersion(string(res.Stdout))
	if !okv {
		return a.verifyFailed(rep, name, "`hermes --version` produced no recognizable version", "a clean exit is not proof; inspect the hermes install")
	}
	if pinned != "" && version != pinned {
		return a.verifyFailed(rep, name, fmt.Sprintf("hermes version %q, pinned %q", version, pinned), "version drift: reconcile the pinned hermes install, do not paper over")
	}
	rep.record(name, true, version)
	return nil
}

func (a *Adapter) verifyGit(ctx context.Context, rep *BootstrapReport) error {
	const name = "git"
	res, err := a.guestProbe(ctx, rep, name, "git", "--version")
	if err != nil {
		return err
	}
	out := strings.TrimSpace(string(res.Stdout))
	if res.ExitCode != 0 || !strings.HasPrefix(out, "git version ") {
		return a.verifyFailed(rep, name, "`git --version` did not report a version", "install git on the guest")
	}
	rep.record(name, true, strings.TrimPrefix(out, "git version "))
	return nil
}

func (a *Adapter) verifyPaths(ctx context.Context, rep *BootstrapReport) error {
	for _, spec := range bootstrapRequiredPaths {
		name := "path:" + spec.path
		st, err := a.guestProbe(ctx, rep, name, "sudo", "-n", "stat", "-c", "%F", spec.path)
		if err != nil {
			return err
		}
		if st.ExitCode != 0 || strings.TrimSpace(string(st.Stdout)) != "directory" {
			return a.verifyFailed(rep, name, "not a directory", "create the persistent Hermes directory on the guest")
		}
		og, err := a.guestProbe(ctx, rep, name, "sudo", "-n", "stat", "-c", "%U:%G %a", spec.path)
		if err != nil {
			return err
		}
		if og.ExitCode != 0 {
			return a.verifyFailed(rep, name, "could not read ownership/mode", "verify the path exists on the guest")
		}
		owner, group, mode, ok := parseStatOwnership(string(og.Stdout))
		if !ok {
			return a.verifyFailed(rep, name, "unparseable ownership/mode", "verify the path exists on the guest")
		}
		if owner != spec.owner || group != spec.group {
			return a.verifyFailed(rep, name, fmt.Sprintf("owner:group %s:%s, want %s:%s", owner, group, spec.owner, spec.group), "fix directory ownership on the guest")
		}
		if !modeMatches(spec, mode) {
			return a.verifyFailed(rep, name, fmt.Sprintf("mode %s, want one of %v", mode, spec.modes), "fix directory permissions on the guest")
		}
		fm, err := a.guestProbe(ctx, rep, name, "sudo", "-n", "findmnt", "-n", "-o", "FSTYPE,SOURCE", "-T", spec.path)
		if err != nil {
			return err
		}
		fields := strings.Fields(string(fm.Stdout))
		if fm.ExitCode != 0 || len(fields) < 1 {
			return a.verifyFailed(rep, name, "could not resolve the backing filesystem", "verify the path exists on the guest")
		}
		fstype := fields[0]
		if !nativeFSTypes[fstype] {
			return a.verifyFailed(rep, name, "backed by non-native filesystem "+fstype, "Hermes state must live on the VM's Linux filesystem, not a host share (ADR-0003)")
		}
		rep.record(name, true, fmt.Sprintf("%s:%s %s %s", owner, group, mode, strings.Join(fields, " ")))
	}
	return nil
}

func (a *Adapter) verifyNoHostMounts(ctx context.Context, rep *BootstrapReport) error {
	const name = "no_host_mounts"
	// findmnt has an exact, evidence-backed PASS contract: it exits 1 with empty
	// output only when nothing matches the host-share filter. That is the ONLY
	// result we accept as "no broad host mount". A matched line (exit 0, non-empty)
	// is a host mount and fails closed. Every other shape is unreliable and must
	// also fail closed rather than be read as PASS — e.g. exit 127 when findmnt is
	// missing, an unexpected exit 0 with empty output, or any other query failure.
	// Empty stdout alone is NOT proof of "no host share"; only exit 1 + empty is.
	res, err := a.guestProbe(ctx, rep, name, "findmnt", "-rn", "-t", hostShareFSTypes, "-o", "TARGET,FSTYPE")
	if err != nil {
		return err
	}
	out := strings.TrimSpace(string(res.Stdout))
	switch {
	case res.ExitCode == 1 && out == "":
		// Documented PASS shape: no host-share mount matched.
		rep.record(name, true, "none")
		return nil
	case res.ExitCode == 0 && out != "":
		// findmnt matched at least one host-share mount over the guest.
		return a.verifyFailed(rep, name, "a macOS host-share mount is present", "remove the broad host mount; only a narrow transfer folder is allowed (ADR-0003)")
	default:
		// Anything else is not the evidence-backed PASS shape and cannot prove the
		// absence of a host mount: exit 127 (findmnt missing / exec failure), an
		// unexpected exit 0 with empty output, exit 1 with stray output, or any
		// other nonzero query failure. Fail closed.
		return a.verifyFailed(rep, name, fmt.Sprintf("could not reliably determine host-share mounts (exit %d)", res.ExitCode), "confirm findmnt is present on the guest and re-run bootstrap")
	}
}

// hasGroup reports whether a space-separated `id -nG` group list contains group.
func hasGroup(groups, group string) bool {
	for _, g := range strings.Fields(groups) {
		if g == group {
			return true
		}
	}
	return false
}

// parseStatOwnership parses `stat -c '%U:%G %a'` output into owner, group, mode.
func parseStatOwnership(out string) (owner, group, mode string, ok bool) {
	line := strings.TrimSpace(out)
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", "", "", false
	}
	mode = fields[len(fields)-1]
	og := strings.Join(fields[:len(fields)-1], " ")
	parts := strings.SplitN(og, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || mode == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], mode, true
}

// modeMatches reports whether the observed stat mode satisfies the path spec.
func modeMatches(spec bootstrapPathSpec, mode string) bool {
	for _, want := range spec.modes {
		if mode == want {
			return true
		}
	}
	return false
}

// parseHermesVersion extracts the semver-ish version from `hermes --version`
// output, whose first line is verified to look like
// "Hermes Agent v0.19.0 (2026.7.20) · upstream …". A recognizable version is
// required: a clean exit with unrecognized output is not proof.
func parseHermesVersion(out string) (string, bool) {
	line := strings.TrimSpace(firstLine(out))
	const marker = "Hermes Agent v"
	i := strings.Index(line, marker)
	if i < 0 {
		return "", false
	}
	rest := line[i+len(marker):]
	end := strings.IndexAny(rest, " \t(")
	if end >= 0 {
		rest = rest[:end]
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", false
	}
	return rest, true
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// boundDetail caps a derived detail string so a report value can never carry an
// unbounded blob into the JSON envelope.
func boundDetail(s string) string {
	const max = 200
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
