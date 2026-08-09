package lima

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/execx"
)

// Bootstrap reconciles and verifies the already-created Torio target so an
// operator has a usable guest: the configured backend installed at its pin and
// reachable by a stable non-interactive command, and the guest filesystem
// layout on a native filesystem.
//
// It is deliberately narrow. It operates ONLY on the existing InstanceName after
// a verified Running precondition, through the same typed limactl/execx boundary
// as the rest of the adapter (fixed argv, no `sh -c`, no concatenated command
// strings, bounded+redacted output). It never creates, recreates, deletes, or
// re-images the VM; never installs a model/provider; never accepts secrets; and
// never creates gateway/serve services.
//
// The steps below are the ones Torio owns for every backend: the shared group
// exists, the operator is in it and the guest session actually carries it, the
// architecture matches the host pin, git is present, the required paths resolve
// on a native filesystem with the expected ownership, no host share is mounted
// over the guest, and both session helpers are root-owned. Interleaved with
// them, in a fixed order, are the backend's own steps — its identity, its
// install and pin, its version, its guardrail files, its credential presence.
// A backend proves what it declares and nothing is checked on its behalf.
//
// Reconcile is idempotent and limited to what the backend declares it installs.
// Verification proves (never merely trusts an exit code) every postcondition
// and fails closed on any mismatch or unverifiable state.
//
// A rerun is success only when all postconditions are proven. A drift
// (architecture/version/image/mount/ownership) is reported, not repaired.
func (a *Adapter) Bootstrap(ctx context.Context, opts BootstrapOptions) (BootstrapReport, error) {
	rep := BootstrapReport{Instance: InstanceName}
	b := opts.Backend
	if b == nil {
		b = Hermes()
	}
	rep.Backend = b.Identity().Name

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
	case StateStopped:
		return rep, &Error{Op: bootstrapOp, Kind: KindNotRunning, Err: fmt.Errorf("instance %q is stopped; run `torio vm start` first", InstanceName)}
	default:
		return rep, &Error{Op: bootstrapOp, Kind: KindAmbiguousState, Err: fmt.Errorf("instance %q is in ambiguous state %q", InstanceName, rec.Status)}
	}

	if err := validateOperatorUser(opts.OperatorUser); err != nil {
		return rep, &Error{Op: bootstrapOp, Kind: KindVerificationFailed, Err: err}
	}

	// The backend's steps run against the same fail-closed machinery as ours:
	// truncated output is not evidence, a failed check is recorded in the report
	// the operator reads, and a failure carries a remediation.
	r := &stepRunner{adapter: a, report: &rep, pinnedVersion: opts.PinnedVersion, reconcile: !opts.VerifyOnly}

	// Identity first, then the shared group it must be in, then the group's
	// other member, then what the identity must NOT hold. Each agnostic step
	// sits where its precondition has just been proven.
	if err := b.VerifyIdentity(ctx, r); err != nil {
		return rep, err
	}
	if err := a.verifyTorioProjectsGroup(ctx, &rep); err != nil {
		return rep, err
	}
	if err := b.VerifyMembership(ctx, r); err != nil {
		return rep, err
	}
	if err := a.verifyOperatorInTorioProjects(ctx, &rep, opts.OperatorUser); err != nil {
		return rep, err
	}
	if err := b.VerifyIsolation(ctx, r); err != nil {
		return rep, err
	}

	if err := b.Install(ctx, r); err != nil {
		return rep, err
	}

	if err := a.verifyArch(ctx, &rep); err != nil {
		return rep, err
	}
	if err := b.VerifyVersion(ctx, r); err != nil {
		return rep, err
	}
	if err := a.verifyGit(ctx, &rep); err != nil {
		return rep, err
	}
	if err := a.verifyPaths(ctx, &rep, b.RequiredPaths()); err != nil {
		return rep, err
	}
	if err := a.verifyNoHostMounts(ctx, &rep); err != nil {
		return rep, err
	}
	if err := a.verifyOperatorShellHelper(ctx, &rep, b.Identity().WorkspacePath, r.reconcile); err != nil {
		return rep, err
	}
	if err := a.verifyProjectEnterHelper(ctx, &rep, b.Identity().WorkspacePath, r.reconcile); err != nil {
		return rep, err
	}
	if err := a.verifyAgentSessionHelper(ctx, &rep, b.Session(), r.reconcile); err != nil {
		return rep, err
	}

	// Guardrails and credentials come last: neither is a precondition of the
	// guest being correctly built, and the credential probe never fails a run —
	// a box has to bootstrap before anyone can log in to it.
	if err := b.VerifyGuardrails(ctx, r); err != nil {
		return rep, err
	}
	if err := b.ProbeAuth(ctx, r); err != nil {
		return rep, err
	}

	return rep, nil
}

// stepRunner is the bootstrap run handed to a backend. It is the only way a
// backend reaches the guest, so a backend cannot acquire its own transport, its
// own truncation policy, or its own idea of what a recorded check is.
type stepRunner struct {
	adapter       *Adapter
	report        *BootstrapReport
	pinnedVersion string
	reconcile     bool
}

var _ backend.StepRunner = (*stepRunner)(nil)

func (r *stepRunner) Probe(ctx context.Context, name string, argv ...string) (execx.Result, error) {
	return r.adapter.guestProbe(ctx, r.report, name, argv...)
}

func (r *stepRunner) ProbeInput(ctx context.Context, name string, stdin []byte, argv []string) (execx.Result, error) {
	res, err := r.adapter.SSHInput(ctx, stdin, argv)
	if err != nil {
		return execx.Result{}, err
	}
	if res.StdoutTruncated || res.StderrTruncated {
		return execx.Result{}, r.adapter.verifyFailed(r.report, name, "guest output was truncated", "re-run with a smaller probe or inspect the guest manually")
	}
	return res, nil
}

func (r *stepRunner) Record(name string, ok bool, detail string) { r.report.record(name, ok, detail) }

func (r *stepRunner) Fail(name, detail, remediation string) error {
	return r.adapter.verifyFailed(r.report, name, detail, remediation)
}

func (r *stepRunner) PinnedVersion() string { return r.pinnedVersion }

func (r *stepRunner) Reconcile() bool { return r.reconcile }

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
	// TorioProjectsGroup is the shared guest group that lets the operator and
	// hermes reach the same directories without either becoming the other. It
	// is the only guest authority the two identities have in common, so any
	// staging directory both must touch is grouped here.
	TorioProjectsGroup = "torio-projects"
)

// The fixed, repository-controlled reconcile targets. These are constants (not
// caller input) so the guest changes are a small, auditable, fixed set — never a
// general remote-script transport.
const (
	dockerGroup             = "docker"
	hermesAgentDir          = "/home/hermes/hermes-agent"
	hermesTarget            = "/home/hermes/hermes-agent/venv/bin/hermes" // pinned launcher (owned by hermes)
	hermesShimPath          = "/usr/local/bin/hermes"                     // on sudo secure_path
	hermesInstallScriptPath = "/home/hermes/.torio-hermes-install.sh"
	hermesInstallScriptURL  = "https://hermes-agent.nousresearch.com/install.sh"
)

// hermesBuildDeps are the guest packages the Hermes install needs to build. They
// are named once, here, and used both by the install step and by the template
// that pre-installs them so a first bootstrap does not pay for a compile it
// could have avoided.
var hermesBuildDeps = []string{
	"ripgrep", "ffmpeg", "build-essential", "python3-dev", "libffi-dev",
	"curl", "ca-certificates", "git",
}

// bootstrapRequiredPaths are the persistent Hermes directories that must resolve
// on the VM's native Linux filesystem with the V1 layout (ADR-0003). Owned
// paths are inspected via sudo.
var bootstrapRequiredPaths = []backend.PathSpec{
	{Path: HermesHome, Owner: HermesUser, Group: TorioProjectsGroup, Modes: []string{"710", "0710"}},
	{Path: HermesProfilePath, Owner: HermesUser, Group: HermesUser, Modes: []string{"750", "0750"}, AllowStricter: true},
	{Path: HermesBrainPath, Owner: HermesUser, Group: HermesUser, Modes: []string{"750", "0750"}, AllowStricter: true},
	{Path: HermesWorkspacePath, Owner: HermesUser, Group: TorioProjectsGroup, Modes: []string{"2770"}},
}

// operatorShellHelperSpec is the required guest state of the operator shell
// helper (OperatorShellHelper), the fixed remote argv of `torio project shell`.
// It is provisioned by the Lima template, never reconciled here: bootstrap
// proves it, and a drift is reported rather than repaired.
var operatorShellHelperSpec = backend.PathSpec{
	Path:  OperatorShellHelper,
	Owner: "root",
	Group: "root",
	Modes: []string{"755", "0755"},
}

var projectEnterHelperSpec = backend.PathSpec{
	Path:  ProjectEnterHelper,
	Owner: "root",
	Group: "root",
	Modes: []string{"755", "0755"},
}

// operatorShellHelperRemediation is the one repair for every helper drift: the
// pinned template re-materializes the file, root-owned and 0755, on boot.
const operatorShellHelperRemediation = "restart the VM so provisioning reinstalls " + OperatorShellHelper + " as root:root 0755"

const projectEnterHelperRemediation = "restart the VM so provisioning reinstalls " + ProjectEnterHelper + " as root:root 0755"

// rootHelperInstallScript builds the atomic install for a root-owned guest
// helper. It is derived from the destination so the staging pattern, the final
// destination and the synced directory cannot name different paths.
func rootHelperInstallScript(dest string) string {
	return `
tmp="$(mktemp ` + path.Dir(dest) + `/.` + path.Base(dest) + `.XXXXXX)"
trap 'rm -f -- "$tmp"' EXIT
cat >"$tmp"
chown root:root "$tmp"
chmod 0755 "$tmp"
sync -f "$tmp"
mv -T -- "$tmp" ` + dest + `
sync -f ` + path.Dir(dest) + `
trap - EXIT
`
}

// rootHelperPathSegmentPattern is deliberately narrower than a filesystem
// permits. A backend supplies its session helper path, and bootstrap later
// splices that path into a script executed by root, so every component must be
// one plain shell-inert segment rather than text that needs quoting.
var rootHelperPathSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)

func validateRootHelperPath(dest string) error {
	if !path.IsAbs(dest) || dest == "/" || path.Clean(dest) != dest {
		return fmt.Errorf("helper path must be absolute and canonical")
	}
	for _, component := range strings.Split(strings.TrimPrefix(dest, "/"), "/") {
		if !rootHelperPathSegmentPattern.MatchString(component) {
			return fmt.Errorf("helper path components must contain only plain filename characters")
		}
	}
	return nil
}

// projectEnterInstallScript is the install for the ordinary workspace-session
// helper.
var projectEnterInstallScript = rootHelperInstallScript(ProjectEnterHelper)

// operatorShellInstallScript is the install for the push-capable session helper.
//
// It exists because the template is written once, at `vm init`, and never
// re-rendered: without this the helper could only ever change by recreating the
// VM, which made a corrected helper undeliverable to a box that already existed.
// Installing an *absent* file is not repairing drift — invariant 10 stands, and
// a helper that is present and wrong is still reported and left alone.
var operatorShellInstallScript = rootHelperInstallScript(OperatorShellHelper)

// hostShareFSTypes is the findmnt -t filter for macOS host-share filesystems. A
// broad host mount over the guest is an ADR-0002 violation and fails closed.
const hostShareFSTypes = "9p,virtiofs,fuse,fuse.virtiofs,nfs,cifs"

// nativeFSTypes are the accepted on-VM block-backed filesystem types for the
// required paths. ext4 is the verified target; the near neighbours are
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
	// PinnedVersion, if non-empty, is the version the backend's observed version
	// must equal; a mismatch is reported as drift. Empty is unpinned for this
	// run: a backend that carries its own pin still enforces that one.
	PinnedVersion string
	// Backend is the agent backend this instance runs. A nil Backend is the
	// Hermes backend: every instance created before an instance declared one
	// runs Hermes, and reading an older config must not re-point a box at a
	// different agent.
	Backend backend.Backend
	// VerifyOnly runs every check and repairs nothing. A step that would have
	// installed or linked something fails instead, carrying the remediation
	// that names `torio vm bootstrap`.
	//
	// It is what makes a status command able to say it changes nothing and be
	// telling the truth. The zero value reconciles, because that is what
	// bootstrap is for and a caller that forgets the field must not silently
	// stop repairing the guest.
	VerifyOnly bool
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
	// Backend is the identity name of the backend this run verified.
	Backend string
	Checks  []CheckResult
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

func (a *Adapter) verifyTorioProjectsGroup(ctx context.Context, rep *BootstrapReport) error {
	const name = "torio_projects_group"
	res, err := a.guestProbe(ctx, rep, name, "getent", "group", TorioProjectsGroup)
	if err != nil {
		return err
	}
	line := strings.TrimSpace(string(res.Stdout))
	if res.ExitCode != 0 || line == "" {
		return a.verifyFailed(rep, name, "group torio-projects not found", "create the torio-projects group on the guest")
	}
	rep.record(name, true, TorioProjectsGroup)
	return nil
}

// verifyOperatorInTorioProjects asks the guest session about itself rather than
// asking the group database about a name.
//
// `id -nG <operator>` answers from /etc/group, and the property that decides
// whether the product works is what the *session* carries. Lima multiplexes
// every guest command over one persistent ssh master, and a master that
// authenticated before the operator joined the group keeps serving commands
// without it. That is not hypothetical: provisioning adds the group over the
// session `limactl start` opened, so the master is stale by construction on
// every freshly created machine. This check reported "member" — correctly, from
// the database — while rsync could not traverse HermesHome and `torio brain
// import` failed on a guest that was configured exactly right.
func (a *Adapter) verifyOperatorInTorioProjects(ctx context.Context, rep *BootstrapReport, operator string) error {
	const name = "operator_torio_projects"
	who, err := a.guestProbe(ctx, rep, name, "id", "-un")
	if err != nil {
		return err
	}
	if who.ExitCode != 0 {
		return a.verifyFailed(rep, name, "cannot read the guest session identity", "confirm the Lima login user exists on the guest")
	}
	if session := strings.TrimSpace(string(who.Stdout)); session != operator {
		return a.verifyFailed(rep, name,
			fmt.Sprintf("guest session runs as %q, configured operator is %q", session, operator),
			"reconcile the configured operator with the Lima login user")
	}
	res, err := a.guestProbe(ctx, rep, name, "id", "-nG")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return a.verifyFailed(rep, name, "cannot read operator group membership", "confirm the Lima login user exists on the guest")
	}
	if !hasGroup(string(res.Stdout), TorioProjectsGroup) {
		return a.verifyFailed(rep, name, "the guest session is not in torio-projects",
			"add the Lima login user to torio-projects on the guest, then run `torio vm stop` and `torio vm start` so the guest session picks the group up")
	}
	rep.record(name, true, "member")
	return nil
}

// verifyArch proves the guest runs the architecture this host pins. Lima's
// config spelling and the kernel's `uname -m` agree on both supported
// platforms, so Profile.Arch serves the created-instance check and this guest
// probe from one value.
func (a *Adapter) verifyArch(ctx context.Context, rep *BootstrapReport) error {
	const name = "arch"
	profile, err := a.profile()
	if err != nil {
		return a.verifyFailed(rep, name, err.Error(), "run Torio on a supported host")
	}
	res, err := a.guestProbe(ctx, rep, name, "uname", "-m")
	if err != nil {
		return err
	}
	arch := strings.TrimSpace(string(res.Stdout))
	if res.ExitCode != 0 || arch != profile.Arch {
		return a.verifyFailed(rep, name,
			fmt.Sprintf("arch %q, want %q", arch, profile.Arch),
			"the target VM must be Linux "+profile.Arch)
	}
	rep.record(name, true, arch)
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

func (a *Adapter) verifyPaths(ctx context.Context, rep *BootstrapReport, specs []backend.PathSpec) error {
	for _, spec := range specs {
		name := "path:" + spec.Path
		st, err := a.guestProbe(ctx, rep, name, "sudo", "-n", "stat", "-c", "%F", spec.Path)
		if err != nil {
			return err
		}
		if st.ExitCode != 0 || strings.TrimSpace(string(st.Stdout)) != "directory" {
			return a.verifyFailed(rep, name, "not a directory", "create the persistent Hermes directory on the guest")
		}
		og, err := a.guestProbe(ctx, rep, name, "sudo", "-n", "stat", "-c", "%U:%G %a", spec.Path)
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
		if owner != spec.Owner || group != spec.Group {
			return a.verifyFailed(rep, name, fmt.Sprintf("owner:group %s:%s, want %s:%s", owner, group, spec.Owner, spec.Group), "fix directory ownership on the guest")
		}
		if !modeMatches(spec, mode) {
			return a.verifyFailed(rep, name, fmt.Sprintf("mode %s, want one of %v", mode, spec.Modes), "fix directory permissions on the guest")
		}
		fm, err := a.guestProbe(ctx, rep, name, "sudo", "-n", "findmnt", "-n", "-o", "FSTYPE,SOURCE", "-T", spec.Path)
		if err != nil {
			return err
		}
		fields := strings.Fields(string(fm.Stdout))
		if fm.ExitCode != 0 || len(fields) < 1 {
			return a.verifyFailed(rep, name, "could not resolve the backing filesystem", "verify the path exists on the guest")
		}
		fstype := fields[0]
		if !nativeFSTypes[fstype] {
			return a.verifyFailed(rep, name, "backed by non-native filesystem "+fstype, "Hermes state must live on the VM's Linux filesystem, not a host share (ADR-0002)")
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
		return a.verifyFailed(rep, name, "a macOS host-share mount is present", "remove the broad host mount; only a narrow transfer folder is allowed (ADR-0002)")
	default:
		// Anything else is not the evidence-backed PASS shape and cannot prove the
		// absence of a host mount: exit 127 (findmnt missing / exec failure), an
		// unexpected exit 0 with empty output, exit 1 with stray output, or any
		// other nonzero query failure. Fail closed.
		return a.verifyFailed(rep, name, fmt.Sprintf("could not reliably determine host-share mounts (exit %d)", res.ExitCode), "confirm findmnt is present on the guest and re-run bootstrap")
	}
}

// verifyOperatorShellHelper proves the guest side of `torio project shell`
// exists before bootstrap calls the target ready. The V1 headline flow ends in
// that helper and nothing but provisioning creates it, so a missing helper is a
// target that reports success and then fails at the remote end.
//
// The session the helper opens carries the operator's forwarded SSH agent, so
// the file itself is part of the boundary: anything that can rewrite it can
// borrow that agent. It must therefore be a regular file (a symlink would move
// the real content somewhere unowned), owned root:root, and writable by nobody
// else. `stat` does not dereference by default, so this reads the path itself.
func (a *Adapter) verifyOperatorShellHelper(ctx context.Context, rep *BootstrapReport, workspaceRoot string, reconcile bool) error {
	const name = "operator_shell_helper"
	st, err := a.guestProbe(ctx, rep, name, "stat", "-c", "%F", operatorShellHelperSpec.Path)
	if err != nil {
		return err
	}
	kind := strings.TrimSpace(string(st.Stdout))
	if st.ExitCode == 1 && kind == "" {
		absent, err := a.guestProbe(ctx, rep, name, "test", "!", "-e", operatorShellHelperSpec.Path)
		if err != nil {
			return err
		}
		if absent.ExitCode != 0 {
			return a.verifyFailed(rep, name, "could not prove the helper path is absent", operatorShellHelperRemediation)
		}
		if !reconcile {
			return a.verifyFailed(rep, name, "no operator shell helper at "+operatorShellHelperSpec.Path, operatorShellHelperRemediation)
		}
		content, err := projectHelper(embeddedProjectShell, workspaceRoot, "operator shell")
		if err != nil {
			return a.verifyFailed(rep, name, err.Error(), operatorShellHelperRemediation)
		}
		installed, err := a.SSHInput(ctx, content,
			[]string{"sudo", "-n", "/bin/bash", "-ceu", operatorShellInstallScript})
		if err != nil {
			return err
		}
		if installed.ExitCode != 0 || installed.StdoutTruncated || installed.StderrTruncated {
			return a.verifyFailed(rep, name, "could not install the missing operator shell helper", "confirm passwordless root provisioning is intact and re-run bootstrap")
		}
		rep.record(name+"_installed", true, "installed embedded helper atomically")
		st, err = a.guestProbe(ctx, rep, name, "stat", "-c", "%F", operatorShellHelperSpec.Path)
		if err != nil {
			return err
		}
	}
	return a.verifyRootHelperFile(ctx, rep, name, "operator shell helper", operatorShellHelperSpec, st,
		"a writable helper is a privilege-escalation path into a session that carries the operator's forwarded agent",
		operatorShellHelperRemediation)
}

// verifyRootHelperFile is the shared tail of the guest helper checks. Given the
// result of `stat -c %F` on spec.Path, it proves the path is a regular file
// (not a symlink pointing the real content somewhere unowned), owned root:root,
// writable by nobody else, and within spec's accepted modes. what names the
// helper in failure details, writableRisk says what a foreign-writable file
// would allow, and remediation is the single repair for every drift.
func (a *Adapter) verifyRootHelperFile(ctx context.Context, rep *BootstrapReport, name, what string, spec backend.PathSpec, st execx.Result, writableRisk, remediation string) error {
	kind := strings.TrimSpace(string(st.Stdout))
	if st.ExitCode != 0 || kind == "" {
		return a.verifyFailed(rep, name, "no "+what+" at "+spec.Path, remediation)
	}
	if kind != "regular file" {
		return a.verifyFailed(rep, name, fmt.Sprintf("helper is a %s, want a regular file", kind), remediation)
	}

	og, err := a.guestProbe(ctx, rep, name, "stat", "-c", "%U:%G %a", spec.Path)
	if err != nil {
		return err
	}
	if og.ExitCode != 0 {
		return a.verifyFailed(rep, name, "could not read helper ownership/mode", remediation)
	}
	owner, group, mode, ok := parseStatOwnership(string(og.Stdout))
	if !ok {
		return a.verifyFailed(rep, name, "unparseable helper ownership/mode", remediation)
	}
	if owner != spec.Owner || group != spec.Group {
		return a.verifyFailed(rep, name,
			fmt.Sprintf("helper owner:group %s:%s, want %s:%s", owner, group, spec.Owner, spec.Group),
			"a helper the operator or hermes owns can be rewritten between sessions; "+remediation)
	}
	writable, parsed := modeGrantsForeignWrite(mode)
	if !parsed {
		return a.verifyFailed(rep, name, "unparseable helper mode "+mode, remediation)
	}
	if writable {
		return a.verifyFailed(rep, name,
			fmt.Sprintf("helper mode %s is group- or world-writable", mode),
			writableRisk+"; "+remediation)
	}
	if !modeMatches(spec, mode) {
		return a.verifyFailed(rep, name, fmt.Sprintf("helper mode %s, want one of %v", mode, spec.Modes), remediation)
	}

	rep.record(name, true, fmt.Sprintf("%s:%s %s", owner, group, mode))
	return nil
}

// verifyProjectEnterHelper proves the ordinary workspace-session helper is the
// root-owned regular file provisioned by the Lima template. A helper absent
// from a VM created by an older Torio is installed from the current embedded
// bytes. Any existing but drifted path is reported and never overwritten.
func (a *Adapter) verifyProjectEnterHelper(ctx context.Context, rep *BootstrapReport, workspaceRoot string, reconcile bool) error {
	const name = "project_enter_helper"
	spec := projectEnterHelperSpec

	st, err := a.guestProbe(ctx, rep, name, "stat", "-c", "%F", spec.Path)
	if err != nil {
		return err
	}
	kind := strings.TrimSpace(string(st.Stdout))
	if st.ExitCode == 1 && kind == "" {
		absent, err := a.guestProbe(ctx, rep, name, "test", "!", "-e", spec.Path)
		if err != nil {
			return err
		}
		if absent.ExitCode != 0 {
			return a.verifyFailed(rep, name, "could not prove the helper path is absent", projectEnterHelperRemediation)
		}
		if !reconcile {
			return a.verifyFailed(rep, name, "no project enter helper at "+spec.Path, projectEnterHelperRemediation)
		}
		// Resolved for the backend that will run it, exactly as the template
		// does. Installing the raw embedded bytes here would put a helper on the
		// guest carrying an unsubstituted placeholder, which refuses every
		// project rather than the wrong ones.
		content, err := projectHelper(embeddedProjectEnter, workspaceRoot, "project enter")
		if err != nil {
			return a.verifyFailed(rep, name, err.Error(), projectEnterHelperRemediation)
		}
		installed, err := a.SSHInput(ctx, content,
			[]string{"sudo", "-n", "/bin/bash", "-ceu", projectEnterInstallScript})
		if err != nil {
			return err
		}
		if installed.ExitCode != 0 || installed.StdoutTruncated || installed.StderrTruncated {
			return a.verifyFailed(rep, name, "could not install the missing project enter helper", "confirm passwordless root provisioning is intact and re-run bootstrap")
		}
		rep.record(name+"_installed", true, "installed embedded helper atomically")
		st, err = a.guestProbe(ctx, rep, name, "stat", "-c", "%F", spec.Path)
		if err != nil {
			return err
		}
	}
	return a.verifyRootHelperFile(ctx, rep, name, "project enter helper", spec, st,
		"a writable helper could replace the command run in an operator-controlled workspace session",
		projectEnterHelperRemediation)
}

// verifyAgentSessionHelper proves the guest entry point of the backend's own
// interactive session. A backend that declares no session has nothing here to
// check, and bootstrap records nothing rather than a check that passed
// vacuously: an operator reading the report must not be able to mistake "there
// is no such helper" for "the helper is fine".
//
// The helper runs as the operator and drops to the backend identity, so it is
// part of the boundary in the same way the shell helper is: root-owned, a
// regular file, writable by nobody else. It is installed from the embedded
// bytes when absent and reported, never overwritten, when it has drifted.
func (a *Adapter) verifyAgentSessionHelper(ctx context.Context, rep *BootstrapReport, session *backend.SessionSpec, reconcile bool) error {
	if session == nil {
		return nil
	}
	if err := a.verifySessionHelper(ctx, rep, "agent_session_helper", "agent session helper",
		session.HelperPath, session.Helper, reconcile); err != nil {
		return err
	}
	// A backend that declares no push-capable session has nothing to check, and
	// bootstrap records nothing rather than a check that passed vacuously.
	if session.PushHelperPath == "" {
		return nil
	}
	return a.verifySessionHelper(ctx, rep, "agent_push_session_helper", "agent push session helper",
		session.PushHelperPath, session.PushHelper, reconcile)
}

// verifySessionHelper is the shared body: both entry points are root-owned
// regular files nobody else may rewrite, installed from embedded bytes when
// absent and reported, never overwritten, when they have drifted.
func (a *Adapter) verifySessionHelper(ctx context.Context, rep *BootstrapReport, name, description, helperPath string, content []byte, reconcile bool) error {
	if err := validateRootHelperPath(helperPath); err != nil {
		return a.verifyFailed(rep, name,
			"backend declares an unsafe "+description+" path",
			"fix the backend to declare an absolute canonical path made of plain filename components")
	}
	spec := backend.PathSpec{
		Path:  helperPath,
		Owner: "root",
		Group: "root",
		Modes: []string{"755", "0755"},
	}
	remediation := "re-run `torio vm bootstrap` so it reinstalls " + spec.Path + " as root:root 0755"

	st, err := a.guestProbe(ctx, rep, name, "stat", "-c", "%F", spec.Path)
	if err != nil {
		return err
	}
	kind := strings.TrimSpace(string(st.Stdout))
	if st.ExitCode == 1 && kind == "" {
		absent, err := a.guestProbe(ctx, rep, name, "test", "!", "-e", spec.Path)
		if err != nil {
			return err
		}
		if absent.ExitCode != 0 {
			return a.verifyFailed(rep, name, "could not prove the helper path is absent", remediation)
		}
		if !reconcile {
			return a.verifyFailed(rep, name, "no "+description+" at "+spec.Path, remediation)
		}
		installed, err := a.SSHInput(ctx, content,
			[]string{"sudo", "-n", "/bin/bash", "-ceu", rootHelperInstallScript(spec.Path)})
		if err != nil {
			return err
		}
		if installed.ExitCode != 0 || installed.StdoutTruncated || installed.StderrTruncated {
			return a.verifyFailed(rep, name, "could not install the missing "+description, "confirm passwordless root provisioning is intact and re-run bootstrap")
		}
		rep.record(name+"_installed", true, "installed embedded helper atomically")
		st, err = a.guestProbe(ctx, rep, name, "stat", "-c", "%F", spec.Path)
		if err != nil {
			return err
		}
	}
	return a.verifyRootHelperFile(ctx, rep, name, description, spec, st,
		"a writable helper could replace the command an operator opens an agent session with",
		remediation)
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

// modeGrantsForeignWrite reports whether a `stat -c %a` mode lets anyone but the
// owner write, and whether the mode could be read at all. An unreadable mode is
// not proof of anything and its caller fails closed.
func modeGrantsForeignWrite(mode string) (writable, parsed bool) {
	bits, err := strconv.ParseUint(strings.TrimSpace(mode), 8, 32)
	if err != nil {
		return false, false
	}
	return bits&0o022 != 0, true
}

// modeMatches reports whether the observed stat mode satisfies the path spec.
//
// An exact match always passes. A spec may additionally accept a mode that
// grants strictly less, because two of the required directories are tightened
// by their own owner in normal use: Hermes chmods /home/hermes/.hermes to 0700
// the first time it writes provider credentials there. Bootstrap accepted 0750
// and nothing else, so the first ordinary use of the product left every machine
// permanently unbootstrapped — `brain init`, `project add` and every other
// verified command failed closed, on a guest where nothing was wrong.
//
// Only the hermes-private directories opt in. The group bit they surrender
// belongs to the hermes group, whose sole member is hermes. Everywhere else the
// granted permission is load-bearing for somebody other than the owner — the
// operator traverses HermesHome and writes under HermesWorkspacePath through
// torio-projects, and the operator shell helper must stay executable by all —
// so there a stricter mode is drift and still fails.
//
// Owner bits must still match exactly. "Stricter" means withholding access from
// others, never the owner locking itself out.
func modeMatches(spec backend.PathSpec, mode string) bool {
	for _, want := range spec.Modes {
		if mode == want {
			return true
		}
	}
	if !spec.AllowStricter {
		return false
	}
	got, err := strconv.ParseUint(mode, 8, 32)
	if err != nil {
		return false
	}
	for _, want := range spec.Modes {
		w, err := strconv.ParseUint(want, 8, 32)
		if err != nil {
			continue
		}
		if got&0o700 == w&0o700 && got&^w == 0 {
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
