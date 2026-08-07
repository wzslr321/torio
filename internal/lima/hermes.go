package lima

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/execx"
)

// Hermes is the Hermes Agent backend: the one Torio was built around, and now
// one implementation of a contract rather than the contract itself.
//
// It is a service backend. It installs from a pinned upstream commit, runs as a
// persistent user systemd unit bound to guest loopback, answers an
// unauthenticated readiness endpoint, and keeps its own project registry that
// Torio drives through a CLI. It declares no interactive session command: an
// operator reaches it through the service, not through a terminal.
//
// The implementation lives beside the guest transport rather than in its own
// package because the guest layout it names — the identity, the profile, the
// vault, the workspace — is still the layout the MCP custody checks and the
// session-path validation in this package are written against. Relocating those
// is follow-up work; the contract is what makes the backend replaceable, and
// that does not depend on which directory the implementation sits in.
func Hermes() backend.Backend { return hermesBackend{} }

type hermesBackend struct{}

func (hermesBackend) Identity() backend.Identity {
	return backend.Identity{
		Name:          "hermes",
		GuestUser:     HermesUser,
		Home:          HermesHome,
		ProfilePath:   HermesProfilePath,
		BrainPath:     HermesBrainPath,
		WorkspacePath: HermesWorkspacePath,
	}
}

func (hermesBackend) RequiredPaths() []backend.PathSpec { return bootstrapRequiredPaths }

// VerifyIdentity proves the dedicated non-root service user exists.
func (hermesBackend) VerifyIdentity(ctx context.Context, r backend.StepRunner) error {
	const name = "hermes_user"
	res, err := r.Probe(ctx, name, "id", "-u", HermesUser)
	if err != nil {
		return err
	}
	uid := strings.TrimSpace(string(res.Stdout))
	if res.ExitCode != 0 || uid == "" {
		return r.Fail(name, "hermes user not found", "provision the hermes service user on the guest")
	}
	r.Record(name, true, "uid="+uid)
	return nil
}

// VerifyMembership proves hermes can reach the shared workspace.
func (hermesBackend) VerifyMembership(ctx context.Context, r backend.StepRunner) error {
	const name = "hermes_torio_projects"
	res, err := r.Probe(ctx, name, "id", "-nG", HermesUser)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return r.Fail(name, "cannot read hermes group membership", "confirm the hermes user exists on the guest")
	}
	if !hasGroup(string(res.Stdout), TorioProjectsGroup) {
		return r.Fail(name, "hermes is not in torio-projects", "add hermes to the torio-projects group on the guest")
	}
	r.Record(name, true, "member")
	return nil
}

// VerifyIsolation proves hermes holds no authority beyond its own work.
func (hermesBackend) VerifyIsolation(ctx context.Context, r backend.StepRunner) error {
	const name = "hermes_not_in_docker"
	res, err := r.Probe(ctx, name, "id", "-nG", HermesUser)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return r.Fail(name, "cannot read hermes group membership", "confirm the hermes user exists on the guest")
	}
	if hasGroup(string(res.Stdout), dockerGroup) {
		return r.Fail(name, "hermes is in the docker group", "remove hermes from the docker group; rootful Docker for hermes is forbidden (ADR-0003)")
	}
	r.Record(name, true, "not a member")
	return nil
}

// Install reconciles the Gate-0-pinned Hermes Agent tree at hermesAgentDir with
// HEAD == PromotedHermesCommit and an executable launcher, then reconciles the
// PATH shim. When the launcher is already present, only the git pin is
// verified. When it is missing, it runs apt-get deps, downloads install.sh to a
// hermes-writable path (never curl|bash pipe), runs it with fixed flags,
// removes the script, and verifies launcher + commit. install.sh content is not
// checksum-pinned; the verifiable postcondition is git HEAD and launcher path.
func (b hermesBackend) Install(ctx context.Context, r backend.StepRunner) error {
	if err := b.reconcileInstall(ctx, r); err != nil {
		return err
	}
	return b.reconcileShim(ctx, r)
}

func (b hermesBackend) reconcileInstall(ctx context.Context, r backend.StepRunner) error {
	const name = "hermes_install"
	present, err := r.Probe(ctx, name, "sudo", "-n", "test", "-x", hermesTarget)
	if err != nil {
		return err
	}
	if present.ExitCode == 0 {
		return b.verifyGitPin(ctx, r, name)
	}

	if err := b.installDeps(ctx, r, name); err != nil {
		return err
	}
	dl, err := r.Probe(ctx, name,
		"sudo", "-n", "-u", HermesUser, "--",
		"curl", "-fsSL", "-o", hermesInstallScriptPath, hermesInstallScriptURL,
	)
	if err != nil {
		return err
	}
	if dl.ExitCode != 0 {
		return r.Fail(name, "could not download hermes install script", "check guest network and curl")
	}
	run, err := r.Probe(ctx, name,
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
		return r.Fail(name, "hermes install script exited non-zero", "inspect the install script output on the guest")
	}
	rm, err := r.Probe(ctx, name, "sudo", "-n", "rm", "-f", hermesInstallScriptPath)
	if err != nil {
		return err
	}
	if rm.ExitCode != 0 {
		return r.Fail(name, "could not remove downloaded install script", "remove "+hermesInstallScriptPath+" on the guest")
	}
	execOK, err := r.Probe(ctx, name, "sudo", "-n", "test", "-x", hermesTarget)
	if err != nil {
		return err
	}
	if execOK.ExitCode != 0 {
		return r.Fail(name, "launcher not executable after install", "re-run bootstrap or inspect the hermes install on the guest")
	}
	return b.verifyGitPin(ctx, r, name)
}

func (hermesBackend) installDeps(ctx context.Context, r backend.StepRunner, name string) error {
	upd, err := r.Probe(ctx, name, "sudo", "-n", "apt-get", "update", "-y")
	if err != nil {
		return err
	}
	if upd.ExitCode != 0 {
		return r.Fail(name, "apt-get update failed", "fix guest apt sources and re-run bootstrap")
	}
	inst, err := r.Probe(ctx, name,
		append([]string{"sudo", "-n", "apt-get", "install", "-y", "--no-install-recommends"}, hermesBuildDeps...)...,
	)
	if err != nil {
		return err
	}
	if inst.ExitCode != 0 {
		return r.Fail(name, "apt-get install of hermes build deps failed", "fix guest apt and re-run bootstrap")
	}
	return nil
}

func (hermesBackend) verifyGitPin(ctx context.Context, r backend.StepRunner, name string) error {
	head, err := r.Probe(ctx, name,
		"sudo", "-n", "-u", HermesUser, "--",
		"git", "-C", hermesAgentDir, "rev-parse", "HEAD",
	)
	if err != nil {
		return err
	}
	observed := strings.TrimSpace(string(head.Stdout))
	if head.ExitCode != 0 || observed == "" {
		return r.Fail(name, "could not read hermes agent git HEAD", "confirm the install at "+hermesAgentDir)
	}
	if observed != PromotedHermesCommit {
		return r.Fail(name,
			fmt.Sprintf("hermes agent commit %q != pinned %q", observed, PromotedHermesCommit),
			"reconcile the pinned hermes install; do not paper over commit drift")
	}
	r.Record(name, true, "commit="+PromotedHermesCommit)
	return nil
}

func (hermesBackend) reconcileShim(ctx context.Context, r backend.StepRunner) error {
	const name = "hermes_shim"
	// Never create a dangling shim: confirm the pinned launcher exists first. A
	// missing launcher is drift, not something to repair.
	present, err := r.Probe(ctx, name, "sudo", "-n", "test", "-x", hermesTarget)
	if err != nil {
		return err
	}
	if present.ExitCode != 0 {
		return r.Fail(name, "pinned hermes launcher not found at "+hermesTarget, "the hermes install drifted; re-provision the agent before bootstrap")
	}
	link, err := r.Probe(ctx, name, "readlink", hermesShimPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(link.Stdout)) == hermesTarget {
		r.Record(name, true, "shim already correct")
		return nil
	}
	ln, err := r.Probe(ctx, name, "sudo", "-n", "ln", "-sfn", hermesTarget, hermesShimPath)
	if err != nil {
		return err
	}
	if ln.ExitCode != 0 {
		return r.Fail(name, "could not install the hermes shim", "check write access to "+hermesShimPath)
	}
	r.Record(name, true, "shim installed")
	return nil
}

// VerifyVersion proves the documented stable command path answers: as the
// hermes user, via the bare `hermes` name resolved by the shim on sudo's
// secure_path.
func (hermesBackend) VerifyVersion(ctx context.Context, r backend.StepRunner) error {
	const name = "hermes_version"
	res, err := r.Probe(ctx, name, "sudo", "-n", "-u", HermesUser, "--", "hermes", "--version")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return r.Fail(name, "`hermes --version` exited non-zero", "confirm the hermes shim and install on the guest")
	}
	version, okv := parseHermesVersion(string(res.Stdout))
	if !okv {
		return r.Fail(name, "`hermes --version` produced no recognizable version", "a clean exit is not proof; inspect the hermes install")
	}
	if pinned := r.PinnedVersion(); pinned != "" && version != pinned {
		return r.Fail(name, fmt.Sprintf("hermes version %q, pinned %q", version, pinned), "version drift: reconcile the pinned hermes install, do not paper over")
	}
	r.Record(name, true, version)
	return nil
}

// VerifyGuardrails has nothing to check. Hermes' own behaviour is shaped by
// files it owns and can rewrite — hooks, and the MCP server list in its
// config.yaml. Those are checked where the custody boundary they belong to is
// checked (`torio mcp status`), and they are drift detectors there, not
// boundaries. Restating them here would suggest bootstrap enforces something it
// does not.
func (hermesBackend) VerifyGuardrails(context.Context, backend.StepRunner) error { return nil }

// ProbeAuth has nothing to report. Hermes takes its provider credential
// interactively, in its own profile, and Torio has never had a way to observe
// that offline which is worth putting in a report.
func (hermesBackend) ProbeAuth(context.Context, backend.StepRunner) error { return nil }

func (hermesBackend) Registry() backend.ProjectRegistry { return hermesRegistry{} }

// Session is nil: Hermes' interactive surface is the service, reached by a
// client over the loopback endpoint an operator forwards. There is no terminal
// command to open in a checkout.
func (hermesBackend) Session() *backend.SessionSpec { return nil }

func (hermesBackend) Service() *backend.ServiceSpec {
	return &backend.ServiceSpec{
		UnitName:   HermesUnitName,
		UnitDir:    HermesHome + "/.config/systemd/user",
		RenderUnit: renderHermesUnit,
		BindHost:   HermesBindHost,
		BindPort:   HermesBindPort,
		StatusPath: HermesStatusPath,
		ParseReady: parseHermesStatusVersion,
	}
}

// The fixed, repository-controlled service facts. Loopback bind is a hard
// invariant (docs/contracts/cli.md); the values are constants — never caller
// input — so the generated unit and every probe target a single, auditable
// loopback endpoint and can never be widened to a public bind.
const (
	// HermesUnitName is the custom user unit Torio owns for the backend.
	HermesUnitName = "hermes-serve.service"
	// HermesBindHost/HermesBindPort are the discovered `hermes serve` loopback
	// defaults. The unit pins them explicitly so the bind cannot drift.
	HermesBindHost = "127.0.0.1"
	HermesBindPort = 9119
	// HermesStatusPath is the unauthenticated readiness endpoint (verified: 200
	// with a JSON version; /api/health|info|version all answer 401).
	HermesStatusPath = "/api/status"
)

// HermesBrainSkillName is the retrieval skill the Brain installs into the
// Hermes profile. It is named here because the environment hint below has to
// name it too, and the two must not be able to drift apart.
const HermesBrainSkillName = "torio-brain"

// HermesEnvironmentHint is handed to the backend as HERMES_ENVIRONMENT_HINT, an
// explicit seam Hermes offers a host that wraps it: the text is appended to
// the stable system prompt of every session, uncapped, without forking the
// identity slot. Torio sets it on the user unit it already generates, so no
// file the operator owns is edited to deliver it.
//
// It exists because the skill index alone cannot be relied on. A hint is
// read whichever skill the model picks, and whether it picks one at all —
// so the vault path and the no-bulk-read rule stop depending on a routing
// contest against a bundled skill that recommends listing every note.
//
// This is a prompt instruction and nothing more. It does not enforce the
// rule: the agent runs as the same user that owns the vault, so no
// permission stops a bulk read. Do not describe it to an operator as a
// guarantee.
//
// Constraints from the transport: one line, and free of `$`, `%` and `"`,
// which systemd would expand or terminate the quoted value on.
const HermesEnvironmentHint = "This machine is managed by Torio. The user's private notes are one Markdown vault at " +
	HermesBrainPath + "; there is no other vault, and no vault path to resolve from an environment variable " +
	"or a fallback location. Read it with the " + HermesBrainSkillName + " skill: search for the few notes " +
	"that answer the question, then read those. Never list or read the vault in bulk."

// renderHermesUnit produces the exact bytes of the custom user systemd unit. It
// is deterministic and derived entirely from the pinned constants, so the
// loopback bind, the HERMES_HOME profile pin, and the restart policy are
// enforced by code (and locked by a golden test), never by hand.
//
// Environment=HERMES_HOME pins the existing /home/hermes/.hermes profile
// (hermes_constants.get_hermes_home reads $HERMES_HOME), and --skip-build is
// what lets the backend start without an npm build step in a non-interactive
// service.
func renderHermesUnit() []byte {
	execStart := hermesShimPath + " serve --skip-build --host " + HermesBindHost + " --port " + strconv.Itoa(HermesBindPort)
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=Torio loopback backend (torio serve)\n")
	b.WriteString("\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	b.WriteString("WorkingDirectory=" + hermesAgentDir + "\n")
	b.WriteString("Environment=HERMES_HOME=" + HermesProfilePath + "\n")
	b.WriteString("Environment=\"HERMES_ENVIRONMENT_HINT=" + HermesEnvironmentHint + "\"\n")
	b.WriteString("ExecStart=" + execStart + "\n")
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=2\n")
	b.WriteString("\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return []byte(b.String())
}

// parseHermesStatusVersion extracts the top-level "version" from the
// /api/status JSON. A parseable version proves the readiness endpoint answered
// with real content, not merely a socket accept.
func parseHermesStatusVersion(body []byte) (string, bool) {
	var s struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(body), &s); err != nil {
		return "", false
	}
	if s.Version == "" {
		return "", false
	}
	return s.Version, true
}

// hermesRegistry drives `hermes project …` on the guest. Every decision comes
// from stdout, never from an exit code: see status for why neither direction of
// that exit code has ever been evidence.
type hermesRegistry struct{}

func (hermesRegistry) argv(args ...string) []string {
	return append([]string{"sudo", "-n", "-u", HermesUser, "--", "hermes", "project"}, args...)
}

// run executes a registry command, failing closed on truncated output. A clean
// non-zero exit is not an error here — the caller interprets it.
func (r hermesRegistry) run(ctx context.Context, t backend.Transport, args ...string) (execx.Result, error) {
	res, err := t.SSH(ctx, r.argv(args...))
	if err != nil {
		return execx.Result{}, err
	}
	if res.StdoutTruncated || res.StderrTruncated {
		return execx.Result{}, &backend.RegistryError{Malformed: true, Err: errors.New("bounded guest output was truncated")}
	}
	return res, nil
}

// mustRun executes a registry command that has to succeed.
func (r hermesRegistry) mustRun(ctx context.Context, t backend.Transport, action string, args ...string) error {
	res, err := r.run(ctx, t, args...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return &backend.RegistryError{Err: fmt.Errorf("%s exited %d", action, res.ExitCode)}
	}
	return nil
}

// Status reads the Hermes project state for id from `show` stdout.
//
// The exit code of `show` is not evidence in either direction, and the two
// directions failed at different times.
//
// Hermes 0.19.0 exited 0 for an unknown project, writing only a stderr
// diagnostic, because upstream discarded the handler's return value. So a clean
// exit never meant the project existed. Hermes 0.19.1 fixed that and now exits
// non-zero — which broke the other half of the original reading, where a
// non-zero exit was taken to mean the CLI itself was broken. On 0.19.1 the most
// ordinary case in the product, adding the first project to a fresh VM, fails
// closed on a guest that is working perfectly.
//
// So neither exit code answers "does this project exist?", and the answer has
// to come from somewhere that is not an exit code:
//
//   - `show` printed a block — the project exists; parse it.
//   - `show` printed nothing and `list` does not name the slug — the slug is
//     free, whatever `show` exited with.
//   - `show` printed nothing and `list` does name the slug — the project exists
//     but could not be described. `show` is the only source of the primary
//     path, so this is unverifiable state and fails closed.
//   - `list` itself failed — the CLI is broken or absent. Fails closed.
//
// `list` is still never a source of *state*: its output carries slugs and
// names, never a path. It is used here only to answer an existence question,
// which is exactly what a list of slugs can answer.
func (r hermesRegistry) Status(ctx context.Context, t backend.Transport, id, workspace string) (backend.RegistryStatus, error) {
	var st backend.RegistryStatus
	show, err := r.run(ctx, t, "show", id)
	if err != nil {
		return st, err
	}
	if strings.TrimSpace(string(show.Stdout)) == "" {
		list, listErr := r.run(ctx, t, "list")
		if listErr != nil {
			return st, listErr
		}
		if list.ExitCode != 0 {
			return st, &backend.RegistryError{Err: errors.New("the Hermes project CLI is unavailable on the guest")}
		}
		if hermesProjectListed(string(list.Stdout), id) {
			return st, &backend.RegistryError{Err: fmt.Errorf("inspect the Hermes project exited %d", show.ExitCode)}
		}
		return st, nil
	}
	st, err = parseHermesProjectShow(string(show.Stdout), id, workspace)
	if err != nil {
		return backend.RegistryStatus{}, &backend.RegistryError{Malformed: true, Err: err}
	}
	return st, nil
}

func (r hermesRegistry) Create(ctx context.Context, t backend.Transport, id, displayName, workspace string) error {
	// Creating is safe only because `show` just proved the slug free: on a taken
	// slug the CLI silently creates `<slug>-2` instead of failing.
	return r.mustRun(ctx, t, "register the Hermes project", "create", displayName, workspace, "--slug", id)
}

func (r hermesRegistry) Restore(ctx context.Context, t backend.Transport, id string) error {
	return r.mustRun(ctx, t, "restore the archived Hermes project", "restore", id)
}

func (r hermesRegistry) Archive(ctx context.Context, t backend.Transport, id string) error {
	return r.mustRun(ctx, t, "archive the Hermes project", "archive", id)
}

// Activate sets the active Hermes project and confirms it from the printed
// line. `hermes project use` cannot report failure through its exit code.
func (r hermesRegistry) Activate(ctx context.Context, t backend.Transport, id string) error {
	res, err := r.run(ctx, t, "use", id)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return &backend.RegistryError{Err: fmt.Errorf("activate the Hermes project exited %d", res.ExitCode)}
	}
	if strings.TrimSpace(string(res.Stdout)) != "Active project: "+id {
		return &backend.RegistryError{Err: fmt.Errorf("`hermes project use` did not confirm %q as the active project", id)}
	}
	return nil
}

// hermesProjectListed reports whether `hermes project list` names slug.
//
// The listing prints one project per line with the slug first, and prints a
// "No projects yet." sentence when there are none. Matching the first field
// rather than searching the whole line is deliberate: a project *named* after
// another project's slug must not answer for it, and a substring search would
// let it.
//
// A slug cannot contain whitespace (it is the project-ID rule: lowercase
// alphanumerics and hyphens), so the first whitespace-separated field is the
// whole slug or nothing.
func hermesProjectListed(out, slug string) bool {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == slug {
			return true
		}
	}
	return false
}

// parseHermesProjectShow reads the block `hermes project show` prints: a
// `<slug>  [<id>]` header carrying an ` (archived)` flag, and exactly one
// `primary:` line. Anything else is unrecognized output, which is unverifiable
// state and fails closed.
func parseHermesProjectShow(out, id, workspace string) (backend.RegistryStatus, error) {
	var st backend.RegistryStatus
	lines := strings.Split(out, "\n")

	header := ""
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			header = strings.TrimSpace(line)
			break
		}
	}
	if !strings.HasPrefix(header, id+" ") {
		return st, errors.New("`hermes project show` described a different project")
	}
	st.Present = true
	st.Archived = strings.HasSuffix(header, "(archived)")

	primary := ""
	seen := 0
	for _, line := range lines {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "primary:")
		if !ok {
			continue
		}
		seen++
		primary = strings.TrimSpace(value)
	}
	if seen != 1 {
		return st, errors.New("`hermes project show` did not report exactly one primary path")
	}
	st.PrimaryMatches = primary == workspace
	return st, nil
}
