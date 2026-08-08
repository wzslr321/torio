package transfer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile creates root/rel with content and mode, making parents as needed.
func writeFile(t *testing.T, root, rel, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	return path
}

// The allowlist is the whole import surface: Markdown plus the attachment types
// a relative link can point at. Everything else is skipped, and the skip is
// reported as a count under a reason — never as a path.
func TestCollectStagesOnlyAllowlistedTypes(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "notes/daily.md", "# day", 0o644)
	writeFile(t, src, "notes/img/diagram.png", "\x89PNG", 0o644)
	writeFile(t, src, "notes/scan.PDF", "%PDF-1.4", 0o644)
	writeFile(t, src, "notes/report.docx", "zip", 0o644)
	writeFile(t, src, "notes/archive.zip", "zip", 0o644)

	manifest, report, err := Collect(src, "")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if manifest.Files() != 3 {
		t.Fatalf("manifest files = %d, want 3", manifest.Files())
	}
	if report.Markdown != 1 || report.Attachments != 2 {
		t.Fatalf("report = %#v, want 1 markdown and 2 attachments", report)
	}
	if report.Skipped[SkipUnsupportedType] != 2 {
		t.Fatalf("skipped unsupported = %d, want 2", report.Skipped[SkipUnsupportedType])
	}
	for _, entry := range manifest.Entries {
		if filepath.Ext(entry.Path) == ".docx" || filepath.Ext(entry.Path) == ".zip" {
			t.Fatalf("manifest contains a non-allowlisted type")
		}
	}
}

// The exclusion list is the filter proven by the promoted Gate brain-transfer
// run, reproduced in Go and made stricter. A transfer never carries repository
// metadata (`.git`), credential directories (`.ssh`, `.aws`), or
// credential-shaped basenames (`.env*`, `*.pem`, `id_rsa`) — a Brain is notes,
// and anything that can authenticate is out of its payload by construction, not
// by an operator remembering to clean the vault. The Gate filter excluded only
// `.obsidian/plugins`, to keep executable plugin code out; this excludes the
// whole `.obsidian` directory, because Obsidian's own configuration is out of
// scope until an ADR reviews it file by file.
func TestCollectExcludesRepositoriesCredentialsAndPluginCode(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "keep.md", "keep", 0o644)
	writeFile(t, src, ".git/config", "[core]", 0o644)
	writeFile(t, src, "vault/.git/HEAD", "ref", 0o644)
	writeFile(t, src, ".env", "TOKEN=x", 0o600)
	writeFile(t, src, "vault/.env.local", "TOKEN=x", 0o600)
	writeFile(t, src, "vault/server.pem", "key", 0o600)
	writeFile(t, src, "vault/id_rsa", "key", 0o600)
	writeFile(t, src, ".ssh/known_hosts", "host", 0o600)
	writeFile(t, src, ".aws/credentials", "aws", 0o600)
	writeFile(t, src, ".obsidian/plugins/evil/main.js", "code", 0o644)
	writeFile(t, src, ".obsidian/app.json", "{}", 0o644)
	writeFile(t, src, ".obsidian/notes.md", "not config", 0o644)

	manifest, report, err := Collect(src, "")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if manifest.Files() != 1 || manifest.Entries[0].Path != "keep.md" {
		t.Fatalf("manifest = %#v, want only keep.md", manifest.Entries)
	}
	if report.Skipped[SkipExcluded] == 0 {
		t.Fatalf("no excluded entries recorded: %#v", report.Skipped)
	}
}

func TestCollectRejectsSymlinksHardlinksSpecialsAndExecutables(t *testing.T) {
	src := t.TempDir()
	outside := t.TempDir()
	writeFile(t, src, "keep.md", "keep", 0o644)
	writeFile(t, outside, "secret.md", "secret", 0o644)
	writeFile(t, src, "runnable.md", "#!/bin/sh", 0o755)
	target := writeFile(t, src, "linked/original.md", "linked", 0o644)

	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(src, "escape.md")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if err := os.Symlink("../..", filepath.Join(src, "up.md")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if err := os.Link(target, filepath.Join(src, "linked", "hard.md")); err != nil {
		t.Fatalf("Link: %v", err)
	}
	if err := makeFIFO(filepath.Join(src, "pipe.md")); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}

	manifest, report, err := Collect(src, "")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	for _, entry := range manifest.Entries {
		switch entry.Path {
		case "escape.md", "up.md", "pipe.md", "runnable.md", "linked/hard.md", "linked/original.md":
			t.Errorf("staged a rejected entry class via %q", entry.Path)
		}
	}
	if manifest.Files() != 1 {
		t.Fatalf("manifest files = %d, want only keep.md", manifest.Files())
	}
	if report.Skipped[SkipSymlink] != 2 {
		t.Errorf("skipped symlinks = %d, want 2", report.Skipped[SkipSymlink])
	}
	if report.Skipped[SkipHardlink] != 2 {
		t.Errorf("skipped hardlinks = %d, want 2", report.Skipped[SkipHardlink])
	}
	if report.Skipped[SkipSpecialFile] != 1 {
		t.Errorf("skipped special files = %d, want 1", report.Skipped[SkipSpecialFile])
	}
	if report.Skipped[SkipExecutable] != 1 {
		t.Errorf("skipped executables = %d, want 1", report.Skipped[SkipExecutable])
	}
}

// Absolute and traversing relative paths must fail closed before anything is
// staged, and so must a name the checksum protocol cannot represent unambiguously.
func TestUnsafeRelativePathsAreRejected(t *testing.T) {
	for _, rel := range []string{
		"/etc/passwd", "..", "../outside.md", "a/../../etc/passwd", "foo/../bar.md",
		"note\nname.md", "note\\name.md", "note\rname.md", "\xff\xfe.md", "",
	} {
		if err := checkRelPath(rel); err == nil {
			t.Errorf("checkRelPath(%q) = nil, want rejection", rel)
		}
	}
	for _, rel := range []string{"a.md", "a/b/c.md", "日本語/ノート.md", "emoji 🧠.md"} {
		if err := checkRelPath(rel); err != nil {
			t.Errorf("checkRelPath(%q) = %v, want nil", rel, err)
		}
	}
}

// `.canvas` is admitted only after its own validation, so a file that merely
// borrows the extension cannot ride in as an attachment.
func TestCollectAdmitsCanvasOnlyAfterValidation(t *testing.T) {
	src := t.TempDir()
	writeFile(t, src, "board.canvas", `{"nodes":[],"edges":[]}`, 0o644)
	writeFile(t, src, "fake.canvas", "not json at all", 0o644)
	writeFile(t, src, "array.canvas", `["nodes"]`, 0o644)
	writeFile(t, src, "object.canvas", `{"token":"not a canvas"}`, 0o644)

	manifest, report, err := Collect(src, "")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if manifest.Files() != 1 || manifest.Entries[0].Path != "board.canvas" {
		t.Fatalf("manifest = %#v, want only the valid canvas", manifest.Entries)
	}
	if report.Skipped[SkipInvalidCanvas] != 3 {
		t.Fatalf("skipped invalid canvases = %d, want 3", report.Skipped[SkipInvalidCanvas])
	}
	if report.Attachments != 1 {
		t.Fatalf("attachments = %d, want 1", report.Attachments)
	}
}

// Staging normalizes what the transport will carry: private modes, no execute
// bit anywhere, and the relative tree shape a Markdown link depends on.
func TestCollectStagesPrivateModesAndPreservesTreeShape(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "staging")
	if err := os.Mkdir(dst, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	writeFile(t, src, "a/deep/note.md", "content", 0o666)
	writeFile(t, src, "a/deep/img.png", "png", 0o644)

	manifest, _, err := Collect(src, dst)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if manifest.Files() != 2 {
		t.Fatalf("manifest files = %d, want 2", manifest.Files())
	}
	err = filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		want := os.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("staged mode = %v for a %v entry, want %v", info.Mode().Perm(), d.Type(), want)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk staging: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "a", "deep", "note.md")); err != nil {
		t.Fatalf("staged tree shape not preserved: %v", err)
	}
}

// The digest binds content to relative path, so a rename is a different tree
// even when every byte is identical.
func TestManifestDigestBindsContentToPath(t *testing.T) {
	a := &Manifest{Entries: []Entry{{Path: "b.md", Size: 1, Sha256: "aa"}, {Path: "a.md", Size: 1, Sha256: "bb"}}}
	b := &Manifest{Entries: []Entry{{Path: "a.md", Size: 1, Sha256: "bb"}, {Path: "b.md", Size: 1, Sha256: "aa"}}}
	if a.Digest() != b.Digest() {
		t.Fatalf("digest depends on entry order: %q vs %q", a.Digest(), b.Digest())
	}
	moved := &Manifest{Entries: []Entry{{Path: "a.md", Size: 1, Sha256: "bb"}, {Path: "c.md", Size: 1, Sha256: "aa"}}}
	if moved.Digest() == a.Digest() {
		t.Fatalf("digest ignores relative path")
	}
}

func TestChecksumFileUsesContainedAbsoluteGuestPaths(t *testing.T) {
	manifest := &Manifest{Entries: []Entry{
		{Path: "notes/space name.md", Size: 1, Sha256: strings.Repeat("a", 64)},
		{Path: "日本語/ノート.md", Size: 1, Sha256: strings.Repeat("b", 64)},
	}}
	payload, err := manifest.ChecksumFile("/home/hermes/.torio-brain-import-staging/payload")
	if err != nil {
		t.Fatalf("ChecksumFile: %v", err)
	}
	text := string(payload)
	for _, want := range []string{
		strings.Repeat("a", 64) + "  /home/hermes/.torio-brain-import-staging/payload/notes/space name.md\n",
		strings.Repeat("b", 64) + "  /home/hermes/.torio-brain-import-staging/payload/日本語/ノート.md\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("checksum payload missing expected entry: %q", text)
		}
	}
	for _, prefix := range []string{"relative", "/home/hermes/../operator", "/home/hermes/bad\npath"} {
		if _, err := manifest.ChecksumFile(prefix); err == nil {
			t.Errorf("ChecksumFile accepted unsafe prefix %q", prefix)
		}
	}
}

// Verification is what makes the transfer all-or-nothing, and its failure must
// stay bounded: the error says a file did not match, never which one.
func TestCollectRejectsInvalidSourceRootsWithoutNamingThem(t *testing.T) {
	const marker = "private-customer-vault"
	parent := t.TempDir()
	real := filepath.Join(parent, marker)
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	link := filepath.Join(parent, marker+"-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	file := writeFile(t, parent, marker+".md", "not a directory", 0o600)

	for _, root := range []string{link, file, filepath.Join(parent, marker+"-missing")} {
		_, _, err := Collect(root, "")
		if err == nil {
			t.Fatalf("Collect(%q) accepted an invalid source root", root)
		}
		if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), root) {
			t.Fatalf("error leaked the private source path: %v", err)
		}
	}
}

func TestCollectRejectsUnsafeStagingWithoutNamingIt(t *testing.T) {
	const marker = "private-staging-marker"
	src := t.TempDir()
	writeFile(t, src, "note.md", "safe", 0o600)

	nonempty := filepath.Join(t.TempDir(), marker+"-nonempty")
	if err := os.Mkdir(nonempty, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	writeFile(t, nonempty, "existing.md", "existing", 0o600)

	worldWritable := filepath.Join(t.TempDir(), marker+"-writable")
	if err := os.Mkdir(worldWritable, 0o777); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.Chmod(worldWritable, 0o777); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	target := filepath.Join(t.TempDir(), marker+"-target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	link := filepath.Join(t.TempDir(), marker+"-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	for _, dst := range []string{nonempty, worldWritable, link} {
		_, _, err := Collect(src, dst)
		if err == nil {
			t.Fatalf("Collect accepted unsafe staging %q", dst)
		}
		if strings.Contains(err.Error(), marker) || strings.Contains(err.Error(), dst) {
			t.Fatalf("error leaked the private staging path: %v", err)
		}
	}
}

func TestStageFileDoesNotFollowAFinalSymlink(t *testing.T) {
	const marker = "private-outside-note"
	src := t.TempDir()
	outside := t.TempDir()
	writeFile(t, outside, marker+".md", "must not be read", 0o600)
	if err := os.Symlink(filepath.Join(outside, marker+".md"), filepath.Join(src, "note.md")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err := stageFile(src, "", "note.md")
	if err == nil {
		t.Fatal("stageFile followed a final-component symlink")
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("stageFile error leaked a private path: %v", err)
	}
}
