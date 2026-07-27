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

	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/transfer"
)

const (
	importStagingPath   = lima.HermesHome + "/.torio-brain-import-staging"
	importPayloadPath   = importStagingPath + "/payload"
	importManifestPath  = importStagingPath + "/manifest.sha256"
	importCandidatePath = lima.HermesHome + "/.torio-brain-candidate"
	exportStagingPath   = lima.HermesHome + "/.torio-brain-export-staging"
	exportPayloadPath   = exportStagingPath + "/payload"
)

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

// ExportOptions describes one Brain-to-host working-tree export.
type ExportOptions struct {
	Destination string
	DryRun      bool
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

const exportManifestName = "torio-brain-manifest.json"

const transferCleanupTimeout = 30 * time.Second

// Export copies the verified Brain working tree, without .git history, through
// private guest and host staging. The destination must not exist and appears
// only after the transferred bytes and manifest have both been made durable.
func (m *Manager) Export(ctx context.Context, opts ExportOptions) (report TransferReport, retErr error) {
	const op = "export"
	report = TransferReport{DryRun: opts.DryRun, Skipped: map[string]int{}}

	destination, err := validateExportDestination(opts.Destination)
	if err != nil {
		return report, &Error{Op: op, Kind: KindPrecondition, Err: err}
	}
	report.FinalPath = destination
	if err := m.requireBootstrapVerified(ctx, op); err != nil {
		return report, err
	}
	if err := m.requireRootAccess(ctx, op); err != nil {
		return report, err
	}
	status, err := m.inspectStatus(ctx, op)
	if err != nil {
		return report, err
	}
	if status.State != StateInitialized {
		return report, &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("canonical Brain is not initialized")}
	}

	if opts.DryRun {
		manifest, err := m.readGuestExportManifest(ctx, op, Path, true)
		if err != nil {
			return report, err
		}
		if exportManifestCollision(manifest) {
			return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("Brain working tree contains the reserved export manifest path")}
		}
		fillExportReport(&report, manifest)
		return report, nil
	}

	lockToken, err := m.acquireInitLock(ctx, op)
	if err != nil {
		return report, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), transferCleanupTimeout)
		defer cancel()
		_, _ = m.run(cleanupCtx, op, rootExec("rm", "-rf", "--", exportStagingPath))
		if err := m.releaseInitLock(cleanupCtx, op, lockToken); retErr == nil && err != nil {
			retErr = err
		}
	}()
	if err := m.mustRun(ctx, op, KindGuestCommand, "clean private export staging",
		rootExec("rm", "-rf", "--", exportStagingPath)); err != nil {
		return report, err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "create private export staging",
		rootExec("install", "-d", "-o", lima.HermesUser, "-g", lima.HermesUser, "-m", "0700",
			exportStagingPath, exportPayloadPath)); err != nil {
		return report, err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "copy Brain working tree into private staging",
		rootExec("cp", "-a", "--", Path+"/.", exportPayloadPath+"/")); err != nil {
		return report, err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "remove Git history from working-tree export",
		rootExec("rm", "-rf", "--", exportPayloadPath+"/.git")); err != nil {
		return report, err
	}
	manifest, err := m.readGuestExportManifest(ctx, op, exportPayloadPath, false)
	if err != nil {
		return report, err
	}
	if exportManifestCollision(manifest) {
		return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("Brain working tree contains the reserved export manifest path")}
	}
	fillExportReport(&report, manifest)

	hostStaging, err := os.MkdirTemp(filepath.Dir(destination), ".torio-brain-export-")
	if err != nil {
		return report, &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("private host export staging could not be created")}
	}
	promoted := false
	defer func() {
		if !promoted {
			_ = os.RemoveAll(hostStaging)
		}
	}()
	if err := os.Chmod(hostStaging, 0o700); err != nil {
		return report, &Error{Op: op, Kind: KindPrecondition, Err: fmt.Errorf("private host export staging could not be protected")}
	}
	if err := m.guest.CopyFromGuest(ctx, exportPayloadPath, hostStaging); err != nil {
		return report, fromGuestErr(op, err)
	}
	if err := transfer.Verify(hostStaging, manifest); err != nil {
		return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("host export staging did not match the private manifest")}
	}
	if err := manifest.WriteJSON(filepath.Join(hostStaging, exportManifestName)); err != nil {
		return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("host export manifest could not be made durable")}
	}
	if err := renameNoReplace(hostStaging, destination); err != nil {
		return report, &Error{Op: op, Kind: KindConflict, Err: fmt.Errorf("export destination could not be promoted atomically")}
	}
	if err := syncHostDirectory(filepath.Dir(destination)); err != nil {
		_ = os.Rename(destination, hostStaging)
		return report, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("export destination directory could not be made durable")}
	}
	promoted = true
	return report, nil
}

func (m *Manager) readGuestExportManifest(ctx context.Context, op, root string, excludeGit bool) (*transfer.Manifest, error) {
	common := []string{"find", root}
	if excludeGit {
		common = append(common, "-path", Path+"/.git", "-prune", "-o")
	}
	link, err := m.run(ctx, op, userExec(append(append([]string(nil), common...), "-type", "l", "-printf", ".", "-quit")...))
	if err != nil {
		return nil, err
	}
	special, err := m.run(ctx, op, userExec(append(append([]string(nil), common...), "!", "-type", "d", "!", "-type", "f", "-printf", ".", "-quit")...))
	if err != nil {
		return nil, err
	}
	if link.ExitCode != 0 || special.ExitCode != 0 || len(link.Stdout) != 0 || len(special.Stdout) != 0 {
		return nil, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("Brain working tree contains a link or special file")}
	}
	hardlinks, err := m.run(ctx, op, userExec(append(append([]string(nil), common...),
		"-type", "f", "-links", "+1", "-printf", ".", "-quit")...))
	if err != nil {
		return nil, err
	}
	if hardlinks.ExitCode != 0 || len(hardlinks.Stdout) != 0 {
		return nil, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("Brain working tree contains a hardlinked file")}
	}
	sizeArgs := append(append([]string(nil), common...), "-type", "f", "-printf", "%s\t%P\\0")
	sizes, err := m.run(ctx, op, userExec(sizeArgs...))
	if err != nil {
		return nil, err
	}
	sumArgs := append(append([]string(nil), common...), "-type", "f", "-exec", "sha256sum", "-z", "--", "{}", "+")
	checksums, err := m.run(ctx, op, userExec(sumArgs...))
	if err != nil {
		return nil, err
	}
	if sizes.ExitCode != 0 || checksums.ExitCode != 0 {
		return nil, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("Brain working-tree manifest could not be computed")}
	}
	manifest, err := transfer.ParseGuestManifest(root, checksums.Stdout, sizes.Stdout)
	if err != nil {
		return nil, &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("Brain working-tree manifest was malformed")}
	}
	return manifest, nil
}

func fillExportReport(report *TransferReport, manifest *transfer.Manifest) {
	report.Files = manifest.Files()
	report.Bytes = manifest.Bytes()
	report.ManifestSHA256 = manifest.Digest()
	for _, entry := range manifest.Entries {
		if strings.EqualFold(path.Ext(entry.Path), ".md") {
			report.Markdown++
		} else {
			report.Attachments++
		}
	}
}

func exportManifestCollision(manifest *transfer.Manifest) bool {
	for _, entry := range manifest.Entries {
		if entry.Path == exportManifestName {
			return true
		}
	}
	return false
}

func validateExportDestination(value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "\x00\n\r") {
		return "", fmt.Errorf("destination must be a new local directory")
	}
	destination, err := filepath.Abs(value)
	if err != nil || destination == string(filepath.Separator) {
		return "", fmt.Errorf("destination must be a new local directory")
	}
	if _, err := os.Lstat(destination); err == nil {
		return "", fmt.Errorf("destination already exists")
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("destination cannot be inspected safely")
	}
	parent, err := os.Lstat(filepath.Dir(destination))
	if err != nil || !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("destination parent must be an existing regular directory")
	}
	return destination, nil
}

func syncHostDirectory(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}

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
		report.FinalPath = Path + "/" + into
	} else {
		report.FinalPath = Path
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
				retErr = &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("import failed and atomic rollback also failed")}
				rollbackFailed = true
			}
		}
		cleanupTargets := []string{"rm", "-rf", "--", importStagingPath, importCandidatePath}
		if rollbackFailed {
			cleanupTargets = []string{"rm", "-rf", "--", importStagingPath}
		}
		_, _ = m.run(cleanupCtx, op, rootExec(cleanupTargets...))
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
			exists, err := m.testPrivatePath(ctx, op, "-e", Path+"/"+into)
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
		rootExec("rm", "-rf", "--", importStagingPath, importCandidatePath)); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "create private import staging",
		rootExec("install", "-d", "-o", lima.HermesUser, "-g", lima.HermesUser, "-m", "0700",
			importStagingPath, importPayloadPath)); err != nil {
		return err
	}
	checksums, err := manifest.ChecksumFile(importPayloadPath)
	if err != nil {
		return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("private transfer manifest could not be rendered")}
	}
	if err := m.mustRunInput(ctx, op, KindGuestCommand, "write private transfer manifest", checksums,
		userExec("dd", "of="+importManifestPath, "status=none", "conv=fsync")); err != nil {
		return err
	}
	if err := m.guest.CopyToGuest(ctx, hostPayload, importPayloadPath); err != nil {
		return fromGuestErr(op, err)
	}
	if err := m.verifyGuestPayload(ctx, op, manifest); err != nil {
		return err
	}
	return nil
}

func (m *Manager) verifyGuestPayload(ctx context.Context, op string, manifest *transfer.Manifest) error {
	for _, argv := range [][]string{
		userExec("find", importPayloadPath, "-type", "l", "-printf", ".", "-quit"),
		userExec("find", importPayloadPath, "!", "-type", "d", "!", "-type", "f", "-printf", ".", "-quit"),
	} {
		res, err := m.run(ctx, op, argv)
		if err != nil {
			return err
		}
		if res.ExitCode != 0 || len(res.Stdout) != 0 {
			return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("guest staging contains a link or special file")}
		}
	}
	count, err := m.run(ctx, op, userExec("find", importPayloadPath, "-type", "f", "-printf", "."))
	if err != nil {
		return err
	}
	if count.ExitCode != 0 || !onlyDots(count.Stdout) || len(count.Stdout) != manifest.Files() {
		return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("guest staging file count does not match the private manifest")}
	}
	sum, err := m.run(ctx, op, userExec("sha256sum", "--quiet", "--strict", "-c", importManifestPath))
	if err != nil {
		return err
	}
	if sum.ExitCode != 0 {
		return &Error{Op: op, Kind: KindVerification, Err: fmt.Errorf("guest staging checksum does not match the private manifest")}
	}
	return nil
}

func (m *Manager) buildFirstImportCandidate(ctx context.Context, op, into string) error {
	if into == "" {
		if err := m.mustRun(ctx, op, KindGuestCommand, "move verified payload into candidate",
			rootExec("mv", "-T", importPayloadPath, importCandidatePath)); err != nil {
			return err
		}
	} else {
		if err := m.mustRun(ctx, op, KindGuestCommand, "create import candidate",
			rootExec("install", "-d", "-o", lima.HermesUser, "-g", lima.HermesUser, "-m", "0750", importCandidatePath)); err != nil {
			return err
		}
		target := importCandidatePath + "/" + into
		if parent := path.Dir(target); parent != importCandidatePath {
			if err := m.mustRun(ctx, op, KindGuestCommand, "create contained import parent",
				rootExec("install", "-d", "-o", lima.HermesUser, "-g", lima.HermesUser, "-m", "0750", parent)); err != nil {
				return err
			}
		}
		if err := m.mustRun(ctx, op, KindGuestCommand, "move verified payload into contained candidate",
			rootExec("mv", "-T", importPayloadPath, target)); err != nil {
			return err
		}
	}

	dirs := []string{importCandidatePath}
	for _, name := range canonicalDirectories {
		dirs = append(dirs, importCandidatePath+"/"+name)
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "complete canonical candidate directories",
		rootExec(append([]string{"install", "-d", "-o", lima.HermesUser, "-g", lima.HermesUser, "-m", "0750"}, dirs...)...)); err != nil {
		return err
	}
	for _, name := range canonicalFiles {
		target := importCandidatePath + "/" + name
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
			userExec("tee", target)); err != nil {
			return err
		}
		if err := m.mustRun(ctx, op, KindGuestCommand, "protect candidate scaffold file",
			rootExec("chmod", "0640", target)); err != nil {
			return err
		}
	}
	if err := m.mustRun(ctx, op, KindGit, "initialize imported Brain repository",
		userExec("git", "-C", importCandidatePath, "init", "--initial-branch=main")); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGit, "stage imported Brain snapshot",
		userExec("git", "-C", importCandidatePath, "add", "-A")); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGit, "commit imported Brain snapshot",
		userExec("git", "-C", importCandidatePath,
			"-c", "user.name=torio",
			"-c", "user.email=torio@localhost",
			"commit", "-m", "Import Torio Second Brain")); err != nil {
		return err
	}
	head, err := m.run(ctx, op, userExec("git", "-C", importCandidatePath, "rev-parse", "--verify", "HEAD"))
	if err != nil {
		return err
	}
	if head.ExitCode != 0 || strings.TrimSpace(string(head.Stdout)) == "" {
		return &Error{Op: op, Kind: KindGit, Err: fmt.Errorf("import candidate has no verified snapshot")}
	}
	remote, err := m.run(ctx, op, userExec("git", "-C", importCandidatePath, "remote"))
	if err != nil {
		return err
	}
	if remote.ExitCode != 0 || strings.TrimSpace(string(remote.Stdout)) != "" {
		return &Error{Op: op, Kind: KindGit, Err: fmt.Errorf("import candidate must have no remote")}
	}
	return nil
}

func (m *Manager) buildExistingImportCandidate(ctx context.Context, op, into string, replacePristine bool) error {
	if err := m.mustRun(ctx, op, KindGuestCommand, "create existing-Brain candidate",
		rootExec("install", "-d", "-o", lima.HermesUser, "-g", lima.HermesUser, "-m", "0750", importCandidatePath)); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGuestCommand, "copy existing Brain into private candidate",
		rootExec("cp", "-a", "--", Path+"/.", importCandidatePath+"/")); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGit, "stage pre-import checkpoint",
		userExec("git", "-C", importCandidatePath, "add", "-A")); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGit, "commit pre-import checkpoint",
		userExec("git", "-C", importCandidatePath,
			"-c", "user.name=torio",
			"-c", "user.email=torio@localhost",
			"commit", "--allow-empty", "-m", "Checkpoint before Torio Brain import")); err != nil {
		return err
	}

	if into != "" {
		target := importCandidatePath + "/" + into
		if parent := path.Dir(target); parent != importCandidatePath {
			if err := m.mustRun(ctx, op, KindGuestCommand, "create contained import parent",
				rootExec("install", "-d", "-o", lima.HermesUser, "-g", lima.HermesUser, "-m", "0750", parent)); err != nil {
				return err
			}
		}
		if err := m.mustRun(ctx, op, KindGuestCommand, "move verified payload into contained candidate",
			rootExec("mv", "-T", importPayloadPath, target)); err != nil {
			return err
		}
	} else {
		copyArgs := rootExec("cp", "-a", "--update=none-fail", "--", importPayloadPath+"/.", importCandidatePath+"/")
		if replacePristine {
			copyArgs = rootExec("cp", "-a", "--", importPayloadPath+"/.", importCandidatePath+"/")
		}
		if err := m.mustRun(ctx, op, KindConflict, "copy verified payload into candidate", copyArgs); err != nil {
			return err
		}
		if err := m.mustRun(ctx, op, KindGuestCommand, "remove copied import payload",
			rootExec("rm", "-rf", "--", importPayloadPath)); err != nil {
			return err
		}
	}
	if err := m.mustRun(ctx, op, KindGit, "stage imported Brain snapshot",
		userExec("git", "-C", importCandidatePath, "add", "-A")); err != nil {
		return err
	}
	if err := m.mustRun(ctx, op, KindGit, "commit imported Brain snapshot",
		userExec("git", "-C", importCandidatePath,
			"-c", "user.name=torio",
			"-c", "user.email=torio@localhost",
			"commit", "-m", "Import Torio Second Brain")); err != nil {
		return err
	}
	head, err := m.run(ctx, op, userExec("git", "-C", importCandidatePath, "rev-parse", "--verify", "HEAD"))
	if err != nil {
		return err
	}
	if head.ExitCode != 0 || strings.TrimSpace(string(head.Stdout)) == "" {
		return &Error{Op: op, Kind: KindGit, Err: fmt.Errorf("import candidate has no verified snapshot")}
	}
	remote, err := m.run(ctx, op, userExec("git", "-C", importCandidatePath, "remote"))
	if err != nil {
		return err
	}
	if remote.ExitCode != 0 || strings.TrimSpace(string(remote.Stdout)) != "" {
		return &Error{Op: op, Kind: KindGit, Err: fmt.Errorf("import candidate must have no remote")}
	}
	return nil
}

func (m *Manager) isPristineScaffold(ctx context.Context, op string, status StatusReport) (bool, error) {
	if status.GitState != GitClean || status.MarkdownFiles != len(canonicalFiles) || status.AttachmentFiles != 0 {
		return false, nil
	}
	count, err := m.run(ctx, op, userExec("git", "-C", Path, "rev-list", "--count", "HEAD"))
	if err != nil {
		return false, err
	}
	if count.ExitCode != 0 || strings.TrimSpace(string(count.Stdout)) != "1" {
		return false, nil
	}
	tree, err := m.run(ctx, op, userExec("git", "-C", Path, "ls-tree", "-r", "--name-only", "HEAD"))
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
		sum, err := m.run(ctx, op, userExec("sha256sum", "--", Path+"/"+name))
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
	res, err := m.guest.SSH(ctx, rootExec(
		"python3", "-c", renameExchangeProgram, Path, importCandidatePath,
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
	base := Path
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
	res, err := m.guest.SSH(ctx, userExec("test", flag, privatePath))
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
