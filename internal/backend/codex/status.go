package codex

import (
	"path"

	"github.com/wzslr321/torio/internal/backend"
)

// sessionProcess is the name a session reports in the guest's process table. It
// is derived from the command path rather than written twice, so repointing the
// stable name cannot orphan this declaration. The kernel truncates the name to
// fifteen characters, and this one is well under.
var sessionProcess = path.Base(commandPath)

// Status declares what a poll may ask this backend.
//
// Liveness is the process, and waiting is the marker its managed hooks write.
// Progress is declared as absent, and that is a finding rather than an omission:
// Codex keeps per-session rollout files under a dated directory, so there is no
// fixed path to watch, and the one fixed file it does keep moves when a prompt is
// submitted. Watching that would report a box as dead for the whole of a long
// tool call, which is precisely when an operator is looking to see whether
// anybody needs them. ADR-0017 rejected that reading for the other backend and it
// is rejected here for the same reason.
func (codexBackend) Status() *backend.StatusSpec {
	return &backend.StatusSpec{
		SessionProcess: sessionProcess,
		WaitingMarker:  true,
	}
}
