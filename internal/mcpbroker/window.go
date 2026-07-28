package mcpbroker

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Write windows.
//
// A tool marked `writes` in a policy document is granted, but a grant alone is
// not enough to call it: the operator must also have opened a write window for
// that service, and windows expire on their own.
//
// The shape is deliberately the same one `torio project shell` already uses for
// Git. Write capability exists inside an operator-initiated, time-bounded
// window and ends by itself; nothing the agent does can extend it. That is what
// answers the attack this mechanism exists for — a poisoned page telling the
// agent to publish something it read. The instruction still arrives, and the
// agent may still follow it, but at three in the morning there is no window and
// the call is refused and recorded.
//
// The window is a file rather than a conversation with the daemon because a
// file survives a daemon restart, can be inspected by an operator with `ls`,
// and needs no protocol. It lives in the broker's own home, which the agent
// identity cannot read or write and has no sudo to reach.

// WriteWindowDirName is the broker-home subdirectory holding one file per
// service with an open window. The file's name is the service; its content is
// the RFC 3339 instant the window closes.
const WriteWindowDirName = "write-windows"

// DefaultWriteWindow is how long `torio mcp allow-write` opens a window for
// when the operator does not say. Long enough not to fight the tool during a
// piece of work, short enough that an injected instruction arriving later finds
// the door shut.
const DefaultWriteWindow = 15 * time.Minute

// maxWriteWindowFileBytes bounds what is read from a window file. The content
// is one timestamp; anything larger is not a window this package wrote.
const maxWriteWindowFileBytes = 64

// WriteWindow is the state of one service's write window. The zero value is
// closed, which is what every failure path returns.
type WriteWindow struct {
	Open  bool
	Until time.Time
}

// ReadWriteWindow reports whether service currently has an open write window.
//
// It returns no error, and that is a decision rather than an omission. A caller
// holding both "closed" and "could not tell" would eventually have to choose
// which of them means deny, and the only safe answer is that they are the same
// answer. Every failure — missing directory, missing file, unreadable file,
// content this package did not write, a name that is not a valid service —
// yields a closed window.
//
// Expiry is exclusive: a window whose instant equals now is closed. A boundary
// that stays open at its own deadline is a window that never quite ends.
func ReadWriteWindow(dir, service string, now time.Time) WriteWindow {
	if err := ValidateServiceName(service); err != nil {
		// The name reaches here from a client request. Validating it is what
		// keeps the lookup inside dir instead of at any path the caller names.
		return WriteWindow{}
	}

	f, err := os.Open(filepath.Join(dir, service))
	if err != nil {
		return WriteWindow{}
	}
	defer f.Close()

	buf := make([]byte, maxWriteWindowFileBytes+1)
	n, _ := f.Read(buf)
	if n <= 0 || n > maxWriteWindowFileBytes {
		return WriteWindow{}
	}

	until, err := time.Parse(time.RFC3339, strings.TrimSpace(string(buf[:n])))
	if err != nil {
		return WriteWindow{}
	}
	if !now.Before(until) {
		return WriteWindow{Until: until}
	}
	return WriteWindow{Open: true, Until: until}
}

// FormatWriteWindow renders the instant a window closes, in the one form
// ReadWriteWindow parses. Writers must use it rather than formatting their own,
// so the producer and the consumer cannot drift apart.
func FormatWriteWindow(until time.Time) string {
	return until.UTC().Format(time.RFC3339) + "\n"
}
