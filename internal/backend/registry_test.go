package backend

import (
	"context"
	"strings"
	"testing"
)

// stub is a minimal Backend for registry tests. It declares no capability at
// all, which is the shape the registry itself must be indifferent to.
type stub struct{ name string }

func (s stub) Identity() Identity                               { return Identity{Name: s.name} }
func (stub) RequiredPaths() []PathSpec                          { return nil }
func (stub) VerifyIdentity(context.Context, StepRunner) error   { return nil }
func (stub) VerifyMembership(context.Context, StepRunner) error { return nil }
func (stub) VerifyIsolation(context.Context, StepRunner) error  { return nil }
func (stub) Install(context.Context, StepRunner) error          { return nil }
func (stub) VerifyVersion(context.Context, StepRunner) error    { return nil }
func (stub) VerifyGuardrails(context.Context, StepRunner) error { return nil }
func (stub) ProbeAuth(context.Context, StepRunner) error        { return nil }
func (stub) Registry() ProjectRegistry                          { return nil }
func (stub) Service() *ServiceSpec                              { return nil }
func (stub) Session() *SessionSpec                              { return nil }
func (stub) ProvisionScript() string                            { return "" }
func (stub) BrainSkill() BrainSkill                             { return BrainSkill{} }

// TestLookupEmptyNameResolvesTheDefault pins the compatibility rule: a config
// document written before instances declared a backend names none, and such an
// instance is running Hermes. Resolving empty to anything else would re-point
// an existing box at a different agent on upgrade.
func TestLookupEmptyNameResolvesTheDefault(t *testing.T) {
	withRegistry(t, map[string]Backend{DefaultName: stub{DefaultName}})

	b, err := Lookup("")
	if err != nil {
		t.Fatalf("Lookup(\"\"): %v", err)
	}
	if got := b.Identity().Name; got != DefaultName {
		t.Fatalf("Lookup(\"\") = %q, want %q", got, DefaultName)
	}
}

// TestLookupUnknownNameFailsClosedAndNamesTheAlternatives proves an unknown
// backend is an error rather than a fallback. A config from a newer Torio that
// names a backend this build does not have must stop, not quietly run every
// command against a different agent than the document says.
func TestLookupUnknownNameFailsClosedAndNamesTheAlternatives(t *testing.T) {
	withRegistry(t, map[string]Backend{"hermes": stub{"hermes"}, "claude-code": stub{"claude-code"}})

	_, err := Lookup("gpt-whatever")
	if err == nil {
		t.Fatal("Lookup of an unknown backend returned no error")
	}
	for _, want := range []string{"gpt-whatever", "hermes", "claude-code"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// TestRegisterRejectsADuplicateName pins that wiring two backends under one
// name is a programmer error caught at startup, not a race over which one an
// instance gets.
func TestRegisterRejectsADuplicateName(t *testing.T) {
	withRegistry(t, map[string]Backend{"hermes": stub{"hermes"}})

	defer func() {
		if recover() == nil {
			t.Fatal("registering a duplicate name did not panic")
		}
	}()
	Register(stub{"hermes"})
}

// withRegistry swaps the process-wide registry for one test and restores it.
func withRegistry(t *testing.T, entries map[string]Backend) {
	t.Helper()
	mu.Lock()
	saved := registry
	registry = entries
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		registry = saved
		mu.Unlock()
	})
}
