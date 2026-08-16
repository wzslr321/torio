// Package brain initializes and verifies Torio's private, Markdown-first Second
// Brain on the native filesystem of the managed Lima guest.
package brain

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"

	"github.com/wzslr321/torio/internal/backend"
)

const (
	// SkillName is the directory name of the global retrieval skill. Every
	// backend Torio supports discovers skills by walking a root recursively and
	// reading each SKILL.md, so one installed copy is visible in every session
	// and every working directory. There is no project-local skills directory.
	SkillName = backend.BrainSkillName

	staleLockAge = "10"

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
// verify the guest file; it cannot observe whether a running backend has
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
	State            State
	Path             string
	PathExists       bool
	PathSecure       bool
	NativeFilesystem bool
	FSType           string
	Owner            string
	Group            string
	Mode             string
	ManagedScaffold  bool
	GitState         GitState
	GitHasRemote     bool
	MarkdownFiles    int
	AttachmentFiles  int
	TotalBytes       int64
	SkillState       SkillState
	// SkillPath is the guest file the retrieval skill is installed at, empty
	// when the backend declares none. It is reported rather than derived by the
	// caller because the path is the backend's, and an operator told to look at
	// one backend's path while running another has been told a falsehood by a
	// command whose whole job is to say what is true.
	SkillPath string
	// HubRefKnown, AheadOfHub and BehindHub say how far this replica is from
	// the one Second Brain on the host (ADR-0025). They are facts about where
	// this box stands, never drift in it: a vault can be perfectly healthy and
	// simply not level with the rest yet, and reporting that as damage would
	// make every reconciliation look like a repair. A box that has never
	// reconciled has nothing to compare with, which HubRefKnown says rather
	// than leaving two zeroes to be read as agreement.
	HubRefKnown bool
	AheadOfHub  int
	BehindHub   int
	Issues      []string
}

// InitReport distinguishes a fresh scaffold from an idempotent verification or
// recovery of an already-promoted scaffold whose project registration failed.
// SkillUpdated reports whether this run actually wrote the retrieval skill
// payload; a backend may cache the skill prompt per process, so a write means
// existing sessions still will not see the skill until they are restarted.
type InitReport struct {
	Created      bool
	SkillUpdated bool
	Status       StatusReport
}
