package brain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/guestexec"
	"github.com/wzslr321/torio/internal/lima"
)

// Guest is the narrow, typed VM boundary used by the Brain manager.
type Guest interface {
	Bootstrap(ctx context.Context, opts lima.BootstrapOptions) (lima.BootstrapReport, error)
	SSH(ctx context.Context, command []string) (execx.Result, error)
	SSHInput(ctx context.Context, stdin []byte, command []string) (execx.Result, error)
	CopyToGuest(ctx context.Context, hostSourceDir, guestDestinationDir, guestHome string) error
}

var _ Guest = (*lima.Adapter)(nil)

// Manager owns initialization and verification of the canonical Brain.
type Manager struct {
	guest         Guest
	bootstrapOpts lima.BootstrapOptions
}

func New(guest Guest, opts lima.BootstrapOptions) *Manager {
	return &Manager{guest: guest, bootstrapOpts: opts}
}

// backend is the agent backend this instance runs, arriving with the bootstrap
// options the Brain is already bounded by.
func (m *Manager) backend() backend.Backend {
	if m.bootstrapOpts.Backend == nil {
		return lima.Hermes()
	}
	return m.bootstrapOpts.Backend
}

// The vault and its working paths belong to the backend identity that owns
// them. Every one of them is derived rather than fixed, because a second
// backend keeps its vault in its own home and a staging directory that landed
// in the wrong one would be a directory the owning identity cannot write.
func (m *Manager) identity() backend.Identity { return m.backend().Identity() }
func (m *Manager) agentUser() string          { return m.identity().GuestUser }
func (m *Manager) vault() string              { return m.identity().BrainPath }
func (m *Manager) stagingPath() string        { return m.identity().Home + "/.torio-brain-staging" }
func (m *Manager) skillStagingPath() string   { return m.identity().Home + "/.torio-brain-skill-staging" }
func (m *Manager) lockPath() string           { return m.identity().Home + "/.torio-brain-init.lock" }

// The retrieval skill's guest paths, derived from where the backend actually
// discovers skills. A backend with no skill root gets no skill installed, and
// status says the vault has no retrieval surface rather than reporting one as
// missing.
func (m *Manager) skillRoot() string { return m.backend().BrainSkill().Root }

func (m *Manager) skillCategoryPath() string {
	sk := m.backend().BrainSkill()
	if sk.Category == "" {
		return sk.Root
	}
	return sk.Root + "/" + sk.Category
}

// skillCategoryFilePath is the category description, empty when the backend
// needs no category. Only a backend whose skill index truncates descriptions
// has a reason for one.
func (m *Manager) skillCategoryFilePath() string {
	if m.backend().BrainSkill().Category == "" {
		return ""
	}
	return m.skillCategoryPath() + "/DESCRIPTION.md"
}

func (m *Manager) skillPath() string     { return m.skillCategoryPath() + "/" + SkillName }
func (m *Manager) skillFilePath() string { return m.skillPath() + "/SKILL.md" }

// legacySkillPath is where releases before the category move installed the
// skill. It is empty for a backend with no category, which never had one.
func (m *Manager) legacySkillPath() string {
	if m.backend().BrainSkill().Category == "" {
		return ""
	}
	return m.skillRoot() + "/" + SkillName
}

// Init creates a fresh scaffold through a private sibling staging directory,
// commits it locally, promotes it, and registers the separate Hermes Project.
// Existing managed state is verified and never overwritten.
func (m *Manager) Init(ctx context.Context) (report InitReport, retErr error) {
	const op = "init"
	if err := m.requireBootstrapVerified(ctx, op); err != nil {
		return report, err
	}
	if err := m.requireRootAccess(ctx, op); err != nil {
		return report, err
	}
	lockToken, err := m.acquireInitLock(ctx, op)
	if err != nil {
		return report, err
	}
	promoted := false
	defer func() {
		if !promoted {
			_, _ = m.run(ctx, op, guestexec.RootExec("rm", "-rf", "--", m.stagingPath()))
		}
		if err := m.releaseInitLock(ctx, op, lockToken); retErr == nil && err != nil {
			retErr = err
		}
	}()

	status, err := m.inspectStatus(ctx, op)
	if err != nil {
		return report, err
	}
	if !status.PathExists {
		if err := m.mustRun(ctx, op, KindGuestCommand, "create canonical Brain directory",
			guestexec.RootExec("install", "-d", "-o", m.agentUser(), "-g", m.agentUser(), "-m", "0750", m.vault())); err != nil {
			return report, err
		}
		status, err = m.inspectStatus(ctx, op)
		if err != nil {
			return report, err
		}
	}

	switch status.State {
	case StateInitialized:
		report.Status = status
		return report, m.activateRetrieval(ctx, op, &report)
	case StateDrift:
		// A fully promoted, secure scaffold with only missing/conflicting Hermes
		// registration is safe to resume. Every other drift is a no-adopt
		// conflict.
		if status.ManagedScaffold && status.PathSecure && status.NativeFilesystem {
			if err := m.ensureProject(ctx, op); err != nil {
				report.Status = status
				return report, err
			}
			final, err := m.inspectStatus(ctx, op)
			report.Status = final
			if err != nil {
				return report, err
			}
			if final.State != StateInitialized {
				return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("managed Brain remains in drift after project registration")}
			}
			return report, m.activateRetrieval(ctx, op, &report)
		}
		return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("canonical Brain directory is non-empty or has unsafe drift; refusing to adopt or overwrite it")}
	case StateUninitialized:
	default:
		return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("unrecognized Brain state")}
	}

	if err := m.refreshInitLock(ctx, op, lockToken); err != nil {
		return report, err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "clean private staging",
		guestexec.RootExec("rm", "-rf", "--", m.stagingPath())); err != nil {
		return report, err
	}

	dirs := []string{m.stagingPath()}
	for _, name := range canonicalDirectories {
		dirs = append(dirs, m.stagingPath()+"/"+name)
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "create private scaffold staging",
		guestexec.RootExec(append([]string{"install", "-d", "-o", m.agentUser(), "-g", m.agentUser(), "-m", "0750"}, dirs...)...)); err != nil {
		return report, err
	}

	for _, name := range canonicalFiles {
		payload, readErr := scaffoldFS.ReadFile("templates/" + name)
		if readErr != nil {
			return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("embedded scaffold unavailable")}
		}
		if err := m.mustRunInput(ctx, op, KindGuestCommand, "write scaffold file", payload,
			guestexec.UserExecAs(m.agentUser(), "tee", m.stagingPath()+"/"+name)); err != nil {
			return report, err
		}
		if err := m.mustRun(ctx, op, KindGuestCommand, "set scaffold file permissions",
			guestexec.RootExec("chmod", "0640", m.stagingPath()+"/"+name)); err != nil {
			return report, err
		}
	}

	if err := m.mustRun(ctx, op, KindGit, "git init",
		guestexec.UserExecAs(m.agentUser(), "git", "-C", m.stagingPath(), "init", "--initial-branch=main")); err != nil {
		return report, err
	}
	if err := m.mustRun(ctx, op, KindGit, "git add",
		guestexec.UserExecAs(m.agentUser(), "git", "-C", m.stagingPath(), "add", "--", "README.md", "AGENTS.md", "todo.md")); err != nil {
		return report, err
	}
	if err := m.mustRun(ctx, op, KindGit, "git commit",
		guestexec.UserExecAs(m.agentUser(), "git", "-C", m.stagingPath(),
			"-c", "user.name=torio",
			"-c", "user.email=torio@localhost",
			"commit", "-m", "Initialize Torio Second Brain")); err != nil {
		return report, err
	}
	if err := m.verifySnapshotRepository(ctx, op, m.stagingPath(), "staged Brain repository"); err != nil {
		return report, err
	}
	if err := m.refreshInitLock(ctx, op, lockToken); err != nil {
		return report, err
	}

	if err := m.mustRun(ctx, op, KindConflict, "remove verified empty canonical directory",
		guestexec.RootExec("rmdir", m.vault())); err != nil {
		return report, err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "promote Brain scaffold",
		guestexec.RootExec("mv", "-T", m.stagingPath(), m.vault())); err != nil {
		return report, err
	}
	promoted = true
	report.Created = true

	if err := m.ensureProject(ctx, op); err != nil {
		status, _ := m.inspectStatus(ctx, op)
		report.Status = status
		return report, err
	}
	final, err := m.inspectStatus(ctx, op)
	report.Status = final
	if err != nil {
		return report, err
	}
	if final.State != StateInitialized {
		return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("promoted Brain did not satisfy initialized postconditions")}
	}
	return report, m.activateRetrieval(ctx, op, &report)
}

// activateRetrieval installs or refreshes the global torio-brain skill. It runs
// only from a success path of Init, once the Brain satisfies every initialized
// postcondition: a skill that points every session at a partial or unverified
// vault is worse than no skill at all.
func (m *Manager) activateRetrieval(ctx context.Context, op string, report *InitReport) error {
	if report.Status.State != StateInitialized {
		return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("refusing to install the retrieval skill for a Brain that is not fully initialized")}
	}
	updated, err := m.installSkill(ctx, op)
	if err != nil {
		return err
	}
	report.SkillUpdated = updated
	if m.backend().BrainSkill().Installable() {
		report.Status.SkillState = SkillInstalled
	}
	// The status this report carries was taken before the repair. Leaving the
	// drift issue on it prints `skill: installed` and `issues:
	// retrieval_skill_drift` in the same block, which reads as a failure to an
	// operator who just watched the repair succeed. Clear what was fixed rather
	// than re-inspecting the guest: installSkill already verified the payload
	// against its digest before returning.
	report.Status.Issues = slices.DeleteFunc(report.Status.Issues, func(issue string) bool {
		return issue == issueSkillDrift
	})
	return nil
}

// installSkill makes the retrieval skill and its category description match the
// embedded payloads. It is content-addressed: an already-current, correctly
// owned pair is left untouched so a rerun is a no-op, and any other state is
// rewritten atomically from a staging file outside the skill discovery root.
// A pre-category installation is retired first — see removeLegacySkill.
func (m *Manager) installSkill(ctx context.Context, op string) (updated bool, retErr error) {
	skill := m.backend().BrainSkill()
	if !skill.Installable() {
		// The backend discovers no skills Torio can install into, or ships no
		// skill written for it. The vault is still a vault — it is a
		// git-versioned directory the agent can read — and pretending to install
		// a retrieval surface it will never load would be worse than saying so.
		return false, nil
	}
	payload, digest, err := m.retrievalSkill()
	if err != nil {
		return false, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("declared retrieval skill unavailable")}
	}
	// The category description exists only where the backend groups skills under
	// one. Its digest stays empty otherwise, and the probe skips the path.
	var category []byte
	var categoryDigest string
	if skill.Category != "" {
		category, categoryDigest, err = m.retrievalCategory()
		if err != nil {
			return false, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("declared retrieval skill category unavailable")}
		}
	}
	probe, err := m.probeSkill(ctx, op, digest, categoryDigest)
	if err != nil {
		return false, err
	}
	if probe.symlink {
		return false, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("retrieval skill path is a symlink; refusing to write through it")}
	}
	if probe.state == SkillInstalled {
		return false, nil
	}

	defer func() {
		if retErr != nil {
			_, _ = m.run(ctx, op, guestexec.RootExec("rm", "-f", "--", m.skillStagingPath()))
		}
	}()
	if err := m.removeLegacySkill(ctx, op); err != nil {
		return false, err
	}
	// With a category this creates it; without one it creates the discovery root
	// itself, which the backend's own provisioning has no reason to have made.
	// Either way the directory the skill lands in exists and is owned by the
	// identity that reads it.
	if err := m.mustRun(ctx, op, KindGuestCommand, "create retrieval skill directory root",
		guestexec.RootExec("install", "-d", "-o", m.agentUser(), "-g", m.agentUser(), "-m", "0750", m.skillCategoryPath())); err != nil {
		return false, err
	}
	if m.skillCategoryFilePath() != "" {
		if err := m.writeSkillFile(ctx, op, "retrieval skill category description", category, m.skillCategoryFilePath()); err != nil {
			return false, err
		}
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "create retrieval skill directory",
		guestexec.RootExec("install", "-d", "-o", m.agentUser(), "-g", m.agentUser(), "-m", "0750", m.skillPath())); err != nil {
		return false, err
	}
	if err := m.writeSkillFile(ctx, op, "retrieval skill payload", payload, m.skillFilePath()); err != nil {
		return false, err
	}

	installed, err := m.probeSkill(ctx, op, digest, categoryDigest)
	if err != nil {
		return false, err
	}
	if installed.state != SkillInstalled {
		return false, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("retrieval skill did not match its expected payload after installation")}
	}
	return true, nil
}

// writeSkillFile lands one payload at dest by way of the staging path, which is
// deliberately outside the skill discovery root so a half-written file can
// never be walked as a skill.
func (m *Manager) writeSkillFile(ctx context.Context, op, what string, payload []byte, dest string) error {
	if err := m.mustRunInput(ctx, op, KindGuestCommand, "write "+what, payload,
		guestexec.UserExecAs(m.agentUser(), "tee", m.skillStagingPath())); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "set "+what+" permissions",
		guestexec.RootExec("chmod", "0640", m.skillStagingPath())); err != nil {
		return err
	}
	return m.mustRun(ctx, op, KindGuestCommand, "promote "+what,
		guestexec.RootExec("mv", "-T", m.skillStagingPath(), dest))
}

// removeLegacySkill retires the installation that releases before the category
// move left at $HERMES_HOME/skills/torio-brain.
//
// It removes the SKILL.md and then rmdirs the directory — never `rm -r`. What
// has to go is the second file carrying the same skill name, because two of
// them make skill_view refuse to load either. The directory itself is only
// swept up if removing that file left it empty; anything else under the old
// path is not Torio's to delete, and by then it is no longer a skill.
func (m *Manager) removeLegacySkill(ctx context.Context, op string) error {
	// A backend that groups no skills under a category never installed one at a
	// pre-category path, so there is nothing superseded to retire.
	if m.legacySkillPath() == "" {
		return nil
	}
	link, err := m.testPath(ctx, op, "-L", m.legacySkillPath())
	if err != nil {
		return err
	}
	if link {
		return &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("the superseded retrieval skill path is a symlink; refusing to remove it")}
	}
	present, err := m.testPath(ctx, op, "-d", m.legacySkillPath())
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "remove the superseded retrieval skill",
		guestexec.RootExec("rm", "-f", "--", m.legacySkillPath()+"/SKILL.md")); err != nil {
		return err
	}
	_, _ = m.run(ctx, op, guestexec.RootExec("rmdir", "--", m.legacySkillPath()))
	return nil
}

// skillProbe is the bounded on-disk view of the retrieval skill. It carries a
// digest comparison result, never the payload and never Brain content.
type skillProbe struct {
	state   SkillState
	symlink bool
}

func (m *Manager) probeSkill(ctx context.Context, op, digest, categoryDigest string) (skillProbe, error) {
	if !m.backend().BrainSkill().Installable() {
		return skillProbe{state: SkillNotApplicable}, nil
	}
	for _, path := range []string{m.skillFilePath(), m.skillPath(), m.skillCategoryFilePath(), m.skillCategoryPath()} {
		if path == "" {
			continue
		}
		link, err := m.testPath(ctx, op, "-L", path)
		if err != nil {
			return skillProbe{}, err
		}
		if link {
			return skillProbe{state: SkillDrift, symlink: true}, nil
		}
	}
	// A copy still sitting at the pre-category path is drift even when the new
	// one is perfect: two files claiming the same skill name make skill_view
	// refuse to load either of them. A backend with no category never had a
	// pre-category path, and testing one would be testing "/SKILL.md".
	if m.legacySkillPath() != "" {
		legacy, err := m.testPath(ctx, op, "-f", m.legacySkillPath()+"/SKILL.md")
		if err != nil {
			return skillProbe{}, err
		}
		if legacy {
			return skillProbe{state: SkillDrift}, nil
		}
	}
	for _, path := range []string{m.skillCategoryPath(), m.skillPath()} {
		dir, err := m.testPath(ctx, op, "-d", path)
		if err != nil {
			return skillProbe{}, err
		}
		if !dir {
			return skillProbe{state: SkillNotInstalled}, nil
		}
	}
	// The category description is checked exactly where one exists. An absent
	// path is not an absent file: `test -f ""` fails, and a probe that read that
	// as "not installed" would report a skill it had just written as missing.
	for _, path := range []string{m.skillFilePath(), m.skillCategoryFilePath()} {
		if path == "" {
			continue
		}
		file, err := m.testPath(ctx, op, "-f", path)
		if err != nil {
			return skillProbe{}, err
		}
		if !file {
			return skillProbe{state: SkillNotInstalled}, nil
		}
	}
	secure, err := m.skillOwnershipSecure(ctx, op)
	if err != nil {
		return skillProbe{}, err
	}
	if !secure {
		return skillProbe{state: SkillDrift}, nil
	}
	for _, spec := range []struct {
		path string
		want string
	}{
		{m.skillFilePath(), digest},
		{m.skillCategoryFilePath(), categoryDigest},
	} {
		if spec.path == "" {
			continue
		}
		sum, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "sha256sum", "--", spec.path))
		if err != nil {
			return skillProbe{}, err
		}
		if sum.ExitCode != 0 {
			return skillProbe{}, commandError(op, KindVerification, "digest retrieval skill payload", sum.ExitCode)
		}
		fields := strings.Fields(string(sum.Stdout))
		if len(fields) == 0 {
			return skillProbe{}, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("could not parse retrieval skill digest")}
		}
		if fields[0] != spec.want {
			return skillProbe{state: SkillDrift}, nil
		}
	}
	return skillProbe{state: SkillInstalled}, nil
}

func (m *Manager) skillOwnershipSecure(ctx context.Context, op string) (bool, error) {
	for _, spec := range []struct {
		path string
		mode string
	}{
		{m.skillCategoryPath(), "750"},
		{m.skillCategoryFilePath(), "640"},
		{m.skillPath(), "750"},
		{m.skillFilePath(), "640"},
	} {
		if spec.path == "" {
			continue
		}
		meta, err := m.run(ctx, op, guestexec.RootExec("stat", "-c", "%U:%G %a", spec.path))
		if err != nil {
			return false, err
		}
		if meta.ExitCode != 0 {
			return false, nil
		}
		owner, group, mode := guestexec.ParseOwnershipMode(string(meta.Stdout))
		if owner != m.agentUser() || group != m.agentUser() || (mode != spec.mode && mode != "0"+spec.mode) {
			return false, nil
		}
	}
	return true, nil
}

// Status inspects the Brain without returning any note path or content.
func (m *Manager) Status(ctx context.Context) (StatusReport, error) {
	const op = "status"
	report := m.newStatusReport()
	if err := m.requireBootstrapVerified(ctx, op); err != nil {
		return report, err
	}
	if err := m.requireRootAccess(ctx, op); err != nil {
		return report, err
	}
	return m.inspectStatus(ctx, op)
}

// newStatusReport is what every status answer starts from, including the ones
// that return before any guest command runs. The skill state it starts at is
// therefore the backend's declaration, not a fixed "not installed": a report
// that bailed out early on a backend which declares no skill must not claim one
// is missing, and SkillPath must be empty rather than name a file no backend
// would ever write.
func (m *Manager) newStatusReport() StatusReport {
	report := StatusReport{
		Path:       m.vault(),
		GitState:   GitMissing,
		SkillState: SkillNotInstalled,
		Issues:     []string{},
	}
	if !m.backend().BrainSkill().Installable() {
		report.SkillState = SkillNotApplicable
		return report
	}
	report.SkillPath = m.skillFilePath()
	return report
}

func (m *Manager) inspectStatus(ctx context.Context, op string) (StatusReport, error) {
	report := m.newStatusReport()
	project, err := m.projectStatus(ctx, op)
	if err != nil {
		return report, err
	}
	report.ProjectRegistered = project.Registered()
	report.ProjectConflict = project.Conflicts()

	// The skill lives under the backend's own profile, not under the Brain, so
	// probe it before the vault: an uninitialized or drifted Brain returns early
	// below and must still report honest skill state. Skill drift is deliberately
	// kept out of the Brain's own State — it is drift `brain init` repairs, and
	// folding it in would make Init refuse to run the very repair it needs to
	// perform.
	var digest, categoryDigest string
	declared := m.backend().BrainSkill()
	if declared.Installable() {
		if _, digest, err = m.retrievalSkill(); err != nil {
			return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("declared retrieval skill unavailable")}
		}
		if declared.Category != "" {
			if _, categoryDigest, err = m.retrievalCategory(); err != nil {
				return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("declared retrieval skill category unavailable")}
			}
		}
	}
	skill, err := m.probeSkill(ctx, op, digest, categoryDigest)
	if err != nil {
		return report, err
	}
	report.SkillState = skill.state
	if skill.state == SkillDrift {
		report.Issues = append(report.Issues, issueSkillDrift)
	}

	link, err := m.testRootPath(ctx, op, "-L", m.vault())
	if err != nil {
		return report, err
	}
	if link {
		report.PathExists = true
		report.Issues = append(report.Issues, "canonical_path_is_symlink")
		report.State = StateDrift
		return report, nil
	}

	exists, err := m.testRootPath(ctx, op, "-d", m.vault())
	if err != nil {
		return report, err
	}
	if !exists {
		if project.Present || project.Conflicts() {
			report.Issues = append(report.Issues, "project_registered_without_scaffold")
			report.State = StateDrift
		} else {
			report.State = StateUninitialized
		}
		return report, nil
	}
	report.PathExists = true

	meta, err := m.run(ctx, op, guestexec.RootExec("stat", "-c", "%U:%G %a", m.vault()))
	if err != nil {
		return report, err
	}
	if meta.ExitCode != 0 {
		return report, commandError(op, KindGuestCommand, "inspect canonical Brain directory", meta.ExitCode)
	}
	report.Owner, report.Group, report.Mode = guestexec.ParseOwnershipMode(string(meta.Stdout))
	report.PathSecure = report.Owner == m.agentUser() &&
		report.Group == m.agentUser() &&
		(report.Mode == "750" || report.Mode == "0750")
	if !report.PathSecure {
		report.Issues = append(report.Issues, "owner_group_or_mode_mismatch")
	}

	fs, err := m.run(ctx, op, guestexec.RootExec("findmnt", "-n", "-o", "FSTYPE", "-T", m.vault()))
	if err != nil {
		return report, err
	}
	report.FSType = strings.TrimSpace(string(fs.Stdout))
	report.NativeFilesystem = fs.ExitCode == 0 && nativeFilesystem(report.FSType)
	if !report.NativeFilesystem {
		report.Issues = append(report.Issues, "non_native_filesystem")
	}
	// Do not traverse, count, or invoke Git inside an incorrectly owned,
	// over-permissive, or non-native directory. Metadata drift is already
	// sufficient to fail closed, and avoiding content access preserves the
	// privacy boundary when the canonical path resolves somewhere unexpected.
	if !report.PathSecure || !report.NativeFilesystem {
		report.State = StateDrift
		return report, nil
	}

	emptyRes, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "find", m.vault(), "-mindepth", "1", "-maxdepth", "1", "-printf", ".", "-quit"))
	if err != nil {
		return report, err
	}
	if emptyRes.ExitCode != 0 {
		return report, commandError(op, KindGuestCommand, "inspect canonical Brain directory", emptyRes.ExitCode)
	}
	empty := len(emptyRes.Stdout) == 0

	if empty {
		if project.Present || project.Conflicts() {
			report.Issues = append(report.Issues, "project_registered_without_scaffold")
		}
		// An empty directory has nothing to overwrite, so a leftover
		// registration on its own must not block a rebuild.
		//
		// Treating it as drift made `brain init` refuse forever: the drift
		// branch repairs a scaffold that exists, and here there is none. There
		// was no way out either, because Hermes cannot free a slug — `archive`
		// keeps the project visible to `project show`, and no delete exists. An
		// operator who cleared the Brain to start over was left with a Brain
		// that could not be recreated by any supported command.
		//
		// A registration pointing at a *different* path stays drift. That slug
		// belongs to something else, and scaffolding under it would trample a
		// project Torio does not own.
		if report.PathSecure && report.NativeFilesystem && !project.Conflicts() {
			report.State = StateUninitialized
		} else {
			report.State = StateDrift
		}
		return report, nil
	}

	symlinks, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "find", m.vault(), "-type", "l", "-printf", ".", "-quit"))
	if err != nil {
		return report, err
	}
	hasSymlink := symlinks.ExitCode != 0 || len(symlinks.Stdout) != 0
	if hasSymlink {
		report.Issues = append(report.Issues, "symlink_present")
	}

	scaffoldComplete := true
	for _, name := range canonicalFiles {
		ok, checkErr := m.testPath(ctx, op, "-f", m.vault()+"/"+name)
		if checkErr != nil {
			return report, checkErr
		}
		if !ok {
			scaffoldComplete = false
		}
	}
	attachmentsPresent := false
	for _, name := range canonicalDirectories {
		ok, checkErr := m.testPath(ctx, op, "-d", m.vault()+"/"+name)
		if checkErr != nil {
			return report, checkErr
		}
		if name == "attachments" {
			attachmentsPresent = ok
		}
		if !ok {
			scaffoldComplete = false
		}
	}
	if !scaffoldComplete {
		report.Issues = append(report.Issues, "canonical_scaffold_incomplete")
	}

	head, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "git", "-C", m.vault(), "rev-parse", "--verify", "HEAD"))
	if err != nil {
		return report, err
	}
	gitRepo := head.ExitCode == 0 && strings.TrimSpace(string(head.Stdout)) != ""
	if !gitRepo {
		report.Issues = append(report.Issues, "git_repository_missing")
	} else {
		remotes, runErr := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "git", "-C", m.vault(), "remote"))
		if runErr != nil {
			return report, runErr
		}
		if remotes.ExitCode != 0 {
			return report, commandError(op, KindGit, "inspect git remotes", remotes.ExitCode)
		}
		report.GitHasRemote = strings.TrimSpace(string(remotes.Stdout)) != ""
		if report.GitHasRemote {
			report.Issues = append(report.Issues, "git_remote_present")
		}
		worktree, runErr := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "git", "-C", m.vault(), "status", "--porcelain=v1", "--untracked-files=normal"))
		if runErr != nil {
			return report, runErr
		}
		if worktree.ExitCode != 0 {
			return report, commandError(op, KindGit, "inspect git worktree", worktree.ExitCode)
		}
		if len(worktree.Stdout) == 0 {
			report.GitState = GitClean
		} else {
			report.GitState = GitDirty
		}
	}

	md, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "find", m.vault(), "-type", "f", "-name", "*.md", "-printf", "."))
	if err != nil {
		return report, err
	}
	if md.ExitCode != 0 || !onlyDots(md.Stdout) {
		return report, commandError(op, KindVerification, "count Markdown files", md.ExitCode)
	}
	report.MarkdownFiles = len(md.Stdout)

	if attachmentsPresent {
		attachments, runErr := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "find", m.vault()+"/attachments", "-type", "f", "-printf", "."))
		if runErr != nil {
			return report, runErr
		}
		if attachments.ExitCode != 0 || !onlyDots(attachments.Stdout) {
			return report, commandError(op, KindVerification, "count attachments", attachments.ExitCode)
		}
		report.AttachmentFiles = len(attachments.Stdout)
	}

	size, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "du", "-sb", "--", m.vault()))
	if err != nil {
		return report, err
	}
	report.TotalBytes, err = parseTotalBytes(size)
	if err != nil {
		return report, &Error{Op: op, Kind: KindVerification, Err: err}
	}

	report.ManagedScaffold = scaffoldComplete && gitRepo && !report.GitHasRemote && !hasSymlink
	// Registration is only a condition where a backend has a registry. Without
	// that clause an unregistered vault is drift on a backend that has nowhere to
	// register it, `init` repairs nothing, and its own postcondition then fails —
	// which is to say the Brain could never be initialized at all on such a
	// backend, however healthy the vault on disk.
	registrationDeclared := m.backend().Registry() != nil
	switch {
	case !report.PathSecure || !report.NativeFilesystem || !report.ManagedScaffold:
		report.State = StateDrift
	case project.Conflicts():
		report.Issues = append(report.Issues, "project_slug_conflict")
		report.State = StateDrift
	case registrationDeclared && !project.Registered():
		report.Issues = append(report.Issues, "project_not_registered")
		report.State = StateDrift
	default:
		report.State = StateInitialized
	}
	return report, nil
}

func (m *Manager) requireBootstrapVerified(ctx context.Context, op string) error {
	if _, err := m.guest.Bootstrap(ctx, m.bootstrapOpts); err != nil {
		return &Error{
			Op:   op,
			Kind: KindPrecondition,
			Err:  fmt.Errorf("VM %q is not bootstrap-verified; run `torio vm bootstrap`: %w", lima.InstanceName, err),
		}
	}
	return nil
}

func (m *Manager) requireRootAccess(ctx context.Context, op string) error {
	return m.mustRun(ctx, op, KindGuestCommand, "verify passwordless sudo", guestexec.RootExec("true"))
}

// ensureProject registers the vault as a project with the backend, so a
// cross-project retrieval skill has something to retrieve from. A backend that
// declares no project registry has nothing to register the vault with, and the
// vault is no less usable for it: the skill reaches a path, not a registration.
func (m *Manager) ensureProject(ctx context.Context, op string) error {
	reg := m.backend().Registry()
	if reg == nil {
		return nil
	}
	status, err := m.projectStatus(ctx, op)
	if err != nil {
		return err
	}
	if status.Conflicts() {
		return &Error{Op: op, Kind: KindRegistration, Err: fmt.Errorf("backend project slug %q points to a different primary path", ProjectSlug)}
	}
	if status.Registered() {
		return nil
	}
	if err := reg.Create(ctx, m.guest, ProjectSlug, ProjectName, m.vault()); err != nil {
		return m.mapRegistryErr(op, err)
	}
	status, err = m.projectStatus(ctx, op)
	if err != nil {
		return err
	}
	if !status.Registered() {
		return &Error{Op: op, Kind: KindRegistration, Err: fmt.Errorf("backend project registration postcondition failed")}
	}
	return nil
}

func (m *Manager) projectStatus(ctx context.Context, op string) (backend.RegistryStatus, error) {
	reg := m.backend().Registry()
	if reg == nil {
		// No registry to be registered with. The vault is reached by path, and
		// a backend that keeps no project list has nothing to add it to.
		return backend.RegistryStatus{}, nil
	}
	status, err := reg.Status(ctx, m.guest, ProjectSlug, m.vault())
	if err != nil {
		return backend.RegistryStatus{}, m.mapRegistryErr(op, err)
	}
	return status, nil
}

func (m *Manager) mapRegistryErr(op string, err error) error {
	var registryErr *backend.RegistryError
	if errors.As(err, &registryErr) && registryErr.Malformed {
		return &Error{Op: op, Kind: KindVerification, Err: registryErr.Err}
	}
	return &Error{Op: op, Kind: KindRegistration, Err: err}
}

func (m *Manager) testPath(ctx context.Context, op, flag, path string) (bool, error) {
	res, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "test", flag, path))
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, commandError(op, KindGuestCommand, "inspect canonical scaffold", res.ExitCode)
	}
}

func (m *Manager) testRootPath(ctx context.Context, op, flag, path string) (bool, error) {
	res, err := m.run(ctx, op, guestexec.RootExec("test", flag, path))
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, commandError(op, KindGuestCommand, "inspect canonical Brain path", res.ExitCode)
	}
}

func (m *Manager) acquireInitLock(ctx context.Context, op string) (string, error) {
	token, err := newLockToken()
	if err != nil {
		return "", &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("generate Brain lock token")}
	}
	for attempt := 0; attempt < 2; attempt++ {
		mkdir, runErr := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "mkdir", "-m", "0700", m.lockPath()))
		if runErr != nil {
			return "", runErr
		}
		if mkdir.ExitCode == 0 {
			if err := m.mustRunInput(ctx, op, KindGuestCommand, "record Brain init lock owner",
				[]byte(token+"\n"), guestexec.UserExecAs(m.agentUser(), "tee", m.lockPath()+"/token")); err != nil {
				_, _ = m.run(ctx, op, guestexec.RootExec("rm", "-rf", "--", m.lockPath()))
				return "", err
			}
			if err := m.mustRun(ctx, op, KindGuestCommand, "protect Brain init lock token",
				guestexec.RootExec("chmod", "0600", m.lockPath()+"/token")); err != nil {
				_, _ = m.run(ctx, op, guestexec.RootExec("rm", "-rf", "--", m.lockPath()))
				return "", err
			}
			if err := m.verifyInitLock(ctx, op, token); err != nil {
				_, _ = m.run(ctx, op, guestexec.RootExec("rm", "-rf", "--", m.lockPath()))
				return "", err
			}
			return token, nil
		}
		if mkdir.ExitCode != 1 {
			return "", commandError(op, KindGuestCommand, "acquire Brain init lock", mkdir.ExitCode)
		}
		if attempt > 0 {
			return "", &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("another Brain initialization acquired the guest lock")}
		}
		recovered, recoverErr := m.recoverStaleInitLock(ctx, op, token)
		if recoverErr != nil {
			return "", recoverErr
		}
		if !recovered {
			return "", &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("another Brain initialization holds the guest lock")}
		}
	}
	return "", &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("could not acquire Brain init lock")}
}

func (m *Manager) recoverStaleInitLock(ctx context.Context, op, recoveryToken string) (bool, error) {
	exists, err := m.testRootPath(ctx, op, "-d", m.lockPath())
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil
	}
	meta, err := m.run(ctx, op, guestexec.RootExec("stat", "-c", "%U:%G %a", m.lockPath()))
	if err != nil {
		return false, err
	}
	owner, group, mode := guestexec.ParseOwnershipMode(string(meta.Stdout))
	if meta.ExitCode != 0 || owner != m.agentUser() || group != m.agentUser() || (mode != "700" && mode != "0700") {
		return false, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("the Brain init lock has unexpected ownership or mode; refusing recovery")}
	}
	stale, err := m.run(ctx, op, guestexec.RootExec(
		"find", m.lockPath(), "-maxdepth", "0", "-mmin", "+"+staleLockAge, "-print", "-quit",
	))
	if err != nil {
		return false, err
	}
	if stale.ExitCode != 0 {
		return false, commandError(op, KindGuestCommand, "inspect Brain init lock age", stale.ExitCode)
	}
	if strings.TrimSpace(string(stale.Stdout)) == "" {
		return false, nil
	}
	if strings.TrimSpace(string(stale.Stdout)) != m.lockPath() {
		return false, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("unexpected Brain init lock age result")}
	}
	quarantine := m.lockPath() + ".stale-" + recoveryToken
	mv, err := m.run(ctx, op, guestexec.RootExec("mv", "-T", m.lockPath(), quarantine))
	if err != nil {
		return false, err
	}
	if mv.ExitCode != 0 {
		return false, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("the Brain init lock changed during stale recovery")}
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "remove quarantined stale Brain init lock",
		guestexec.RootExec("rm", "-rf", "--", quarantine)); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) verifyInitLock(ctx context.Context, op, token string) error {
	meta, err := m.run(ctx, op, guestexec.RootExec("stat", "-c", "%U:%G %a", m.lockPath()))
	if err != nil {
		return err
	}
	owner, group, mode := guestexec.ParseOwnershipMode(string(meta.Stdout))
	if meta.ExitCode != 0 || owner != m.agentUser() || group != m.agentUser() || (mode != "700" && mode != "0700") {
		return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("the Brain init lock ownership or mode changed")}
	}
	current, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "cat", m.lockPath()+"/token"))
	if err != nil {
		return err
	}
	if current.ExitCode != 0 || strings.TrimSpace(string(current.Stdout)) != token {
		return &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("the Brain init lock ownership changed")}
	}
	return nil
}

func (m *Manager) refreshInitLock(ctx context.Context, op, token string) error {
	if err := m.verifyInitLock(ctx, op, token); err != nil {
		return err
	}
	return m.mustRun(ctx, op, KindGuestCommand, "refresh Brain init lock", guestexec.UserExecAs(m.agentUser(), "touch", m.lockPath()))
}

func (m *Manager) releaseInitLock(ctx context.Context, op, token string) error {
	if err := m.verifyInitLock(ctx, op, token); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "remove Brain init lock token",
		guestexec.RootExec("rm", "-f", "--", m.lockPath()+"/token")); err != nil {
		return err
	}
	return m.mustRun(ctx, op, KindGuestCommand, "release Brain init lock", guestexec.RootExec("rmdir", m.lockPath()))
}

func newLockToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (m *Manager) run(ctx context.Context, op string, argv []string) (execx.Result, error) {
	res, err := guestexec.Run(ctx, m.guest, argv)
	switch {
	case errors.Is(err, guestexec.ErrTruncated):
		return execx.Result{}, &Error{Op: op, Kind: KindVerification, Err: err}
	case err != nil:
		return execx.Result{}, fromGuestErr(op, err)
	}
	return res, nil
}

func (m *Manager) runInput(ctx context.Context, op string, stdin []byte, argv []string) (execx.Result, error) {
	res, err := guestexec.RunInput(ctx, m.guest, stdin, argv)
	switch {
	case errors.Is(err, guestexec.ErrTruncated):
		return execx.Result{}, &Error{Op: op, Kind: KindVerification, Err: err}
	case err != nil:
		return execx.Result{}, fromGuestErr(op, err)
	}
	return res, nil
}

func (m *Manager) mustRun(ctx context.Context, op string, kind ErrorKind, action string, argv []string) error {
	res, err := m.run(ctx, op, argv)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return commandError(op, kind, action, res.ExitCode)
	}
	return nil
}

func (m *Manager) mustRunInput(ctx context.Context, op string, kind ErrorKind, action string, stdin []byte, argv []string) error {
	res, err := m.runInput(ctx, op, stdin, argv)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return commandError(op, kind, action, res.ExitCode)
	}
	return nil
}

func nativeFilesystem(fstype string) bool {
	switch fstype {
	case "ext4", "ext3", "ext2", "xfs", "btrfs":
		return true
	default:
		return false
	}
}

func onlyDots(b []byte) bool {
	for _, c := range b {
		if c != '.' {
			return false
		}
	}
	return true
}

func parseTotalBytes(res execx.Result) (int64, error) {
	if res.ExitCode != 0 {
		return 0, fmt.Errorf("measure Brain bytes exited %d", res.ExitCode)
	}
	fields := strings.Fields(string(res.Stdout))
	if len(fields) < 1 {
		return 0, fmt.Errorf("could not parse bounded Brain byte count")
	}
	n, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("could not parse bounded Brain byte count")
	}
	return n, nil
}
