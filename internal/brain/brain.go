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
	"embed"

	"github.com/wzslr321/torio/internal/lima"
)

const (
	// Path is the canonical V1 Second Brain path.
	Path = lima.HermesBrainPath
	// ProjectSlug is the stable Hermes Project identifier.
	ProjectSlug = "second-brain"
	// ProjectName is the human-facing Hermes Project name.
	ProjectName = "Second Brain"

	stagingPath  = lima.HermesHome + "/.torio-brain-staging"
	lockPath     = lima.HermesHome + "/.torio-brain-init.lock"
	staleLockAge = "10"
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

// SkillState is reserved for the Task 13 retrieval skill lifecycle.
type SkillState string

const (
	SkillNotInstalled SkillState = "not_installed"
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
type InitReport struct {
	Created bool
	Status  StatusReport
}
