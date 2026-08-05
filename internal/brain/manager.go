package brain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
)

// Guest is the narrow, typed VM boundary used by the Brain manager.
type Guest interface {
	Bootstrap(ctx context.Context, opts lima.BootstrapOptions) (lima.BootstrapReport, error)
	SSH(ctx context.Context, command []string) (execx.Result, error)
	SSHInput(ctx context.Context, stdin []byte, command []string) (execx.Result, error)
	CopyToGuest(ctx context.Context, hostSourceDir, guestDestinationDir string) error
}

var _ Guest = (*lima.Adapter)(nil)

// Manager owns initialization and verification of the canonical Brain.
type Manager struct {
	guest         Guest
	bootstrapOpts lima.BootstrapOptions
}

func New(guest Guest, opts ...lima.BootstrapOptions) *Manager {
	var bootstrapOpts lima.BootstrapOptions
	if len(opts) > 0 {
		bootstrapOpts = opts[0]
	}
	return &Manager{guest: guest, bootstrapOpts: bootstrapOpts}
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
			_, _ = m.run(ctx, op, rootExec("rm", "-rf", "--", stagingPath))
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
			rootExec("install", "-d", "-o", lima.HermesUser, "-g", lima.HermesUser, "-m", "0750", Path)); err != nil {
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
		// proceed
	default:
		return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("unrecognized Brain state")}
	}

	if err := m.refreshInitLock(ctx, op, lockToken); err != nil {
		return report, err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "clean private staging",
		rootExec("rm", "-rf", "--", stagingPath)); err != nil {
		return report, err
	}

	dirs := []string{stagingPath}
	for _, name := range canonicalDirectories {
		dirs = append(dirs, stagingPath+"/"+name)
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "create private scaffold staging",
		rootExec(append([]string{"install", "-d", "-o", lima.HermesUser, "-g", lima.HermesUser, "-m", "0750"}, dirs...)...)); err != nil {
		return report, err
	}

	for _, name := range canonicalFiles {
		payload, readErr := scaffoldFS.ReadFile("templates/" + name)
		if readErr != nil {
			return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("embedded scaffold unavailable")}
		}
		if err := m.mustRunInput(ctx, op, KindGuestCommand, "write scaffold file", payload,
			userExec("tee", stagingPath+"/"+name)); err != nil {
			return report, err
		}
		if err := m.mustRun(ctx, op, KindGuestCommand, "set scaffold file permissions",
			rootExec("chmod", "0640", stagingPath+"/"+name)); err != nil {
			return report, err
		}
	}

	if err := m.mustRun(ctx, op, KindGit, "git init",
		userExec("git", "-C", stagingPath, "init", "--initial-branch=main")); err != nil {
		return report, err
	}
	if err := m.mustRun(ctx, op, KindGit, "git add",
		userExec("git", "-C", stagingPath, "add", "--", "README.md", "AGENTS.md", "todo.md")); err != nil {
		return report, err
	}
	if err := m.mustRun(ctx, op, KindGit, "git commit",
		userExec("git", "-C", stagingPath,
			"-c", "user.name=torio",
			"-c", "user.email=torio@localhost",
			"commit", "-m", "Initialize Torio Second Brain")); err != nil {
		return report, err
	}
	if err := m.verifyStagedRepository(ctx, op); err != nil {
		return report, err
	}
	if err := m.refreshInitLock(ctx, op, lockToken); err != nil {
		return report, err
	}

	if err := m.mustRun(ctx, op, KindConflict, "remove verified empty canonical directory",
		rootExec("rmdir", Path)); err != nil {
		return report, err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "promote Brain scaffold",
		rootExec("mv", "-T", stagingPath, Path)); err != nil {
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
	report.Status.SkillState = SkillInstalled
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
	payload, digest, err := retrievalSkill()
	if err != nil {
		return false, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("embedded retrieval skill unavailable")}
	}
	category, categoryDigest, err := retrievalCategory()
	if err != nil {
		return false, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("embedded retrieval skill category unavailable")}
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
			_, _ = m.run(ctx, op, rootExec("rm", "-f", "--", skillStagingPath))
		}
	}()
	if err := m.removeLegacySkill(ctx, op); err != nil {
		return false, err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "create retrieval skill category directory",
		rootExec("install", "-d", "-o", lima.HermesUser, "-g", lima.HermesUser, "-m", "0750", SkillCategoryPath)); err != nil {
		return false, err
	}
	if err := m.writeSkillFile(ctx, op, "retrieval skill category description", category, SkillCategoryFilePath); err != nil {
		return false, err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "create retrieval skill directory",
		rootExec("install", "-d", "-o", lima.HermesUser, "-g", lima.HermesUser, "-m", "0750", SkillPath)); err != nil {
		return false, err
	}
	if err := m.writeSkillFile(ctx, op, "retrieval skill payload", payload, SkillFilePath); err != nil {
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
		userExec("tee", skillStagingPath)); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "set "+what+" permissions",
		rootExec("chmod", "0640", skillStagingPath)); err != nil {
		return err
	}
	return m.mustRun(ctx, op, KindGuestCommand, "promote "+what,
		rootExec("mv", "-T", skillStagingPath, dest))
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
	link, err := m.testPath(ctx, op, "-L", legacySkillPath)
	if err != nil {
		return err
	}
	if link {
		return &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("the superseded retrieval skill path is a symlink; refusing to remove it")}
	}
	present, err := m.testPath(ctx, op, "-d", legacySkillPath)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "remove the superseded retrieval skill",
		rootExec("rm", "-f", "--", legacySkillPath+"/SKILL.md")); err != nil {
		return err
	}
	_, _ = m.run(ctx, op, rootExec("rmdir", "--", legacySkillPath))
	return nil
}

// skillProbe is the bounded on-disk view of the retrieval skill. It carries a
// digest comparison result, never the payload and never Brain content.
type skillProbe struct {
	state   SkillState
	symlink bool
}

func (m *Manager) probeSkill(ctx context.Context, op, digest, categoryDigest string) (skillProbe, error) {
	for _, path := range []string{SkillFilePath, SkillPath, SkillCategoryFilePath, SkillCategoryPath} {
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
	// refuse to load either of them.
	legacy, err := m.testPath(ctx, op, "-f", legacySkillPath+"/SKILL.md")
	if err != nil {
		return skillProbe{}, err
	}
	if legacy {
		return skillProbe{state: SkillDrift}, nil
	}
	for _, path := range []string{SkillCategoryPath, SkillPath} {
		dir, err := m.testPath(ctx, op, "-d", path)
		if err != nil {
			return skillProbe{}, err
		}
		if !dir {
			return skillProbe{state: SkillNotInstalled}, nil
		}
	}
	for _, path := range []string{SkillFilePath, SkillCategoryFilePath} {
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
		{SkillFilePath, digest},
		{SkillCategoryFilePath, categoryDigest},
	} {
		sum, err := m.run(ctx, op, userExec("sha256sum", "--", spec.path))
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
		{SkillCategoryPath, "750"},
		{SkillCategoryFilePath, "640"},
		{SkillPath, "750"},
		{SkillFilePath, "640"},
	} {
		meta, err := m.run(ctx, op, rootExec("stat", "-c", "%U:%G %a", spec.path))
		if err != nil {
			return false, err
		}
		if meta.ExitCode != 0 {
			return false, nil
		}
		owner, group, mode := parseOwnershipMode(string(meta.Stdout))
		if owner != lima.HermesUser || group != lima.HermesUser || (mode != spec.mode && mode != "0"+spec.mode) {
			return false, nil
		}
	}
	return true, nil
}

// Status inspects the Brain without returning any note path or content.
func (m *Manager) Status(ctx context.Context) (StatusReport, error) {
	const op = "status"
	report := newStatusReport()
	if err := m.requireBootstrapVerified(ctx, op); err != nil {
		return report, err
	}
	if err := m.requireRootAccess(ctx, op); err != nil {
		return report, err
	}
	return m.inspectStatus(ctx, op)
}

func newStatusReport() StatusReport {
	return StatusReport{
		Path:       Path,
		GitState:   GitMissing,
		SkillState: SkillNotInstalled,
		Issues:     []string{},
	}
}

func (m *Manager) inspectStatus(ctx context.Context, op string) (StatusReport, error) {
	report := StatusReport{
		Path:       Path,
		GitState:   GitMissing,
		SkillState: SkillNotInstalled,
		Issues:     []string{},
	}
	registered, projectConflict, err := m.projectStatus(ctx, op)
	if err != nil {
		return report, err
	}
	report.ProjectRegistered = registered
	report.ProjectConflict = projectConflict

	// The skill lives under the Hermes profile, not under the Brain, so probe it
	// before the vault: an uninitialized or drifted Brain returns early below and
	// must still report honest skill state. Skill drift is deliberately kept out
	// of the Brain's own State — it is drift `brain init` repairs, and folding it
	// in would make Init refuse to run the very repair it needs to perform.
	_, digest, err := retrievalSkill()
	if err != nil {
		return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("embedded retrieval skill unavailable")}
	}
	_, categoryDigest, err := retrievalCategory()
	if err != nil {
		return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("embedded retrieval skill category unavailable")}
	}
	skill, err := m.probeSkill(ctx, op, digest, categoryDigest)
	if err != nil {
		return report, err
	}
	report.SkillState = skill.state
	if skill.state == SkillDrift {
		report.Issues = append(report.Issues, issueSkillDrift)
	}

	link, err := m.testRootPath(ctx, op, "-L", Path)
	if err != nil {
		return report, err
	}
	if link {
		report.PathExists = true
		report.Issues = append(report.Issues, "canonical_path_is_symlink")
		report.State = StateDrift
		return report, nil
	}

	exists, err := m.testRootPath(ctx, op, "-d", Path)
	if err != nil {
		return report, err
	}
	if !exists {
		if registered || projectConflict {
			report.Issues = append(report.Issues, "project_registered_without_scaffold")
			report.State = StateDrift
		} else {
			report.State = StateUninitialized
		}
		return report, nil
	}
	report.PathExists = true

	meta, err := m.run(ctx, op, rootExec("stat", "-c", "%U:%G %a", Path))
	if err != nil {
		return report, err
	}
	if meta.ExitCode != 0 {
		return report, commandError(op, KindGuestCommand, "inspect canonical Brain directory", meta.ExitCode)
	}
	report.Owner, report.Group, report.Mode = parseOwnershipMode(string(meta.Stdout))
	report.PathSecure = report.Owner == lima.HermesUser &&
		report.Group == lima.HermesUser &&
		(report.Mode == "750" || report.Mode == "0750")
	if !report.PathSecure {
		report.Issues = append(report.Issues, "owner_group_or_mode_mismatch")
	}

	fs, err := m.run(ctx, op, rootExec("findmnt", "-n", "-o", "FSTYPE", "-T", Path))
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

	emptyRes, err := m.run(ctx, op, userExec("find", Path, "-mindepth", "1", "-maxdepth", "1", "-printf", ".", "-quit"))
	if err != nil {
		return report, err
	}
	if emptyRes.ExitCode != 0 {
		return report, commandError(op, KindGuestCommand, "inspect canonical Brain directory", emptyRes.ExitCode)
	}
	empty := len(emptyRes.Stdout) == 0

	if empty {
		if registered || projectConflict {
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
		if report.PathSecure && report.NativeFilesystem && !projectConflict {
			report.State = StateUninitialized
		} else {
			report.State = StateDrift
		}
		return report, nil
	}

	symlinks, err := m.run(ctx, op, userExec("find", Path, "-type", "l", "-printf", ".", "-quit"))
	if err != nil {
		return report, err
	}
	hasSymlink := symlinks.ExitCode != 0 || len(symlinks.Stdout) != 0
	if hasSymlink {
		report.Issues = append(report.Issues, "symlink_present")
	}

	scaffoldComplete := true
	for _, name := range canonicalFiles {
		ok, checkErr := m.testPath(ctx, op, "-f", Path+"/"+name)
		if checkErr != nil {
			return report, checkErr
		}
		if !ok {
			scaffoldComplete = false
		}
	}
	attachmentsPresent := false
	for _, name := range canonicalDirectories {
		ok, checkErr := m.testPath(ctx, op, "-d", Path+"/"+name)
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

	head, err := m.run(ctx, op, userExec("git", "-C", Path, "rev-parse", "--verify", "HEAD"))
	if err != nil {
		return report, err
	}
	gitRepo := head.ExitCode == 0 && strings.TrimSpace(string(head.Stdout)) != ""
	if !gitRepo {
		report.Issues = append(report.Issues, "git_repository_missing")
	} else {
		remotes, runErr := m.run(ctx, op, userExec("git", "-C", Path, "remote"))
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
		worktree, runErr := m.run(ctx, op, userExec("git", "-C", Path, "status", "--porcelain=v1", "--untracked-files=normal"))
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

	md, err := m.run(ctx, op, userExec("find", Path, "-type", "f", "-name", "*.md", "-printf", "."))
	if err != nil {
		return report, err
	}
	if md.ExitCode != 0 || !onlyDots(md.Stdout) {
		return report, commandError(op, KindVerification, "count Markdown files", md.ExitCode)
	}
	report.MarkdownFiles = len(md.Stdout)

	if attachmentsPresent {
		attachments, runErr := m.run(ctx, op, userExec("find", Path+"/attachments", "-type", "f", "-printf", "."))
		if runErr != nil {
			return report, runErr
		}
		if attachments.ExitCode != 0 || !onlyDots(attachments.Stdout) {
			return report, commandError(op, KindVerification, "count attachments", attachments.ExitCode)
		}
		report.AttachmentFiles = len(attachments.Stdout)
	}

	size, err := m.run(ctx, op, userExec("du", "-sb", "--", Path))
	if err != nil {
		return report, err
	}
	report.TotalBytes, err = parseTotalBytes(size)
	if err != nil {
		return report, &Error{Op: op, Kind: KindVerification, Err: err}
	}

	report.ManagedScaffold = scaffoldComplete && gitRepo && !report.GitHasRemote && !hasSymlink
	switch {
	case !report.PathSecure || !report.NativeFilesystem || !report.ManagedScaffold:
		report.State = StateDrift
	case projectConflict:
		report.Issues = append(report.Issues, "project_slug_conflict")
		report.State = StateDrift
	case !registered:
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
	return m.mustRun(ctx, op, KindGuestCommand, "verify passwordless sudo", rootExec("true"))
}

func (m *Manager) verifyStagedRepository(ctx context.Context, op string) error {
	head, err := m.run(ctx, op, userExec("git", "-C", stagingPath, "rev-parse", "--verify", "HEAD"))
	if err != nil {
		return err
	}
	if head.ExitCode != 0 || strings.TrimSpace(string(head.Stdout)) == "" {
		return commandError(op, KindGit, "verify initial scaffold commit", head.ExitCode)
	}
	remote, err := m.run(ctx, op, userExec("git", "-C", stagingPath, "remote"))
	if err != nil {
		return err
	}
	if remote.ExitCode != 0 || strings.TrimSpace(string(remote.Stdout)) != "" {
		return &Error{Op: op, Kind: KindGit, Err: fmt.Errorf("staged Brain repository must have no remote")}
	}
	return nil
}

func (m *Manager) ensureProject(ctx context.Context, op string) error {
	registered, conflict, err := m.projectStatus(ctx, op)
	if err != nil {
		return err
	}
	if conflict {
		return &Error{Op: op, Kind: KindRegistration, Err: fmt.Errorf("Hermes Project slug %q points to a different primary path", ProjectSlug)}
	}
	if registered {
		return nil
	}
	if err := m.mustRun(ctx, op, KindRegistration, "register Hermes Project",
		userExec("hermes", "project", "create", ProjectName, Path, "--slug", ProjectSlug)); err != nil {
		return err
	}
	registered, conflict, err = m.projectStatus(ctx, op)
	if err != nil {
		return err
	}
	if !registered || conflict {
		return &Error{Op: op, Kind: KindRegistration, Err: fmt.Errorf("Hermes Project registration postcondition failed")}
	}
	return nil
}

func (m *Manager) projectStatus(ctx context.Context, op string) (registered, conflict bool, err error) {
	show, err := m.run(ctx, op, userExec("hermes", "project", "show", ProjectSlug))
	if err != nil {
		return false, false, err
	}
	// Hand-verified against a real Hermes v0.19.0 guest: `hermes project show`
	// exits 0 for an unknown slug too, writing only a diagnostic to stderr,
	// because upstream `hermes_cli/main.py` calls `args.func(args)` and discards
	// the return value, making every `return 1` in `projects_cmd.py` dead code.
	// Existence must therefore be read from stdout, never from the exit code.
	if show.ExitCode == 0 {
		switch {
		case strings.TrimSpace(string(show.Stdout)) == "":
			// No project block was printed: the slug is free, not conflicting.
			return false, false, nil
		case pathMentioned(string(show.Stdout), Path):
			return true, false, nil
		default:
			// A project block exists but its primary path is not ours.
			return false, true, nil
		}
	}
	// A non-zero exit no longer means "no such project"; it means the Hermes CLI
	// itself is broken or missing (e.g. 127, or argparse's 2). A successful list
	// proves the Hermes Project CLI is available, but list output is never used
	// for path matching: it carries slugs and names, not primary paths.
	list, err := m.run(ctx, op, userExec("hermes", "project", "list"))
	if err != nil {
		return false, false, err
	}
	if list.ExitCode != 0 {
		return false, false, commandError(op, KindRegistration, "list Hermes Projects", list.ExitCode)
	}
	return false, false, nil
}

func (m *Manager) testPath(ctx context.Context, op, flag, path string) (bool, error) {
	res, err := m.run(ctx, op, userExec("test", flag, path))
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
	res, err := m.run(ctx, op, rootExec("test", flag, path))
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
		mkdir, runErr := m.run(ctx, op, userExec("mkdir", "-m", "0700", lockPath))
		if runErr != nil {
			return "", runErr
		}
		if mkdir.ExitCode == 0 {
			if err := m.mustRunInput(ctx, op, KindGuestCommand, "record Brain init lock owner",
				[]byte(token+"\n"), userExec("tee", lockPath+"/token")); err != nil {
				_, _ = m.run(ctx, op, rootExec("rm", "-rf", "--", lockPath))
				return "", err
			}
			if err := m.mustRun(ctx, op, KindGuestCommand, "protect Brain init lock token",
				rootExec("chmod", "0600", lockPath+"/token")); err != nil {
				_, _ = m.run(ctx, op, rootExec("rm", "-rf", "--", lockPath))
				return "", err
			}
			if err := m.verifyInitLock(ctx, op, token); err != nil {
				_, _ = m.run(ctx, op, rootExec("rm", "-rf", "--", lockPath))
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
	exists, err := m.testRootPath(ctx, op, "-d", lockPath)
	if err != nil {
		return false, err
	}
	if !exists {
		return true, nil
	}
	meta, err := m.run(ctx, op, rootExec("stat", "-c", "%U:%G %a", lockPath))
	if err != nil {
		return false, err
	}
	owner, group, mode := parseOwnershipMode(string(meta.Stdout))
	if meta.ExitCode != 0 || owner != lima.HermesUser || group != lima.HermesUser || (mode != "700" && mode != "0700") {
		return false, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("Brain init lock has unexpected ownership or mode; refusing recovery")}
	}
	stale, err := m.run(ctx, op, rootExec(
		"find", lockPath, "-maxdepth", "0", "-mmin", "+"+staleLockAge, "-print", "-quit",
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
	if strings.TrimSpace(string(stale.Stdout)) != lockPath {
		return false, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("unexpected Brain init lock age result")}
	}
	quarantine := lockPath + ".stale-" + recoveryToken
	mv, err := m.run(ctx, op, rootExec("mv", "-T", lockPath, quarantine))
	if err != nil {
		return false, err
	}
	if mv.ExitCode != 0 {
		return false, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("Brain init lock changed during stale recovery")}
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "remove quarantined stale Brain init lock",
		rootExec("rm", "-rf", "--", quarantine)); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) verifyInitLock(ctx context.Context, op, token string) error {
	meta, err := m.run(ctx, op, rootExec("stat", "-c", "%U:%G %a", lockPath))
	if err != nil {
		return err
	}
	owner, group, mode := parseOwnershipMode(string(meta.Stdout))
	if meta.ExitCode != 0 || owner != lima.HermesUser || group != lima.HermesUser || (mode != "700" && mode != "0700") {
		return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("Brain init lock ownership or mode changed")}
	}
	current, err := m.run(ctx, op, userExec("cat", lockPath+"/token"))
	if err != nil {
		return err
	}
	if current.ExitCode != 0 || strings.TrimSpace(string(current.Stdout)) != token {
		return &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("Brain init lock ownership changed")}
	}
	return nil
}

func (m *Manager) refreshInitLock(ctx context.Context, op, token string) error {
	if err := m.verifyInitLock(ctx, op, token); err != nil {
		return err
	}
	return m.mustRun(ctx, op, KindGuestCommand, "refresh Brain init lock", userExec("touch", lockPath))
}

func (m *Manager) releaseInitLock(ctx context.Context, op, token string) error {
	if err := m.verifyInitLock(ctx, op, token); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "remove Brain init lock token",
		rootExec("rm", "-f", "--", lockPath+"/token")); err != nil {
		return err
	}
	return m.mustRun(ctx, op, KindGuestCommand, "release Brain init lock", rootExec("rmdir", lockPath))
}

func newLockToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (m *Manager) run(ctx context.Context, op string, argv []string) (execx.Result, error) {
	res, err := m.guest.SSH(ctx, argv)
	if err != nil {
		return execx.Result{}, fromGuestErr(op, err)
	}
	if res.StdoutTruncated || res.StderrTruncated {
		return execx.Result{}, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("bounded guest output was truncated")}
	}
	return res, nil
}

func (m *Manager) runInput(ctx context.Context, op string, stdin []byte, argv []string) (execx.Result, error) {
	res, err := m.guest.SSHInput(ctx, stdin, argv)
	if err != nil {
		return execx.Result{}, fromGuestErr(op, err)
	}
	if res.StdoutTruncated || res.StderrTruncated {
		return execx.Result{}, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("bounded guest output was truncated")}
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

func rootExec(args ...string) []string {
	return append([]string{"sudo", "-n", "--"}, args...)
}

func userExec(args ...string) []string {
	return append([]string{"sudo", "-n", "-u", lima.HermesUser, "--"}, args...)
}

func parseOwnershipMode(out string) (owner, group, mode string) {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		return "", "", ""
	}
	parts := strings.SplitN(fields[0], ":", 2)
	if len(parts) != 2 {
		return "", "", ""
	}
	return parts[0], parts[1], fields[1]
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

func pathMentioned(output, path string) bool {
	found := false
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		primary, ok := strings.CutPrefix(line, "primary:")
		if !ok {
			continue
		}
		if found {
			return false
		}
		found = true
		if strings.TrimSpace(primary) != path {
			return false
		}
	}
	return found
}
