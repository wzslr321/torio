package codex

import (
	"context"

	"github.com/wzslr321/torio/internal/backend"
)

// credentialPath is where Codex stores its own credential, under the agent's
// profile. Torio never reads it, never copies it and never writes it: it only
// asks whether it is there.
//
// One file answers for both ways in. A ChatGPT sign-in and an API key land in
// the same document, so the probe does not have to know which the operator
// chose, and adding a third way in later does not change what this check means.
const credentialPath = ProfilePath + "/auth.json"

// ProbeAuth reports whether the box holds a credential of its own.
//
// It is offline by construction. Asking the provider would turn `vm bootstrap`
// into a command that reaches a third party, and it would answer a question
// Torio has no business asking: whether a grant is still valid is between the
// operator and whoever issued it.
//
// It never fails a run. A box has to bootstrap before anyone can log in to it,
// so an absent credential is the expected state of a freshly built guest rather
// than a defect in it. The check is recorded either way, because "this box is
// not logged in" is exactly what an operator needs told at the end of a
// bootstrap rather than discovered at the start of a session.
//
// The credential lives under the agent's own uid, which means the agent can read
// what it is authenticated with. That is an accepted, documented hole of the
// same class as the other backends' (SECURITY.md), and it is not fixed here.
// What the box changes is the blast radius: this grant belongs to the box and
// can be revoked without touching the operator's own.
func (codexBackend) ProbeAuth(ctx context.Context, r backend.StepRunner) error {
	const name = authCheck
	res, err := r.Probe(ctx, name, "sudo", "-n", "-u", User, "--", "test", "-s", credentialPath)
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		r.Record(name, true, backend.CredentialPresent)
		return nil
	}
	r.Record(name, true, backend.CredentialAbsent)
	return nil
}
