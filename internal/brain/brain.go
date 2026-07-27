/*
 * AI-Provenance:
 *   model: Cursor Grok 4.5
 *   harness: Cursor
 *   plugins:
 *     - lean-ai-provenance
 *   skills:
 *     - mark-ai-provenance
 */
// Package brain initializes and verifies Torio's private, Markdown-first Second
// Brain on the native filesystem of the managed Lima guest.
package brain

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"

	"github.com/wzslr321/torio/internal/lima"
)

const (
	// Path is the canonical V1 Second Brain path.
	Path = lima.HermesBrainPath
	// ProjectSlug is the stable Hermes Project identifier.
	ProjectSlug = "second-brain"
	// ProjectName is the human-facing Hermes Project name.
	ProjectName = "Second Brain"
	// SkillName is the directory name of the global retrieval skill. Hermes
	// discovers skills by walking $HERMES_HOME/skills recursively and reading
	// each SKILL.md, so one installed copy is visible in every session and
	// every working directory. There is no project-local skills directory.
	SkillName = "torio-brain"
	// SkillPath is the guest directory that holds the retrieval skill.
	//
	// Torio writes the file directly rather than going through `hermes skills`
	// or the skill_manage tool. Both alternatives were rejected: they stamp the
	// skill `created_by: agent` in the usage record, which is exactly the marker
	// the skill curator uses to decide what it may prune, and a mandatory V1
	// product surface must not be prunable. The cost of a direct write is that
	// the per-process skill prompt cache is not invalidated, so `brain status`
	// says so instead of pretending the skill is live in open sessions.
	SkillPath = lima.HermesProfilePath + "/skills/" + SkillName
	// SkillFilePath is the only file Torio installs under SkillPath.
	SkillFilePath = SkillPath + "/SKILL.md"

	stagingPath = lima.HermesHome + "/.torio-brain-staging"
	// skillStagingPath is deliberately outside the skill discovery root so a
	// half-written payload can never be walked as a skill.
	skillStagingPath = lima.HermesHome + "/.torio-brain-skill-staging"
	lockPath         = lima.HermesHome + "/.torio-brain-init.lock"
	staleLockAge     = "10"

	skillTemplate = "templates/skill/SKILL.md"
)

var canonicalDirectories = []string{
	"inbox",
	"daily",
	"projects",
	"people",
	"meetings",
	"resources",
	"attachments",
}

var canonicalFiles = []string{
	"README.md",
	"AGENTS.md",
	"todo.md",
}

//go:embed templates/*.md
var scaffoldFS embed.FS

//go:embed templates/skill/SKILL.md
var skillFS embed.FS

// retrievalSkill returns the embedded skill payload and its content digest. The
// digest is what makes installation idempotent and drift detectable without ever
// reading Brain content: Torio compares the guest file's sha256 against this,
// never the file's bytes.
func retrievalSkill() ([]byte, string, error) {
	payload, err := skillFS.ReadFile(skillTemplate)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(payload)
	return payload, hex.EncodeToString(sum[:]), nil
}

// State is the aggregate health of the canonical Brain.
type State string

const (
	StateInitialized   State = "initialized"
	StateUninitialized State = "uninitialized"
	StateDrift         State = "drift"
)

// GitState is the bounded worktree status. It intentionally carries no paths.
type GitState string

const (
	GitMissing GitState = "missing"
	GitClean   GitState = "clean"
	GitDirty   GitState = "dirty"
)

// SkillState is the on-disk state of the global retrieval skill. Torio can only
// verify the guest file; it cannot observe whether a running Hermes backend has
// already loaded it, so callers must treat "installed" as "present and
// verified", not as "active in every open session".
type SkillState string

const (
	SkillNotInstalled SkillState = "not_installed"
	SkillInstalled    SkillState = "installed"
	SkillDrift        SkillState = "drift"
)

// StatusReport contains only derived metadata and aggregate counts. It never
// contains note names, relative paths, note content, or raw guest output.
type StatusReport struct {
	State             State
	Path              string
	PathExists        bool
	PathSecure        bool
	NativeFilesystem  bool
	FSType            string
	Owner             string
	Group             string
	Mode              string
	ManagedScaffold   bool
	GitState          GitState
	GitHasRemote      bool
	MarkdownFiles     int
	AttachmentFiles   int
	TotalBytes        int64
	ProjectRegistered bool
	ProjectConflict   bool
	SkillState        SkillState
	Issues            []string
}

// InitReport distinguishes a fresh scaffold from an idempotent verification or
// recovery of an already-promoted scaffold whose project registration failed.
// SkillUpdated reports whether this run actually wrote the retrieval skill
// payload; Hermes caches the skill prompt per backend process, so a write means
// existing sessions still will not see the skill until they are restarted.
type InitReport struct {
	Created      bool
	SkillUpdated bool
	Status       StatusReport
}
