package codex

import (
	"context"

	"github.com/wzslr321/torio/internal/backend"
)

// VerifyGuardrails checks the files that shape this backend's own behaviour but
// are honoured by it rather than enforced against it.
//
// Nothing is checked yet: the install proven so far is the binary and the
// identity that runs it, and a check recorded here would have to name a file
// this package does not yet write. An absent check is a check nobody can read a
// false pass out of.
func (codexBackend) VerifyGuardrails(context.Context, backend.StepRunner) error { return nil }

// Session is nil: no session helper is installed yet, and a declared session
// whose root-owned entry point does not exist would make bootstrap prove a file
// nothing put there.
func (codexBackend) Session() *backend.SessionSpec { return nil }

// Status is nil: this backend answers no liveness question yet. `torio status`
// reports that rather than running a guest command to discover what it was
// already told.
func (codexBackend) Status() *backend.StatusSpec { return nil }

// BrainSkill is empty: nothing is installed into the skill root yet, so `brain
// status` reports no retrieval surface rather than reporting one as missing.
func (codexBackend) BrainSkill() backend.BrainSkill { return backend.BrainSkill{} }
