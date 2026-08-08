// Package brain initializes and verifies Torio's private, Markdown-first Second
// Brain on the native filesystem of the managed Lima guest.
package brain

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/lima"
)

const (
	// Path is the canonical V1 Second Brain path.
	Path = lima.HermesBrainPath
	// ProjectSlug is the stable Hermes Project identifier.
	ProjectSlug = "second-brain"
	// ProjectName is the human-facing Hermes Project name.
	ProjectName = "Second Brain"
	// SkillName is the directory name of the global retrieval skill. Every
	// backend Torio supports discovers skills by walking a root recursively and
	// reading each SKILL.md, so one installed copy is visible in every session
	// and every working directory. There is no project-local skills directory.
	SkillName = backend.BrainSkillName
	// SkillCategory is the directory the skill is grouped under, and it is a
	// deliberate choice rather than a tidy-up.
	//
	// Hermes has no skill routing: it renders a static, alphabetically ordered
	// index of `name: description` lines into the stable system prompt, and the
	// model picks by calling skill_view(name). Two properties of that index
	// decide how visible a skill is, and Torio was losing on both. Categories
	// render in sorted order, and a top-level skill is its own category — so
	// `torio-brain` rendered 21st of 23, second from last. A category may also
	// carry a DESCRIPTION.md whose text is NOT subject to the 60-character cap
	// that truncates skill descriptions, and a top-level skill cannot have one.
	//
	// The bundled note-taking/obsidian skill sorts 15th and adds 133 uncapped
	// characters through its category.
	//
	// "brain" sorts near the front and gives the skill a category description.
	// Neither is enforcement — see EnvironmentHint for what carries the rule
	// when the model does not load this skill at all.
	SkillCategory = "brain"
	// SkillCategoryPath is the guest directory for the skill's category.
	SkillCategoryPath = lima.HermesProfilePath + "/skills/" + SkillCategory
	// SkillCategoryFilePath holds the category description. Hermes reads the
	// text from this file's YAML frontmatter `description` key, not its body.
	SkillCategoryFilePath = SkillCategoryPath + "/DESCRIPTION.md"
	// SkillPath is the guest directory that holds the retrieval skill.
	//
	// Torio writes the file directly rather than going through `hermes skills`
	// or the skill_manage tool. Both alternatives were rejected: they stamp the
	// skill `created_by: agent` in the usage record, which is exactly the marker
	// the skill curator uses to decide what it may prune, and a mandatory V1
	// product surface must not be prunable. The cost of a direct write is that
	// the per-process skill prompt cache is not invalidated, so `brain status`
	// says so instead of pretending the skill is live in open sessions.
	SkillPath = SkillCategoryPath + "/" + SkillName
	// SkillFilePath is the only file Torio installs under SkillPath.
	SkillFilePath = SkillPath + "/SKILL.md"
	// legacySkillPath is where releases before the category move installed the
	// skill. Removing it is not tidiness: skill_view collects every candidate
	// matching a name and, on more than one, refuses outright with "Ambiguous
	// skill name … Refusing to guess" rather than picking either. Leaving the
	// old copy behind would therefore break retrieval completely — a worse
	// outcome than never having moved it.
	legacySkillPath = lima.HermesProfilePath + "/skills/" + SkillName

	stagingPath = lima.HermesHome + "/.torio-brain-staging"
	// skillStagingPath is deliberately outside the skill discovery root so a
	// half-written payload can never be walked as a skill.
	skillStagingPath = lima.HermesHome + "/.torio-brain-skill-staging"
	lockPath         = lima.HermesHome + "/.torio-brain-init.lock"
	staleLockAge     = "10"

	// issueSkillDrift is the machine-readable issue string for a retrieval skill
	// that does not match what Torio ships. Named once so the writer and the
	// repair that clears it cannot drift apart.
	issueSkillDrift = "retrieval_skill_drift"
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

// retrievalSkill returns the backend's skill payload and its content digest.
// The digest is what makes installation idempotent and drift detectable without
// ever reading Brain content: Torio compares the guest file's sha256 against
// this, never the file's bytes.
//
// The payload comes from the backend rather than from this package, because a
// retrieval skill names the tools one agent has and the vault path one identity
// owns. This package installs it and verifies it; it does not know what it says.
func (m *Manager) retrievalSkill() ([]byte, string, error) {
	return declaredPayload(m.backend().BrainSkill().Payload)
}

// retrievalCategory returns the backend's category description and its digest.
// It is installed and drift-checked exactly like the skill payload: the
// category line is a product surface too, and an operator who edits it changes
// what every session is told about the vault.
func (m *Manager) retrievalCategory() ([]byte, string, error) {
	return declaredPayload(m.backend().BrainSkill().CategoryPayload)
}

// declaredPayload digests what a backend declared. An empty payload is an error
// rather than an empty install: reaching here means the backend declared a
// skill root, and writing a zero-byte SKILL.md into it would leave the agent a
// skill that says nothing where the report claims one is installed.
func declaredPayload(payload []byte) ([]byte, string, error) {
	if len(payload) == 0 {
		return nil, "", errors.New("backend declares no payload")
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
	// SkillNotApplicable means the backend discovers no skills, so there is
	// nowhere to install a retrieval surface and nothing was expected. It is
	// deliberately not "not_installed": that would report a missing thing where
	// nothing is missing, and an operator who learns to ignore it would ignore
	// the real one too.
	SkillNotApplicable SkillState = "not_applicable"
	SkillNotInstalled  SkillState = "not_installed"
	SkillInstalled     SkillState = "installed"
	SkillDrift         SkillState = "drift"
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
	// SkillPath is the guest file the retrieval skill is installed at, empty
	// when the backend declares none. It is reported rather than derived by the
	// caller because the path is the backend's, and an operator told to look at
	// one backend's path while running another has been told a falsehood by a
	// command whose whole job is to say what is true.
	SkillPath string
	Issues    []string
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
