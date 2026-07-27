/*
 * AI-Provenance:
 *   model: Cursor Grok 4.5
 *   harness: Cursor
 *   plugins:
 *     - lean-ai-provenance
 *   skills:
 *     - mark-ai-provenance
 */
package brain

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/lima"
)

// Guest is the narrow, typed VM boundary used by the Brain manager.
type Guest interface {
	Status(ctx context.Context) (lima.Status, error)
	SSH(ctx context.Context, command []string) (execx.Result, error)
	SSHInput(ctx context.Context, stdin []byte, command []string) (execx.Result, error)
}

var _ Guest = (*lima.Adapter)(nil)

// Manager owns initialization and verification of the canonical Brain.
type Manager struct {
	guest Guest
}

func New(guest Guest) *Manager { return &Manager{guest: guest} }

// Init creates a fresh scaffold through a private sibling staging directory,
// commits it locally, promotes it, and registers the separate Hermes Project.
// Existing managed state is verified and never overwritten.
func (m *Manager) Init(ctx context.Context) (InitReport, error) {
	const op = "init"
	report := InitReport{}

	status, err := m.Status(ctx)
	if err != nil {
		return report, err
	}
	if !status.PathExists {
		if err := m.mustRun(ctx, op, KindGuestCommand, "create canonical Brain directory",
			rootExec("install", "-d", "-o", lima.HermesUser, "-g", lima.HermesUser, "-m", "0750", Path)); err != nil {
			return report, err
		}
		status, err = m.Status(ctx)
		if err != nil {
			return report, err
		}
	}

	switch status.State {
	case StateInitialized:
		report.Status = status
		return report, nil
	case StateDrift:
		// A fully promoted, secure scaffold with only missing/conflicting Hermes
		// registration is safe to resume. Every other drift is a no-adopt
		// conflict.
		if status.ManagedScaffold && status.PathSecure && status.NativeFilesystem {
			if err := m.ensureProject(ctx, op); err != nil {
				report.Status = status
				return report, err
			}
			final, err := m.Status(ctx)
			report.Status = final
			if err != nil {
				return report, err
			}
			if final.State != StateInitialized {
				return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("managed Brain remains in drift after project registration")}
			}
			return report, nil
		}
		return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("canonical Brain directory is non-empty or has unsafe drift; refusing to adopt or overwrite it")}
	case StateUninitialized:
		// proceed
	default:
		return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("unrecognized Brain state")}
	}

	if err := m.mustRun(ctx, op, KindGuestCommand, "clean private staging",
		rootExec("rm", "-rf", "--", stagingPath)); err != nil {
		return report, err
	}
	promoted := false
	defer func() {
		if !promoted {
			_, _ = m.run(ctx, op, rootExec("rm", "-rf", "--", stagingPath))
		}
	}()

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
		status, _ := m.Status(ctx)
		report.Status = status
		return report, err
	}
	final, err := m.Status(ctx)
	report.Status = final
	if err != nil {
		return report, err
	}
	if final.State != StateInitialized {
		return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("promoted Brain did not satisfy initialized postconditions")}
	}
	return report, nil
}

// Status inspects the Brain without returning any note path or content.
func (m *Manager) Status(ctx context.Context) (StatusReport, error) {
	const op = "status"
	report := StatusReport{
		Path:       Path,
		GitState:   GitMissing,
		SkillState: SkillNotInstalled,
		Issues:     []string{},
	}
	if err := m.requireRunning(ctx, op); err != nil {
		return report, err
	}
	registered, projectConflict, err := m.projectStatus(ctx, op)
	if err != nil {
		return report, err
	}
	report.ProjectRegistered = registered
	report.ProjectConflict = projectConflict

	link, err := m.run(ctx, op, rootExec("test", "-L", Path))
	if err != nil {
		return report, err
	}
	if link.ExitCode == 0 {
		report.PathExists = true
		report.Issues = append(report.Issues, "canonical_path_is_symlink")
		report.State = StateDrift
		return report, nil
	}

	exists, err := m.run(ctx, op, rootExec("test", "-d", Path))
	if err != nil {
		return report, err
	}
	if exists.ExitCode != 0 {
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
		if report.PathSecure && report.NativeFilesystem && !registered && !projectConflict {
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

func (m *Manager) requireRunning(ctx context.Context, op string) error {
	status, err := m.guest.Status(ctx)
	if err != nil {
		return fromGuestErr(op, err)
	}
	if status.State != lima.StateRunning {
		return &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("VM %q is %s; run `torio vm start` and `torio vm bootstrap` first", lima.InstanceName, status.State)}
	}
	return nil
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
	if show.ExitCode == 0 {
		if pathMentioned(string(show.Stdout), Path) {
			return true, false, nil
		}
		return false, true, nil
	}
	list, err := m.run(ctx, op, userExec("hermes", "project", "list"))
	if err != nil {
		return false, false, err
	}
	if list.ExitCode != 0 {
		return false, false, commandError(op, KindRegistration, "list Hermes Projects", list.ExitCode)
	}
	return pathMentioned(string(list.Stdout), Path), false, nil
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
	for start := 0; ; {
		i := strings.Index(output[start:], path)
		if i < 0 {
			return false
		}
		i += start
		beforeOK := i == 0 || !isPathChar(output[i-1])
		end := i + len(path)
		afterOK := end == len(output) || !isPathChar(output[end])
		if beforeOK && afterOK {
			return true
		}
		start = i + 1
	}
}

func isPathChar(b byte) bool {
	return b == '/' || b == '-' || b == '_' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
