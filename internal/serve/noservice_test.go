package serve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
)

// processBackend is a backend that declares no guest service — the shape a
// per-session CLI agent has. It exists here to prove serve's answers about such
// a backend without waiting for a real one to be implemented.
type processBackend struct{}

func (processBackend) Identity() backend.Identity {
	return backend.Identity{Name: "process-only", GuestUser: "agent"}
}
func (processBackend) RequiredPaths() []backend.PathSpec                          { return nil }
func (processBackend) VerifyIdentity(context.Context, backend.StepRunner) error   { return nil }
func (processBackend) VerifyMembership(context.Context, backend.StepRunner) error { return nil }
func (processBackend) VerifyIsolation(context.Context, backend.StepRunner) error  { return nil }
func (processBackend) Install(context.Context, backend.StepRunner) error          { return nil }
func (processBackend) VerifyVersion(context.Context, backend.StepRunner) error    { return nil }
func (processBackend) VerifyGuardrails(context.Context, backend.StepRunner) error { return nil }
func (processBackend) ProbeAuth(context.Context, backend.StepRunner) error        { return nil }
func (processBackend) Registry() backend.ProjectRegistry                          { return nil }
func (processBackend) Service() *backend.ServiceSpec                              { return nil }
func (processBackend) Session() *backend.SessionSpec                              { return nil }

// TestStatusOnAServicelessBackendAnswersInsteadOfFailing pins the honesty rule
// in the direction that is easy to get wrong. A backend that runs no service is
// not an unready one, and reporting it as unready would teach an operator to
// ignore the state that means a real backend has died.
//
// It must also touch the guest for nothing: there is no unit to ask systemd
// about and no endpoint to probe, so asking would be inventing work to justify
// an answer already known from the declaration.
func TestStatusOnAServicelessBackendAnswersInsteadOfFailing(t *testing.T) {
	f := newFake(serveEnv{})
	a := New(f, processBackend{})

	rep, err := a.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: unexpected error: %v", err)
	}
	if rep.ServiceDeclared {
		t.Error("ServiceDeclared = true for a backend that declares no service")
	}
	if rep.Ready || rep.Installed || rep.Active {
		t.Errorf("report claims service state for a backend with no service: %+v", rep)
	}
	if n := len(f.joinedCalls()); n != 0 {
		t.Errorf("Status ran %d guest commands for a backend with no service, want 0", n)
	}
}

// TestManagingAServicelessBackendIsAPreconditionError pins the other half: a
// query has an answer, but a request to install, start, stop or read logs from
// a service that was never declared is an operator mistake and fails closed
// with the backend named, because "which backend am I on" is the next question.
func TestManagingAServicelessBackendIsAPreconditionError(t *testing.T) {
	f := newFake(serveEnv{})
	a := New(f, processBackend{})

	ops := map[string]func() error{
		"install": func() error { _, err := a.Install(context.Background()); return err },
		"start":   func() error { _, err := a.Start(context.Background()); return err },
		"restart": func() error { _, err := a.Restart(context.Background()); return err },
		"stop":    func() error { _, err := a.Stop(context.Background()); return err },
		"logs":    func() error { _, err := a.Logs(context.Background(), 10); return err },
	}
	for name, run := range ops {
		err := run()
		if err == nil {
			t.Errorf("%s: no error for a backend with no service", name)
			continue
		}
		var serr *Error
		if !errors.As(err, &serr) {
			t.Errorf("%s: error is not a *serve.Error: %v", name, err)
			continue
		}
		if serr.Kind != KindNoService {
			t.Errorf("%s: Kind = %q, want %q", name, serr.Kind, KindNoService)
		}
		if !strings.Contains(serr.Error(), "process-only") {
			t.Errorf("%s: error does not name the backend: %v", name, serr)
		}
	}
	if n := len(f.joinedCalls()); n != 0 {
		t.Errorf("refusing ran %d guest commands, want 0", n)
	}
}
