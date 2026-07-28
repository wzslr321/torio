/*
 * AI-Provenance:
 *   model: Claude Opus 5
 *   harness: Claude Code
 */
// Package transfer is the host-side half of `torio brain import/export`: it
// walks a local directory, applies the import filter, hashes what survives into
// a private manifest, and stages the allowed bytes into a private directory that
// the promoted transport can move.
//
// Nothing in this package talks to a VM, and nothing in it may emit a note name.
// Every exported value is either an aggregate (counts, bytes) or a digest, so a
// caller that renders a Report can never leak vault content.
package transfer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// markdownExt is the only note extension V1 imports. Obsidian writes `.md`;
// admitting more spellings would widen the surface without evidence that any
// vault needs it.
const markdownExt = ".md"

// canvasExt is admitted only after validCanvas accepts the payload.
const canvasExt = ".canvas"

// attachmentExts are the local attachment types a relative Markdown link can
// point at (plan Task 12). `.canvas` is deliberately absent: it is admitted
// separately, after its own content validation.
var attachmentExts = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
	".svg":  true,
	".pdf":  true,
}

// SkipReason classifies why an entry was not staged. It is the only thing a
// report ever says about a rejected path: the reason, and how many times it
// fired. Never which path.
type SkipReason string

const (
	SkipExcluded        SkipReason = "excluded"
	SkipUnsupportedType SkipReason = "unsupported_type"
	SkipSymlink         SkipReason = "symlink"
	SkipHardlink        SkipReason = "hardlink"
	SkipSpecialFile     SkipReason = "special_file"
	SkipExecutable      SkipReason = "executable"
	SkipUnsafePath      SkipReason = "unsafe_path"
	SkipInvalidCanvas   SkipReason = "invalid_canvas"
)

// maxCanvasBytes bounds a `.canvas` file before it is parsed. A canvas is a
// small JSON description of a board; anything larger is not one, and refusing to
// buffer it keeps the validator from being a memory lever.
const maxCanvasBytes = 8 << 20

// excludedDirs never enter a transfer, with or without a matching extension.
// `.obsidian` is stricter than the Gate harness, which excluded only
// `.obsidian/plugins`: V1 imports notes, and Obsidian's own configuration is
// explicitly out of scope until an ADR reviews it file by file.
var excludedDirs = map[string]bool{
	".git":      true,
	".obsidian": true,
	".ssh":      true,
	".aws":      true,
}

// excludedExactNames are credential-shaped basenames. Most would already fail
// the extension allowlist; naming them keeps the reason honest ("excluded", not
// "unsupported type") and keeps the filter equivalent to the proven harness.
var excludedExactNames = map[string]bool{
	".env":             true,
	"id_rsa":           true,
	"id_ed25519":       true,
	"credentials":      true,
	"credentials.json": true,
}

// excludedPrefixes and excludedSuffixes catch the credential families the
// harness matched with globs (`.env.*`, `*.pem`, `id_rsa*`, `credentials*`).
var (
	excludedPrefixes = []string{".env.", "id_rsa", "id_ed25519", "credentials"}
	excludedSuffixes = []string{".pem", ".key", ".credentials.json"}
)

// Entry is one staged file: its slash-separated path relative to the source
// root, its size, and the sha256 of its content.
type Entry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Sha256 string `json:"sha256"`
}

// Manifest is the ordered set of files a transfer covers. Entries are sorted by
// path so the digest is independent of directory-walk order.
type Manifest struct {
	Entries []Entry
}

// Files returns the number of covered files.
func (m *Manifest) Files() int { return len(m.Entries) }

// Bytes returns the total covered content size.
func (m *Manifest) Bytes() int64 {
	var total int64
	for _, e := range m.Entries {
		total += e.Size
	}
	return total
}

// Report is the bounded, payload-free outcome of a Collect. It is what the CLI
// renders, so by construction it carries no path.
type Report struct {
	Markdown    int
	Attachments int
	Skipped     map[SkipReason]int
}

// Collect walks root, applies the import filter, and returns the manifest of
// what may be transferred plus a bounded report of what was skipped. When dst is
// non-empty the allowed bytes are also staged under it.
func Collect(root, dst string) (*Manifest, Report, error) {
	manifest := &Manifest{}
	report := Report{Skipped: map[SkipReason]int{}}
	if err := validateRoot(root, false); err != nil {
		return nil, Report{}, privateError("source directory is not a readable regular directory")
	}
	if dst != "" {
		if err := validateRoot(dst, true); err != nil {
			return nil, Report{}, privateError("host staging directory is not private and empty")
		}
		if filepath.Clean(root) == filepath.Clean(dst) {
			return nil, Report{}, privateError("source and staging directories must be distinct")
		}
	}

	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if full == root {
			return nil
		}
		rel, relErr := relPath(root, full)
		if relErr != nil {
			return relErr
		}
		// An excluded directory is counted once and never descended into, so a
		// vault with a large .git costs one decision instead of thousands.
		if d.IsDir() {
			if excludedDirs[path.Base(rel)] {
				report.Skipped[SkipExcluded]++
				return filepath.SkipDir
			}
			return nil
		}
		if reason, ok := rejectEntry(rel, d); ok {
			report.Skipped[reason]++
			return nil
		}
		ext := strings.ToLower(path.Ext(rel))
		switch {
		case ext == markdownExt:
			report.Markdown++
		case attachmentExts[ext]:
			report.Attachments++
		case ext == canvasExt:
			ok, canvasErr := validCanvas(full)
			if canvasErr != nil {
				return canvasErr
			}
			if !ok {
				report.Skipped[SkipInvalidCanvas]++
				return nil
			}
			report.Attachments++
		default:
			report.Skipped[SkipUnsupportedType]++
			return nil
		}
		entry, stageErr := stageFile(root, dst, rel)
		if stageErr != nil {
			return stageErr
		}
		manifest.Entries = append(manifest.Entries, entry)
		return nil
	})
	if err != nil {
		return nil, Report{}, privateError("source tree could not be staged safely")
	}
	sort.Slice(manifest.Entries, func(i, j int) bool {
		return manifest.Entries[i].Path < manifest.Entries[j].Path
	})
	if dst != "" {
		if err := syncStagingTree(dst); err != nil {
			return nil, Report{}, privateError("host staging could not be made durable")
		}
	}
	return manifest, report, nil
}

// stageFile hashes root/rel and, when dst is non-empty, copies it to dst/rel.
func stageFile(root, dst, rel string) (Entry, error) {
	src := filepath.Join(root, filepath.FromSlash(rel))
	in, info, err := openRegularNoFollow(src)
	if err != nil {
		return Entry{}, privateError("source entry changed during staging")
	}
	defer in.Close()
	if hardLinked(info) {
		return Entry{}, privateError("source entry changed during staging")
	}

	digest := sha256.New()
	var sink io.Writer = digest
	var out *os.File
	var target string
	if dst != "" {
		target = filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return Entry{}, privateError("host staging could not be created")
		}
		out, err = os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return Entry{}, privateError("host staging was not empty")
		}
		defer func() {
			_ = out.Close()
		}()
		sink = io.MultiWriter(digest, out)
	}
	size, err := io.Copy(sink, in)
	if err != nil {
		if target != "" {
			_ = os.Remove(target)
		}
		return Entry{}, privateError("source entry changed during staging")
	}
	if out != nil {
		if err := out.Sync(); err != nil {
			_ = os.Remove(target)
			return Entry{}, privateError("host staging file could not be made durable")
		}
		if err := out.Close(); err != nil {
			_ = os.Remove(target)
			return Entry{}, privateError("host staging file could not be closed safely")
		}
	}
	return Entry{Path: rel, Size: size, Sha256: hex.EncodeToString(digest.Sum(nil))}, nil
}

// validCanvas reports whether path holds an Obsidian canvas: a single bounded
// JSON object. The extension alone is not enough — `.canvas` is the one
// allowlisted type whose payload Torio actually interprets, and admitting
// arbitrary bytes under it would turn the allowlist into a bypass.
func validCanvas(path string) (bool, error) {
	file, info, err := openRegularNoFollow(path)
	if err != nil {
		return false, err
	}
	defer file.Close()
	if info.Size() > maxCanvasBytes {
		return false, nil
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxCanvasBytes+1))
	if err != nil {
		return false, err
	}
	var board map[string]any
	if err := json.Unmarshal(payload, &board); err != nil {
		return false, nil
	}
	return true, nil
}

// Digest is the content manifest hash: sha256 over one
// "<file digest>  <relative path>" line per entry, in path order. It matches the
// shape the Gate harness proved round-trip identical, so a tree that survives a
// transfer keeps both its bytes and its shape.
func (m *Manifest) Digest() string {
	entries := append([]Entry(nil), m.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	sum := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(sum, "%s  %s\n", e.Sha256, e.Path)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// ChecksumFile renders the private sha256sum input used to verify guest
// staging. It is payload metadata and must travel only as command stdin; callers
// must never log or render it. Absolute guest paths avoid a shell/cwd wrapper,
// while the same relative paths remain bound by Digest.
func (m *Manifest) ChecksumFile(guestPrefix string) ([]byte, error) {
	if !strings.HasPrefix(guestPrefix, "/") ||
		path.Clean(guestPrefix) != guestPrefix ||
		strings.ContainsAny(guestPrefix, "\x00\n\r\\") {
		return nil, privateError("checksum prefix is outside guest staging")
	}
	var payload bytes.Buffer
	for _, entry := range m.Entries {
		if err := checkRelPath(entry.Path); err != nil {
			return nil, privateError("manifest contains an unsafe path")
		}
		if len(entry.Sha256) != sha256.Size*2 {
			return nil, privateError("manifest contains an invalid digest")
		}
		fmt.Fprintf(&payload, "%s  %s/%s\n", entry.Sha256, guestPrefix, entry.Path)
	}
	return payload.Bytes(), nil
}

// rejectEntry applies every non-extension rule in the order the proven harness
// applies them: unsafe name, exclusion list, symlink, special file, hardlink,
// executable bit. It reports the reason and whether the entry is rejected.
//
// The order matters. The name is judged before the filesystem is touched, and a
// symlink is judged before anything opens it, so no rule can be reached through
// a link that leaves the source root.
func rejectEntry(rel string, d fs.DirEntry) (SkipReason, bool) {
	if err := checkRelPath(rel); err != nil {
		return SkipUnsafePath, true
	}
	if excluded(rel) {
		return SkipExcluded, true
	}
	if d.Type()&fs.ModeSymlink != 0 {
		return SkipSymlink, true
	}
	if !d.Type().IsRegular() {
		return SkipSpecialFile, true
	}
	info, err := d.Info()
	if err != nil {
		return SkipSpecialFile, true
	}
	// A hardlinked note is one name for bytes that may also live outside the
	// source root, and the transport would silently flatten it into a full copy.
	// The harness rejects every file with more than one link; so do we.
	if hardLinked(info) {
		return SkipHardlink, true
	}
	if info.Mode().Perm()&0o111 != 0 {
		return SkipExecutable, true
	}
	return "", false
}

// excluded reports whether rel is on the credential/repository exclusion list.
func excluded(rel string) bool {
	for _, segment := range strings.Split(rel, "/") {
		if excludedDirs[segment] {
			return true
		}
	}
	base := strings.ToLower(path.Base(rel))
	if excludedExactNames[base] {
		return true
	}
	for _, prefix := range excludedPrefixes {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	for _, suffix := range excludedSuffixes {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// checkRelPath fails closed on any relative path a transfer must not carry.
//
// Traversal and absolute paths are the harness rules. The three extra ones are
// this implementation's: a name is rejected when it is not valid UTF-8, and when
// it contains a newline, a carriage return or a backslash — those are exactly
// the bytes GNU coreutils escapes in `sha256sum --check` input, so admitting
// them would make the guest-side verification ambiguous rather than merely ugly.
func checkRelPath(rel string) error {
	if rel == "" {
		return fmt.Errorf("empty relative path")
	}
	if strings.HasPrefix(rel, "/") {
		return fmt.Errorf("absolute path")
	}
	if !utf8.ValidString(rel) {
		return fmt.Errorf("path is not valid UTF-8")
	}
	if strings.ContainsAny(rel, "\n\r\\\x00") {
		return fmt.Errorf("path contains a byte the checksum protocol cannot represent")
	}
	if path.Clean(rel) != rel || rel == "." {
		return fmt.Errorf("path is not canonical")
	}
	for _, segment := range strings.Split(rel, "/") {
		if segment == ".." {
			return fmt.Errorf("path traverses above the root")
		}
	}
	return nil
}

// relPath returns the slash-separated path of full relative to root.
func relPath(root, full string) (string, error) {
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return "", fmt.Errorf("resolve a path under the source root")
	}
	return filepath.ToSlash(rel), nil
}

func validateRoot(root string, requirePrivateEmpty bool) error {
	info, err := os.Lstat(root)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return privateError("invalid directory root")
	}
	if !requirePrivateEmpty {
		return nil
	}
	if info.Mode().Perm()&0o077 != 0 {
		return privateError("directory is not private")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		return privateError("directory is not empty")
	}
	return nil
}

func syncStagingTree(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, full)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		dir, err := os.Open(dirs[i])
		if err != nil {
			return err
		}
		syncErr := dir.Sync()
		closeErr := dir.Close()
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

type transferError struct {
	message string
}

func (e *transferError) Error() string { return "brain transfer: " + e.message }

func privateError(message string) error {
	return &transferError{message: message}
}
