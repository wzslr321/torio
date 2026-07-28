package brain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/lima"
)

func writeHostTransferFile(t *testing.T, root, rel, content string, mode os.FileMode) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(target, []byte(content), mode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestImportDryRunPreflightsWithoutTransferringContent(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "notes/decision.md", "# Decision", 0o600)
	writeHostTransferFile(t, source, "attachments/diagram.png", "png", 0o600)
	writeHostTransferFile(t, source, ".env", "PRIVATE_MARKER=value", 0o600)
	writeHostTransferFile(t, source, "ignored.docx", "doc", 0o600)

	g := readyFake()
	report, err := New(g).Import(context.Background(), ImportOptions{
		Source: source,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("Import dry-run: %v", err)
	}
	if !report.DryRun || report.Files != 2 || report.Markdown != 1 || report.Attachments != 1 {
		t.Fatalf("report = %#v, want bounded 2-file dry-run", report)
	}
	if report.Bytes != int64(len("# Decision")+len("png")) {
		t.Fatalf("bytes = %d", report.Bytes)
	}
	if len(report.ManifestSHA256) != 64 {
		t.Fatalf("manifest digest = %q", report.ManifestSHA256)
	}
	if report.Skipped["excluded"] != 1 || report.Skipped["unsupported_type"] != 1 {
		t.Fatalf("skipped = %#v", report.Skipped)
	}
	if len(g.copies) != 0 {
		t.Fatalf("dry-run performed content transfer: %#v", g.copies)
	}
	if g.saw("git commit") || g.saw("mv -T") || g.saw("hermes project create") {
		t.Fatalf("dry-run mutated guest state: %#v", g.calls)
	}
}

func TestImportRejectsUnsafeIntoBeforeGuestAccess(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "note.md", "note", 0o600)

	for _, into := range []string{"/absolute", ".", "..", "../escape", "a/../../escape", "a/./b", "a\\b", "a\nb", ".git", "notes/.git/data"} {
		t.Run(strings.ReplaceAll(into, "/", "_"), func(t *testing.T) {
			g := readyFake()
			_, err := New(g).Import(context.Background(), ImportOptions{
				Source: source,
				Into:   into,
				DryRun: true,
			})
			if err == nil {
				t.Fatalf("Import accepted unsafe --into %q", into)
			}
			if len(g.calls) != 0 || len(g.copies) != 0 {
				t.Fatalf("unsafe --into reached the guest: calls=%d copies=%d", len(g.calls), len(g.copies))
			}
		})
	}
}

func TestImportSourceFailureDoesNotLeakHostPath(t *testing.T) {
	const marker = "private-customer-vault"
	source := filepath.Join(t.TempDir(), marker)
	g := readyFake()

	_, err := New(g).Import(context.Background(), ImportOptions{Source: source, DryRun: true})
	if err == nil {
		t.Fatal("Import accepted a missing source")
	}
	if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), source) {
		t.Fatalf("Import error leaked source path: %v", err)
	}
	if len(g.calls) != 0 || len(g.copies) != 0 {
		t.Fatalf("source failure reached the guest: calls=%d copies=%d", len(g.calls), len(g.copies))
	}
}

func TestImportFirstRunStagesVerifiesAndPromotesOneManagedBrain(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "notes/decision.md", "# Decision", 0o600)
	writeHostTransferFile(t, source, "attachments/diagram.png", "png", 0o600)

	g := readyFake()
	report, err := New(g).Import(context.Background(), ImportOptions{Source: source})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.DryRun || report.Files != 2 || report.ManifestSHA256 == "" {
		t.Fatalf("report = %#v", report)
	}
	if len(g.copies) != 1 || g.copies[0].direction != "to_guest" {
		t.Fatalf("copies = %#v, want one host-to-guest transfer", g.copies)
	}
	if !strings.HasSuffix(g.copies[0].host, "/payload") {
		t.Fatalf("copy source = %q, want private payload staging", g.copies[0].host)
	}
	for _, want := range []string{
		"dd of=/home/hermes/.torio-brain-import-staging/manifest.sha256",
		"sha256sum --quiet --strict -c /home/hermes/.torio-brain-import-staging/manifest.sha256",
		"git -C /home/hermes/.torio-brain-candidate init",
		"git -C /home/hermes/.torio-brain-candidate add -A",
		"renameat2",
		"hermes project create",
	} {
		if !g.saw(want) {
			t.Errorf("Import did not execute %q; calls=%#v", want, g.calls)
		}
	}
	if !g.pathExists || g.empty || !g.gitRepo || !g.registered {
		t.Fatalf("final fake state is not initialized: %#v", g)
	}
	if !g.skillPresent {
		t.Fatal("successful import did not activate the retrieval skill")
	}
}

func TestImportFirstRunPostExchangeFailureRestoresEmptyBrain(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "note.md", "note", 0o600)

	g := readyFake()
	g.importFiles = 1
	g.setFailure("tee "+skillStagingPath, 1)
	_, err := New(g).Import(context.Background(), ImportOptions{Source: source})
	if err == nil {
		t.Fatal("Import skill activation failure returned nil")
	}
	if g.count("renameat2") != 2 {
		t.Fatalf("first-run failure performed %d exchanges, want promote + rollback", g.count("renameat2"))
	}
	if !g.pathExists || !g.empty || g.gitRepo {
		t.Fatalf("rollback did not restore the original empty Brain: %#v", g)
	}
}

func TestImportTransferFailureLeavesExistingBrainAndCleansStaging(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "note.md", "note", 0o600)

	g := readyFake()
	g.copyToErr = context.DeadlineExceeded
	_, err := New(g).Import(context.Background(), ImportOptions{Source: source})
	if err == nil {
		t.Fatal("Import transfer failure returned nil")
	}
	if g.saw("mv -T /home/hermes/.torio-brain-candidate " + Path) {
		t.Fatalf("failed transfer promoted a candidate: %#v", g.calls)
	}
	if !g.saw("rm -rf -- /home/hermes/.torio-brain-import-staging") {
		t.Fatalf("failed transfer did not clean guest staging: %#v", g.calls)
	}
	if !g.pathExists || !g.empty {
		t.Fatalf("failed first-run import changed the existing empty Brain path: %#v", g)
	}
}

func TestImportNonPristineCollisionRefusesBeforeContentTransfer(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "notes/existing.md", "replacement", 0o600)

	g := initializedFake()
	g.gitDirty = true
	g.privateExists = map[string]bool{Path + "/notes/existing.md": true}
	_, err := New(g).Import(context.Background(), ImportOptions{Source: source})
	if err == nil {
		t.Fatal("Import accepted a collision in a non-pristine Brain")
	}
	if len(g.copies) != 0 {
		t.Fatalf("collision transferred content: %#v", g.copies)
	}
	if g.saw("renameat2") || g.saw("mv -T /home/hermes/.torio-brain-candidate "+Path) {
		t.Fatalf("collision promoted a candidate: %#v", g.calls)
	}
	if !g.pathExists || g.empty || !g.gitRepo {
		t.Fatalf("collision changed the existing Brain: %#v", g)
	}
}

func TestImportExistingBrainUsesAtomicDirectoryExchange(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "notes/new.md", "new", 0o600)

	g := initializedFake()
	g.gitDirty = true
	g.importFiles = 1
	g.privateExists = map[string]bool{}
	report, err := New(g).Import(context.Background(), ImportOptions{Source: source})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.Files != 1 || len(g.copies) != 1 {
		t.Fatalf("report/copies = %#v / %#v", report, g.copies)
	}
	exchange := g.firstIndex("renameat2")
	snapshot := g.firstIndex("Import Torio Second Brain")
	if exchange < 0 || snapshot < 0 || exchange <= snapshot {
		t.Fatalf("atomic exchange index=%d snapshot index=%d; calls=%#v", exchange, snapshot, g.calls)
	}
	if g.count("renameat2") != 1 {
		t.Fatalf("successful import exchanged directories %d times, want once", g.count("renameat2"))
	}
	if !g.saw(importStagingPath + " " + importCandidatePath) {
		t.Fatalf("successful exchange did not remove the old candidate/backup: %#v", g.calls)
	}
}

func TestImportIntoRequiresOneNewContainedSubtree(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "note.md", "note", 0o600)

	g := initializedFake()
	g.gitDirty = true
	g.privateExists = map[string]bool{Path + "/archive/existing": true}

	_, err := New(g).Import(context.Background(), ImportOptions{
		Source: source,
		Into:   "archive/existing",
	})
	if err == nil {
		t.Fatal("Import --into accepted an existing destination subtree")
	}
	if len(g.copies) != 0 {
		t.Fatalf("conflicting --into transferred content: %#v", g.copies)
	}

	g = initializedFake()
	g.gitDirty = true
	g.importFiles = 1
	g.privateExists = map[string]bool{}
	_, err = New(g).Import(context.Background(), ImportOptions{
		Source: source,
		Into:   "archive/new-vault",
	})
	if err != nil {
		t.Fatalf("Import --into new subtree: %v", err)
	}
	if !g.saw("mv -T " + importPayloadPath + " " + importCandidatePath + "/archive/new-vault") {
		t.Fatalf("--into did not preserve the source root as one subtree: %#v", g.calls)
	}
}

func TestImportChecksumMismatchNeverPromotes(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "note.md", "note", 0o600)

	g := readyFake()
	g.importFiles = 1
	g.setFailure("sha256sum --quiet --strict -c "+importManifestPath, 1)
	_, err := New(g).Import(context.Background(), ImportOptions{Source: source})
	if err == nil {
		t.Fatal("Import accepted a checksum mismatch")
	}
	if g.saw("mv -T "+importCandidatePath+" "+Path) || g.saw("renameat2") {
		t.Fatalf("checksum mismatch promoted data: %#v", g.calls)
	}
	if !g.pathExists || !g.empty {
		t.Fatalf("checksum mismatch changed the existing Brain path: %#v", g)
	}
}

func TestImportPostExchangeFailureAtomicallyRollsBack(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "notes/new.md", "new", 0o600)

	g := initializedFake()
	g.gitDirty = true
	g.importFiles = 1
	g.privateExists = map[string]bool{}
	g.setFailure("tee "+skillStagingPath, 1)
	_, err := New(g).Import(context.Background(), ImportOptions{Source: source})
	if err == nil {
		t.Fatal("Import skill activation failure returned nil")
	}
	if g.count("renameat2") != 2 {
		t.Fatalf("post-exchange failure performed %d exchanges, want promote + rollback", g.count("renameat2"))
	}
	if !g.pathExists || g.empty || !g.gitRepo || !g.registered {
		t.Fatalf("rollback did not preserve initialized Brain state: %#v", g)
	}
}

func TestImportCancellationAfterExchangeUsesFreshRollbackContext(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "notes/new.md", "new", 0o600)

	ctx, cancel := context.WithCancel(context.Background())
	g := initializedFake()
	g.gitDirty = true
	g.importFiles = 1
	g.privateExists = map[string]bool{}
	g.cancel = cancel
	g.cancelOn = "tee " + skillStagingPath

	_, err := New(g).Import(ctx, ImportOptions{Source: source})
	if err == nil {
		t.Fatal("Import cancellation returned nil")
	}
	if g.count("renameat2") != 2 {
		t.Fatalf("cancellation performed %d exchanges, want promote + rollback", g.count("renameat2"))
	}
	if !g.pathExists || g.empty || !g.gitRepo || !g.registered {
		t.Fatalf("fresh-context rollback did not preserve initialized Brain state: %#v", g)
	}
}

func TestImportRollbackFailureRetainsTheOldBrainRecoveryCandidate(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "notes/new.md", "new", 0o600)

	g := initializedFake()
	g.gitDirty = true
	g.importFiles = 1
	g.privateExists = map[string]bool{}
	g.failExchangeAt = 2
	g.setFailure("tee "+skillStagingPath, 1)

	_, err := New(g).Import(context.Background(), ImportOptions{Source: source})
	if err == nil || !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("Import rollback failure = %v, want bounded rollback error", err)
	}
	for _, call := range g.calls[g.firstIndex("renameat2")+1:] {
		joined := strings.Join(call.argv, " ")
		if strings.Contains(joined, "rm -rf --") && strings.Contains(joined, importCandidatePath) {
			t.Fatalf("rollback failure deleted the old Brain recovery candidate: %q", joined)
		}
	}
}

func TestImportMayReplaceOnlyTheExactPristineScaffold(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "README.md", "# Existing vault", 0o600)

	g := initializedFake()
	g.importFiles = 1
	g.privateExists = map[string]bool{Path + "/README.md": true}
	report, err := New(g).Import(context.Background(), ImportOptions{Source: source})
	if err != nil {
		t.Fatalf("Import over pristine scaffold: %v", err)
	}
	if report.Conflicts != 1 {
		t.Fatalf("reported conflicts = %d, want 1 acknowledged pristine replacement", report.Conflicts)
	}
	if !g.saw("cp -a -- " + importPayloadPath + "/. " + importCandidatePath + "/") {
		t.Fatalf("pristine replacement did not overlay the verified candidate: %#v", g.calls)
	}

	g = initializedFake()
	g.importFiles = 1
	g.privateExists = map[string]bool{Path + "/README.md": true}
	g.pristineTree = false
	_, err = New(g).Import(context.Background(), ImportOptions{Source: source})
	if err == nil {
		t.Fatal("Import treated a non-canonical one-commit tree as pristine")
	}
	if len(g.copies) != 0 {
		t.Fatalf("non-pristine collision transferred content: %#v", g.copies)
	}
}

// TestImportStagesWherePayloadTransportCanActuallyWrite pins the two facts that
// made every real `torio brain import` fail before the first byte moved.
//
// The payload arrives over `limactl copy`, which is rsync running as the Lima
// login user. Staging created 0700 hermes:hermes refused it — rsync stopped at
// "cannot stat destination" — so the destination is grouped in the one guest
// authority the operator and hermes share, and is group-writable. The staging
// root above it is not: it holds the manifest that verification checks the
// payload against, and the side supplying the payload must not be able to
// rewrite its own reference.
//
// The tree then arrives owned by the operator with the host's 0700 directory
// modes, which hermes cannot enter, so ownership is normalized before the first
// hermes-side read rather than after it.
func TestImportStagesWherePayloadTransportCanActuallyWrite(t *testing.T) {
	source := t.TempDir()
	writeHostTransferFile(t, source, "notes/decision.md", "# Decision", 0o600)
	writeHostTransferFile(t, source, "attachments/diagram.png", "png", 0o600)

	g := readyFake()
	if _, err := New(g).Import(context.Background(), ImportOptions{Source: source}); err != nil {
		t.Fatalf("Import: %v", err)
	}

	payloadStaging := "install -d -o " + fakeGuestSessionUser + " -g " + lima.TorioProjectsGroup + " -m 2770 " + importPayloadPath
	if !g.saw(payloadStaging) {
		t.Errorf("payload staging is not owned by the copy transport; want %q", payloadStaging)
	}
	rootStaging := "install -d -o " + lima.HermesUser + " -g " + lima.TorioProjectsGroup + " -m 0750 " + importStagingPath
	if !g.saw(rootStaging) {
		t.Errorf("staging root is not operator-readable-but-not-writable; want %q", rootStaging)
	}

	adopt := g.firstIndex("chown -R -- " + lima.HermesUser + ":" + lima.HermesUser + " " + importPayloadPath)
	normalize := g.firstIndex("chmod -R u=rwX,g=rX,o= -- " + importPayloadPath)
	firstRead := g.firstIndex("find " + importPayloadPath)
	if adopt < 0 || normalize < 0 {
		t.Fatalf("copied payload was never adopted by hermes: chown=%d chmod=%d", adopt, normalize)
	}
	if firstRead < 0 {
		t.Fatal("payload was never verified on the guest")
	}
	if adopt > firstRead || normalize > firstRead {
		t.Errorf("payload read as hermes at call %d before adoption (chown=%d, chmod=%d)", firstRead, adopt, normalize)
	}
}
