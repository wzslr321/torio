package claudecode

import (
	"context"

	"github.com/wzslr321/torio/internal/backend"
)

// credentialPath is where Claude Code stores its own credential on Linux, under
// the agent's home. Torio never reads it, never copies it and never writes it —
// it only asks whether it is there.
const credentialPath = ProfilePath + "/.credentials.json"

// ProbeAuth reports whether the box holds a credential of its own.
//
// It is offline by construction. Asking the provider would turn `vm bootstrap`
// into a command that reaches a third party, and it would answer a question
// Torio has no business asking: whether a grant is still valid is between the
// operator and whoever issued it.
//
// It never fails a run. A box has to bootstrap before anyone can log in to it,
// so an absent credential is the expected state of a freshly built guest, not a
// defect in it. The check is recorded either way, because "this box is not
// logged in" is exactly the thing an operator needs told at the end of a
// bootstrap rather than discovered at the start of a session.
//
// The credential lives under the agent's own uid, which means the agent can
// read what it is authenticated with. That is an accepted, documented hole of
// the same class as the Hermes inference credential (SECURITY.md), and it is
// not fixed here. What the box does change is the blast radius of a compromise:
// this grant belongs to the box and can be revoked without touching the
// operator's own.
func (claudeBackend) ProbeAuth(ctx context.Context, r backend.StepRunner) error {
	const name = "claude_auth"
	res, err := r.Probe(ctx, name, "sudo", "-n", "-u", User, "--", "test", "-s", credentialPath)
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		r.Record(name, true, "credential present")
		return nil
	}
	r.Record(name, true, "credential absent; run `torio backend login`")
	return nil
}
