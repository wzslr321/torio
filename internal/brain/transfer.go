package brain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/wzslr321/torio/internal/guestexec"
	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/transfer"
)

// The import working paths, derived from the home of the identity that owns the
// vault for the same reason every other Brain path is (see Manager.vault). A
// fixed home here would be worse than an unwritable staging directory: root
// creates the parents, so an import on a backend whose identity lives elsewhere
// silently fabricates the other backend's home and stages private vault bytes —
// and parks the previous Brain — outside the boundary the owning identity keeps.
func (m *Manager) importStagingPath() string {
	return m.identity().Home + "/.torio-brain-import-staging"
}
func (m *Manager) importPayloadPath() string   { return m.importStagingPath() + "/payload" }
func (m *Manager) importManifestPath() string  { return m.importStagingPath() + "/manifest.sha256" }
func (m *Manager) importCandidatePath() string { return m.identity().Home + "/.torio-brain-candidate" }

const renameExchangeProgram = `import ctypes, os, sys
if len(sys.argv) != 3:
    sys.exit(64)
libc = ctypes.CDLL(None, use_errno=True)
renameat2 = libc.renameat2
renameat2.argtypes = [ctypes.c_int, ctypes.c_char_p, ctypes.c_int, ctypes.c_char_p, ctypes.c_uint]
renameat2.restype = ctypes.c_int
rc = renameat2(-100, os.fsencode(sys.argv[1]), -100, os.fsencode(sys.argv[2]), 2)
sys.exit(0 if rc == 0 else 1)
`

// ImportOptions describes one host-to-Brain transfer. Source is a local host
// directory; Into, when non-empty, is a contained relative subdirectory below
// the canonical Brain.
type ImportOptions struct {
	Source string
	Into   string
	DryRun bool
}

// TransferReport is deliberately aggregate-only. It cannot carry a note name
// or payload content into human output, JSON, logs, or evidence.
type TransferReport struct {
	DryRun         bool
	Files          int
	Markdown       int
	Attachments    int
	Bytes          int64
	ManifestSHA256 string
	Conflicts      int
	Skipped        map[string]int
	FinalPath      string
}

const transferCleanupTimeout = 30 * time.Second

// Import performs the host-side source preflight before contacting the guest.
// This first slice implements the complete dry-run boundary; the mutating
// staging/promotion path follows the same manifest and report.
func (m *Manager) Import(ctx context.Context, opts ImportOptions) (report TransferReport, retErr error) {
	const op = "import"
	report = TransferReport{DryRun: opts.DryRun, Skipped: map[string]int{}}

	into, err := validateInto(opts.Into)
	if err != nil {
		return report, &Error{Op: op, Kind: KindPrecondition, Err: err}
	}
	if into != "" {
		report.FinalPath = m.vault() + "/" + into
	} else {
		report.FinalPath = m.vault()
	}
	var hostRoot, hostPayload string
	if !opts.DryRun {
		hostRoot, err = os.MkdirTemp("", "torio-brain-import-")
		if err != nil {
			return report, &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("private host staging could not be created")}
		}
		defer os.RemoveAll(hostRoot)
		hostPayload = filepath.Join(hostRoot, "payload")
		if err := os.Mkdir(hostPayload, 0o700); err != nil {
			return report, &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("private host staging could not be created")}
		}
	}
	manifest, collected, err := transfer.Collect(opts.Source, hostPayload)
	if err != nil {
		return report, &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("host source preflight failed")}
	}
	report.Files = manifest.Files()
	report.Markdown = collected.Markdown
	report.Attachments = collected.Attachments
	report.Bytes = manifest.Bytes()
	report.ManifestSHA256 = manifest.Digest()
	for reason, count := range collected.Skipped {
		report.Skipped[string(reason)] = count
	}

	if err := m.requireBootstrapVerified(ctx, op); err != nil {
		return report, err
	}
	if err := m.requireRootAccess(ctx, op); err != nil {
		return report, err
	}
	if opts.DryRun {
		status, err := m.inspectStatus(ctx, op)
		if err != nil {
			return report, err
		}
		switch status.State {
		case StateUninitialized:
			// No destination files exist, so the manifest is conflict-free.
		case StateInitialized:
			// Known asymmetry, not drift: the dry run counts per-file conflicts
			// under m.vault()/<into>, while a real run with --into refuses whenever
			// the subtree exists at all (it must be one new contained subtree).
			report.Conflicts, err = m.importConflicts(ctx, op, manifest, into)
			if err != nil {
				return report, err
			}
		case StateDrift:
			return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("canonical Brain has drift; refusing transfer")}
		default:
			return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("unrecognized Brain state")}
		}
		return report, nil
	}
	if manifest.Files() == 0 {
		return report, &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("source contains no allowlisted Brain data")}
	}

	lockToken, err := m.acquireInitLock(ctx, op)
	if err != nil {
		return report, err
	}
	swapped := false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), transferCleanupTimeout)
		defer cancel()
		rollbackFailed := false
		if swapped && retErr != nil {
			if err := m.exchangeImportCandidate(cleanupCtx, op); err != nil {
				// Wrap the original failure rather than replace it: retErr is
				// already a redacted *Error, and it is what the operator has to
				// fix first.
				retErr = &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("import failed and atomic rollback also failed: %w", retErr)}
				rollbackFailed = true
			}
		}
		cleanupTargets := []string{"rm", "-rf", "--", m.importStagingPath(), m.importCandidatePath()}
		if rollbackFailed {
			cleanupTargets = []string{"rm", "-rf", "--", m.importStagingPath()}
		}
		_, _ = m.run(cleanupCtx, op, guestexec.RootExec(cleanupTargets...))
		if err := m.releaseInitLock(cleanupCtx, op, lockToken); retErr == nil && err != nil {
			retErr = err
		}
	}()

	status, err := m.inspectStatus(ctx, op)
	if err != nil {
		return report, err
	}
	existing := status.State == StateInitialized
	replacePristine := false
	if status.State != StateUninitialized && !existing {
		return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("canonical Brain has drift; refusing transfer")}
	}
	if existing {
		if into != "" {
			exists, err := m.testPrivatePath(ctx, op, "-e", m.vault()+"/"+into)
			if err != nil {
				return report, err
			}
			if exists {
				report.Conflicts = 1
				return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("contained import destination already exists")}
			}
		} else {
			report.Conflicts, err = m.importConflicts(ctx, op, manifest, "")
			if err != nil {
				return report, err
			}
			if report.Conflicts > 0 {
				replacePristine, err = m.isPristineScaffold(ctx, op, status)
				if err != nil {
					return report, err
				}
				if !replacePristine {
					return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("%d destination path(s) conflict; use --into with a new subdirectory", report.Conflicts)}
				}
			}
		}
	}
	if err := m.prepareGuestImport(ctx, op, hostPayload, manifest); err != nil {
		return report, err
	}
	if existing {
		if err := m.buildExistingImportCandidate(ctx, op, into, replacePristine); err != nil {
			return report, err
		}
	} else {
		if err := m.buildFirstImportCandidate(ctx, op, into); err != nil {
			return report, err
		}
	}
	if err := m.refreshInitLock(ctx, op, lockToken); err != nil {
		return report, err
	}
	if err := m.exchangeImportCandidate(ctx, op); err != nil {
		return report, err
	}
	swapped = true
	if err := m.ensureProject(ctx, op); err != nil {
		return report, err
	}
	final, err := m.inspectStatus(ctx, op)
	if err != nil {
		return report, err
	}
	if final.State != StateInitialized {
		return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("imported Brain did not satisfy initialized postconditions")}
	}
	initReport := InitReport{Status: final}
	if err := m.activateRetrieval(ctx, op, &initReport); err != nil {
		return report, err
	}
	swapped = false
	return report, nil
}

func (m *Manager) prepareGuestImport(ctx context.Context, op, hostPayload string, manifest *transfer.Manifest) error {
	if err := m.mustRun(ctx, op, KindGuestCommand, "clean private import staging",
		guestexec.RootExec("rm", "-rf", "--", m.importStagingPath(), m.importCandidatePath())); err != nil {
		return err
	}
	// The payload arrives over `limactl copy`, which is rsync running as the Lima
	// login user — never as hermes. Staging private to hermes could not receive
	// it at all: rsync stopped at "cannot stat destination" before writing a
	// byte, so no import ever completed. Group-writable is still not enough,
	// because rsync sets times on the destination root and only its owner may do
	// that: the transfer then lands every file and still exits 23. So the payload
	// directory belongs to whoever the guest session actually is, and
	// adoptGuestPayload hands it back to hermes before anything reads it.
	//
	// The staging root above it stays hermes-owned and not operator-writable: it
	// holds the manifest that verification checks the payload against, and the
	// side supplying the payload must not be able to rewrite its own reference.
	transportUser, err := m.guestSessionUser(ctx, op)
	if err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "create private import staging",
		guestexec.RootExec("install", "-d", "-o", m.agentUser(), "-g", lima.TorioProjectsGroup, "-m", "0750",
			m.importStagingPath())); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "create private import payload staging",
		guestexec.RootExec("install", "-d", "-o", transportUser, "-g", lima.TorioProjectsGroup, "-m", "2770",
			m.importPayloadPath())); err != nil {
		return err
	}
	checksums, err := manifest.ChecksumFile(m.importPayloadPath())
	if err != nil {
		return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("private transfer manifest could not be rendered")}
	}
	if err := m.mustRunInput(ctx, op, KindGuestCommand, "write private transfer manifest", checksums,
		guestexec.UserExecAs(m.agentUser(), "dd", "of="+m.importManifestPath(), "status=none", "conv=fsync")); err != nil {
		return err
	}
	if err := m.guest.CopyToGuest(ctx, hostPayload, m.importPayloadPath(), m.identity().Home); err != nil {
		return fromGuestErr(op, err)
	}
	if err := m.adoptGuestPayload(ctx, op); err != nil {
		return err
	}
	if err := m.verifyGuestPayload(ctx, op, manifest); err != nil {
		return err
	}
	return nil
}

// guestSessionUser reads back the identity guest commands actually run as, which
// is the identity `limactl copy` writes as. It is a guest-supplied value that
// becomes an argv element, so it is validated as a login name before use rather
// than trusted because of where it came from.
func (m *Manager) guestSessionUser(ctx context.Context, op string) (string, error) {
	res, err := m.run(ctx, op, []string{"id", "-un"})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", commandError(op, KindGuestCommand, "read the guest session identity", res.ExitCode)
	}
	user := strings.TrimSpace(string(res.Stdout))
	if err := lima.ValidateOperatorUser(user); err != nil {
		return "", &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("the guest session identity is not a usable login name")}
	}
	return user, nil
}

// adoptGuestPayload hands the copied tree from the operator to hermes.
//
// rsync lands the payload owned by the Lima login user, carrying the host
// staging modes — 0700 directories, which hermes cannot even enter. Verification
// runs as hermes and the Brain is hermes-owned throughout, so ownership and
// modes are normalized here, between the copy and the first read: hermes:hermes,
// 0750 on directories and 0640 on files, the same shape the canonical Brain
// keeps. Doing it before verification also means the checked bytes are the bytes
// that will be moved into place, with nothing rewritten afterwards.
func (m *Manager) adoptGuestPayload(ctx context.Context, op string) error {
	if err := m.mustRun(ctx, op, KindGuestCommand, "adopt the copied payload",
		guestexec.RootExec("chown", "-R", "--", m.agentUser()+":"+m.agentUser(), m.importPayloadPath())); err != nil {
		return err
	}
	return m.mustRun(ctx, op, KindGuestCommand, "normalize copied payload permissions",
		guestexec.RootExec("chmod", "-R", "u=rwX,g=rX,o=", "--", m.importPayloadPath()))
}

func (m *Manager) verifyGuestPayload(ctx context.Context, op string, manifest *transfer.Manifest) error {
	for _, argv := range [][]string{
		guestexec.UserExecAs(m.agentUser(), "find", m.importPayloadPath(), "-type", "l", "-printf", ".", "-quit"),
		guestexec.UserExecAs(m.agentUser(), "find", m.importPayloadPath(), "!", "-type", "d", "!", "-type", "f", "-printf", ".", "-quit"),
	} {
		res, err := m.run(ctx, op, argv)
		if err != nil {
			return err
		}
		if res.ExitCode != 0 || len(res.Stdout) != 0 {
			return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("guest staging contains a link or special file")}
		}
	}
	count, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "find", m.importPayloadPath(), "-type", "f", "-printf", "."))
	if err != nil {
		return err
	}
	if count.ExitCode != 0 || !onlyDots(count.Stdout) || len(count.Stdout) != manifest.Files() {
		return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("guest staging file count does not match the private manifest")}
	}
	sum, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "sha256sum", "--quiet", "--strict", "-c", m.importManifestPath()))
	if err != nil {
		return err
	}
	if sum.ExitCode != 0 {
		return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("guest staging checksum does not match the private manifest")}
	}
	return nil
}

// movePayloadIntoContainedCandidate lands the verified payload at
// candidate/<into>, creating a hermes-owned 0750 parent first when the target
// is nested deeper than the candidate root.
func (m *Manager) movePayloadIntoContainedCandidate(ctx context.Context, op, into string) error {
	target := m.importCandidatePath() + "/" + into
	if parent := path.Dir(target); parent != m.importCandidatePath() {
		if err := m.mustRun(ctx, op, KindGuestCommand, "create contained import parent",
			guestexec.RootExec("install", "-d", "-o", m.agentUser(), "-g", m.agentUser(), "-m", "0750", parent)); err != nil {
			return err
		}
	}
	return m.mustRun(ctx, op, KindGuestCommand, "move verified payload into contained candidate",
		guestexec.RootExec("mv", "-T", m.importPayloadPath(), target))
}

// verifySnapshotRepository proves dir holds exactly a local committed snapshot:
// `rev-parse --verify HEAD` answers a non-empty commit and `git remote` prints
// nothing. what names the repository in the failure messages.
func (m *Manager) verifySnapshotRepository(ctx context.Context, op, dir, what string) error {
	head, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "git", "-C", dir, "rev-parse", "--verify", "HEAD"))
	if err != nil {
		return err
	}
	if head.ExitCode != 0 || strings.TrimSpace(string(head.Stdout)) == "" {
		return &Error{Op: op, Kind: KindGit, Err: fmt.Errorf("%s has no verified snapshot", what)}
	}
	remote, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "git", "-C", dir, "remote"))
	if err != nil {
		return err
	}
	if remote.ExitCode != 0 || strings.TrimSpace(string(remote.Stdout)) != "" {
		return &Error{Op: op, Kind: KindGit, Err: fmt.Errorf("%s must have no remote", what)}
	}
	return nil
}

func (m *Manager) buildFirstImportCandidate(ctx context.Context, op, into string) error {
	if into == "" {
		if err := m.mustRun(ctx, op, KindGuestCommand, "move verified payload into candidate",
			guestexec.RootExec("mv", "-T", m.importPayloadPath(), m.importCandidatePath())); err != nil {
			return err
		}
	} else {
		if err := m.mustRun(ctx, op, KindGuestCommand, "create import candidate",
			guestexec.RootExec("install", "-d", "-o", m.agentUser(), "-g", m.agentUser(), "-m", "0750", m.importCandidatePath())); err != nil {
			return err
		}
		if err := m.movePayloadIntoContainedCandidate(ctx, op, into); err != nil {
			return err
		}
	}

	dirs := []string{m.importCandidatePath()}
	for _, name := range canonicalDirectories {
		dirs = append(dirs, m.importCandidatePath()+"/"+name)
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "complete canonical candidate directories",
		guestexec.RootExec(append([]string{"install", "-d", "-o", m.agentUser(), "-g", m.agentUser(), "-m", "0750"}, dirs...)...)); err != nil {
		return err
	}
	for _, name := range canonicalFiles {
		target := m.importCandidatePath() + "/" + name
		exists, err := m.testPrivatePath(ctx, op, "-f", target)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		payload, err := scaffoldFS.ReadFile("templates/" + name)
		if err != nil {
			return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("embedded scaffold unavailable")}
		}
		if err := m.mustRunInput(ctx, op, KindGuestCommand, "write missing candidate scaffold file", payload,
			guestexec.UserExecAs(m.agentUser(), "tee", target)); err != nil {
			return err
		}
		if err := m.mustRun(ctx, op, KindGuestCommand, "protect candidate scaffold file",
			guestexec.RootExec("chmod", "0640", target)); err != nil {
			return err
		}
	}
	if err := m.mustRun(ctx, op, KindGit, "initialize imported Brain repository",
		guestexec.UserExecAs(m.agentUser(), "git", "-C", m.importCandidatePath(), "init", "--initial-branch=main")); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGit, "stage imported Brain snapshot",
		guestexec.UserExecAs(m.agentUser(), "git", "-C", m.importCandidatePath(), "add", "-A")); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGit, "commit imported Brain snapshot",
		guestexec.UserExecAs(m.agentUser(), "git", "-C", m.importCandidatePath(),
			"-c", "user.name=torio",
			"-c", "user.email=torio@localhost",
			"commit", "-m", "Import Torio Second Brain")); err != nil {
		return err
	}
	return m.verifySnapshotRepository(ctx, op, m.importCandidatePath(), "import candidate")
}

func (m *Manager) buildExistingImportCandidate(ctx context.Context, op, into string, replacePristine bool) error {
	if err := m.mustRun(ctx, op, KindGuestCommand, "create existing-Brain candidate",
		guestexec.RootExec("install", "-d", "-o", m.agentUser(), "-g", m.agentUser(), "-m", "0750", m.importCandidatePath())); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "copy existing Brain into private candidate",
		guestexec.RootExec("cp", "-a", "--", m.vault()+"/.", m.importCandidatePath()+"/")); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGit, "stage pre-import checkpoint",
		guestexec.UserExecAs(m.agentUser(), "git", "-C", m.importCandidatePath(), "add", "-A")); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGit, "commit pre-import checkpoint",
		guestexec.UserExecAs(m.agentUser(), "git", "-C", m.importCandidatePath(),
			"-c", "user.name=torio",
			"-c", "user.email=torio@localhost",
			"commit", "--allow-empty", "-m", "Checkpoint before Torio Brain import")); err != nil {
		return err
	}

	if into != "" {
		if err := m.movePayloadIntoContainedCandidate(ctx, op, into); err != nil {
			return err
		}
	} else {
		copyArgs := guestexec.RootExec("cp", "-a", "--update=none-fail", "--", m.importPayloadPath()+"/.", m.importCandidatePath()+"/")
		if replacePristine {
			copyArgs = guestexec.RootExec("cp", "-a", "--", m.importPayloadPath()+"/.", m.importCandidatePath()+"/")
		}
		if err := m.mustRun(ctx, op, KindConflict, "copy verified payload into candidate", copyArgs); err != nil {
			return err
		}
		if err := m.mustRun(ctx, op, KindGuestCommand, "remove copied import payload",
			guestexec.RootExec("rm", "-rf", "--", m.importPayloadPath())); err != nil {
			return err
		}
	}
	if err := m.mustRun(ctx, op, KindGit, "stage imported Brain snapshot",
		guestexec.UserExecAs(m.agentUser(), "git", "-C", m.importCandidatePath(), "add", "-A")); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGit, "commit imported Brain snapshot",
		guestexec.UserExecAs(m.agentUser(), "git", "-C", m.importCandidatePath(),
			"-c", "user.name=torio",
			"-c", "user.email=torio@localhost",
			"commit", "-m", "Import Torio Second Brain")); err != nil {
		return err
	}
	return m.verifySnapshotRepository(ctx, op, m.importCandidatePath(), "import candidate")
}

func (m *Manager) isPristineScaffold(ctx context.Context, op string, status StatusReport) (bool, error) {
	if status.GitState != GitClean || status.MarkdownFiles != len(canonicalFiles) || status.AttachmentFiles != 0 {
		return false, nil
	}
	count, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "git", "-C", m.vault(), "rev-list", "--count", "HEAD"))
	if err != nil {
		return false, err
	}
	if count.ExitCode != 0 || strings.TrimSpace(string(count.Stdout)) != "1" {
		return false, nil
	}
	tree, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "git", "-C", m.vault(), "ls-tree", "-r", "--name-only", "HEAD"))
	if err != nil {
		return false, err
	}
	if tree.ExitCode != 0 {
		return false, &Error{Op: op, Kind: KindGit, Err: fmt.Errorf("could not verify pristine scaffold tree")}
	}
	got := map[string]bool{}
	for _, name := range strings.Fields(string(tree.Stdout)) {
		got[name] = true
	}
	if len(got) != len(canonicalFiles) {
		return false, nil
	}
	for _, name := range canonicalFiles {
		if !got[name] {
			return false, nil
		}
		payload, err := scaffoldFS.ReadFile("templates/" + name)
		if err != nil {
			return false, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("embedded scaffold unavailable")}
		}
		expected := sha256.Sum256(payload)
		sum, err := m.run(ctx, op, guestexec.UserExecAs(m.agentUser(), "sha256sum", "--", m.vault()+"/"+name))
		if err != nil {
			return false, err
		}
		fields := strings.Fields(string(sum.Stdout))
		if sum.ExitCode != 0 || len(fields) == 0 || fields[0] != hex.EncodeToString(expected[:]) {
			return false, nil
		}
	}
	return true, nil
}

func (m *Manager) exchangeImportCandidate(ctx context.Context, op string) error {
	res, err := m.guest.SSH(ctx, guestexec.RootExec(
		"python3", "-c", renameExchangeProgram, m.vault(), m.importCandidatePath(),
	))
	if err != nil {
		return &Error{Op: op, Kind: KindTransport, Err: fmt.Errorf("atomic Brain directory exchange could not start")}
	}
	if res.ExitCode != 0 {
		return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("atomic Brain directory exchange exited %d", res.ExitCode)}
	}
	return nil
}

func (m *Manager) importConflicts(ctx context.Context, op string, manifest *transfer.Manifest, into string) (int, error) {
	base := m.vault()
	if into != "" {
		base += "/" + into
	}
	conflicts := 0
	for _, entry := range manifest.Entries {
		exists, err := m.testPrivatePath(ctx, op, "-e", base+"/"+entry.Path)
		if err != nil {
			return 0, err
		}
		if exists {
			conflicts++
		}
	}
	return conflicts, nil
}

// testPrivatePath is the path-bearing counterpart of testPath. A transport
// failure must not wrap execx's argv-bearing diagnostic because argv contains a
// private Brain filename.
func (m *Manager) testPrivatePath(ctx context.Context, op, flag, privatePath string) (bool, error) {
	res, err := m.guest.SSH(ctx, guestexec.UserExecAs(m.agentUser(), "test", flag, privatePath))
	if err != nil {
		return false, &Error{Op: op, Kind: KindTransport, Err: fmt.Errorf("private path preflight failed")}
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, &Error{Op: op, Kind: KindGuestCommand, Err: fmt.Errorf("private path preflight exited %d", res.ExitCode)}
	}
}

func validateInto(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, "/") ||
		strings.ContainsAny(value, "\x00\n\r\\") ||
		path.Clean(value) != value ||
		value == "." {
		return "", fmt.Errorf("--into must be a contained relative subdirectory")
	}
	for _, segment := range strings.Split(value, "/") {
		switch segment {
		case "", ".", "..", ".git", ".obsidian":
			return "", fmt.Errorf("--into must be a contained relative data subdirectory")
		}
	}
	return value, nil
}
