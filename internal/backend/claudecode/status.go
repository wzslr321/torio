package claudecode

import (
	"path"

	"github.com/wzslr321/torio/internal/backend"
)

// sessionProcess is the name a Claude Code session appears under in the guest's
// process table.
//
// It is derived from commandPath rather than written out, because that is the
// path a session is launched through — the root-owned session helper runs
// exactly it — and the kernel takes the process name from the path given to
// exec, not from the pinned binary the symlink resolves to. Deriving it means
// repointing the command path cannot leave this declaration matching a name
// nothing runs under.
//
// It is well inside the fifteen characters the kernel keeps, which a longer
// name would silently be truncated to.
var sessionProcess = path.Base(commandPath)

// Status declares what a poll may read on a Claude Code box.
//
// Sessions are processes and nothing else, which is the whole of what this
// backend leaves behind: it writes no roster, keeps no pid file, and its
// sessions directory is empty between sessions. That is not a gap in the
// probe — a live process is a stronger fact than any record, because it cannot
// outlive the thing it describes.
//
// No progress path is declared, and the omission is the honest answer rather
// than an oversight. The evidence this backend cannot help producing is its
// per-session transcript, which lives at a path named after the project and the
// session id, so a fixed declaration cannot point at it. The file that does sit
// at a fixed path, `history.jsonl`, moves when a prompt is submitted and not
// while one is being worked on — which is exactly the "last message" reading
// ADR-0010 refuses, because it reports a busy agent as dead throughout a long
// tool call. A session's own age already answers the question that reading
// would have answered badly.
func (claudeBackend) Status() *backend.StatusSpec {
	return &backend.StatusSpec{
		SessionProcess: sessionProcess,
		WaitingMarker:  true,
	}
}
