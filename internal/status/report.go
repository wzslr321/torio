// Package status answers one question across every box Torio owns: which
// agents exist, which are working, which are waiting on a human, and which are
// gone (ADR-0017).
//
// It is a poll of facts, not a cache of events. What it reads are things a
// backend cannot help producing while it runs — a process that exists, a file
// whose modification time moved — rather than a document that reports its own
// state, because the one transition a status surface exists to show is the one
// that emits no event: no backend announces its own death. A document written
// by hooks would say "running" forever after a crash, and a poll would
// faithfully relay the lie. Polling is only honest when what is polled cannot
// fail to be updated.
//
// Everything this package renders is either an identifier, an enumerated value,
// or a number. That is deliberate and load-bearing: its output reaches a
// terminal that interprets escape sequences, and the guests it reads from run
// agents that write their own prose.
package status

// FieldState is the marker every field carries, so that a renderer can tell
// three different silences apart.
//
// The distinction is the whole point of the schema. On a host running several
// backends most of the surface is "not knowable here", and an operator who
// cannot tell that from "all quiet" learns to ignore the surface — which is the
// failure mode a status line exists to prevent. It is the vocabulary
// `torio backend status` already uses for credential state, for the same
// reason.
type FieldState string

const (
	// NotApplicable means the backend declares no such capability. Nothing was
	// asked, and nothing should be shown as an answer.
	NotApplicable FieldState = "not-applicable"
	// Unknown means the capability is declared but could not be proven right
	// now: a box that could not be reached, output that could not be parsed, a
	// marker too old to trust. It is never a guess in either direction.
	Unknown FieldState = "unknown"
	// Known means the payload fields carry a proven answer.
	Known FieldState = "known"
)

// Report is one poll: every Torio-owned box on the host, name-ordered.
type Report struct {
	Instances []Instance `json:"instances"`
}

// Instance is one box and what could be proven about the agent on it.
type Instance struct {
	// Name is the Lima instance name.
	Name string `json:"instance"`
	// Box is the state Lima reports: running, stopped, broken, or Lima's own
	// unknown. It is a host-side fact that costs no guest command, so it is the
	// one field that is always answered.
	Box string `json:"box"`
	// Backend is which agent this box was provisioned for, read from the
	// document the box owns.
	Backend BackendField `json:"backend"`
	// Session is the backend's live processes from the guest process table.
	Session SessionField `json:"session"`
	// Waiting is whether an agent here is blocked on a human.
	Waiting WaitingField `json:"waiting"`
	// Progress is the last moment this box provably did any work.
	Progress ProgressField `json:"last_progress"`
}

// BackendField is the backend an instance declares.
//
// It is never not-applicable: every box runs exactly one backend (ADR-0009).
// Unknown means the box's own document could not be read or names a backend
// this binary does not have — which is a fact about this host, not a reason to
// stop reporting the other boxes.
type BackendField struct {
	State FieldState `json:"state"`
	Name  string     `json:"name,omitempty"`
}

// SessionField is what is running.
//
// Sessions is always an array, never null, so a shell recipe can count it
// without first testing the state. When the state is not Known it is empty and
// the state says why; an empty array with state Known is the proven answer that
// nothing is running.
type SessionField struct {
	State    FieldState `json:"state"`
	Sessions []Session  `json:"sessions"`
}

// Session is one live agent process.
//
// It carries no title, no prompt and no working directory. A session title is
// prose an agent wrote, and this document is rendered into a terminal.
type Session struct {
	// PID is the process, as the backend recorded it and the guest confirmed.
	PID int `json:"pid"`
	// StartedAt is when the process actually started, derived from the guest's
	// own clock rather than from what the backend claimed. RFC 3339, UTC.
	StartedAt string `json:"started_at"`
	// AgeSeconds is how long it has been running, measured on the guest so no
	// clock skew between host and box can invent a value.
	AgeSeconds int64 `json:"age_seconds"`
}

// WaitingField is whether a human is being waited on.
//
// This is the field the whole surface exists for, and the only one carried by
// an event rather than read as a fact: an agent is waiting only at the moment
// it asks, so there is nothing continuous to read. It is therefore ranked below
// liveness — a marker whose process is gone reports not-waiting — and expires,
// so a marker nobody cleared becomes unknown rather than a plea that never ends.
type WaitingField struct {
	State   FieldState `json:"state"`
	Waiting bool       `json:"waiting"`
	// Waits identifies every live session currently asking for attention. It is
	// always an array so a renderer can count it directly.
	Waits []Wait `json:"waits"`
}

// Wait is one live session represented by the fixed waiting document. Every
// field is an identifier, enum or number; hook payload prose has no place in
// the status schema.
type Wait struct {
	SessionID  string `json:"session_id,omitempty"`
	PID        int    `json:"pid,omitempty"`
	AgeSeconds int64  `json:"age_seconds"`
}

// ProgressField is the newest modification time among the files a backend
// cannot help writing while it works.
//
// It is deliberately not "when the last message was recorded": a backend that
// writes a row per turn reads as dead throughout a long tool call, which is
// precisely when an operator is watching to see whether they are needed.
type ProgressField struct {
	State FieldState `json:"state"`
	// At is the modification time, RFC 3339, UTC, on the guest's clock.
	At string `json:"at,omitempty"`
	// AgeSeconds is how long ago that was, measured on the guest.
	AgeSeconds int64 `json:"age_seconds,omitempty"`
}

// unknownSession, unknownWaiting and unknownProgress are the answers a poll
// gives when it could not prove one. They are constructors rather than values
// because Sessions must be an empty array and not a shared slice.
func unknownSession() SessionField   { return SessionField{State: Unknown, Sessions: []Session{}} }
func unknownWaiting() WaitingField   { return WaitingField{State: Unknown, Waits: []Wait{}} }
func unknownProgress() ProgressField { return ProgressField{State: Unknown} }
