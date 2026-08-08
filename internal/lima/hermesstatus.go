package lima

import "github.com/wzslr321/torio/internal/backend"

// The guest files whose modification time is evidence Hermes did work.
//
// They are the agent database and its write-ahead log, which SQLite touches
// when a write lands and leaves alone otherwise — checked on a live box, where
// the log did not move across a quiet interval and does move when the agent
// runs. That is what makes them evidence rather than a heartbeat: a file
// rewritten on a timer would report every idle box as having just progressed.
//
// The database itself is included because the log is checkpointed back into it
// and then truncated, so neither file alone covers the whole history of writes.
const (
	hermesStateDB    = HermesProfilePath + "/state.db"
	hermesStateDBWAL = HermesProfilePath + "/state.db-wal"
)

// Status declares what a poll may read on a Hermes box.
//
// No session process is declared, and that is a statement about how this
// backend is shaped rather than a missing probe. Hermes runs one long-lived
// service and holds its sessions inside it, as rows in a database; there is no
// process per session to find, and the service's own liveness is a different
// question that `torio serve status` already answers from systemd and the
// readiness endpoint. Reporting the service here as though it were a session
// would put a number on the row that never changes and means something other
// than the same number on a box running a process-per-session backend.
//
// No waiting marker is declared either. Hermes knows truthfully when it is
// blocked on an approval — the predicate exists — but it lives in the memory of
// the running process and is written nowhere a poll can read. Until Hermes
// exports it, the field is unknown, which is the honest answer: an operator
// told an agent is not waiting stops looking at it.
func (hermesBackend) Status() *backend.StatusSpec {
	return &backend.StatusSpec{
		ProgressPaths: []string{hermesStateDB, hermesStateDBWAL},
	}
}
