package wizard

import (
	"testing"

	"github.com/wzslr321/torio/internal/lima"
)

// hermesFacts is a fully set up Hermes box: a service backend with no auth
// check of its own. Each test below breaks exactly one fact.
func hermesFacts() Facts {
	return Facts{
		Box:             lima.StateRunning,
		Bootstrapped:    true,
		Credential:      CredentialNotApplicable,
		ServiceDeclared: true,
		ServeInstalled:  true,
		ServeRunning:    true,
		BrainReady:      true,
		ProjectCount:    1,
	}
}

// claudeFacts is a fully set up Claude Code box: a session backend that holds a
// credential and installs no service.
func claudeFacts() Facts {
	return Facts{
		Box:          lima.StateRunning,
		Bootstrapped: true,
		Credential:   CredentialPresent,
		BrainReady:   true,
		ProjectCount: 1,
	}
}

func TestNextWalksTheSetupInOrder(t *testing.T) {
	tests := []struct {
		name  string
		facts Facts
		want  Step
	}{
		{
			name:  "no box yet",
			facts: Facts{Box: lima.StateNotFound},
			want:  StepVMInit,
		},
		{
			name:  "box exists but is stopped",
			facts: Facts{Box: lima.StateStopped},
			want:  StepVMStart,
		},
		{
			name:  "running box that never bootstrapped",
			facts: Facts{Box: lima.StateRunning},
			want:  StepBootstrap,
		},
		{
			name: "bootstrapped box whose credential is absent",
			facts: Facts{
				Box:          lima.StateRunning,
				Bootstrapped: true,
				Credential:   CredentialAbsent,
			},
			want: StepBackendLogin,
		},
		{
			name: "service backend with no unit installed",
			facts: Facts{
				Box:             lima.StateRunning,
				Bootstrapped:    true,
				Credential:      CredentialNotApplicable,
				ServiceDeclared: true,
			},
			want: StepServeInstall,
		},
		{
			name: "service backend whose installed unit is not running",
			facts: Facts{
				Box:             lima.StateRunning,
				Bootstrapped:    true,
				Credential:      CredentialNotApplicable,
				ServiceDeclared: true,
				ServeInstalled:  true,
			},
			want: StepServeStart,
		},
		{
			name: "running service backend without a brain",
			facts: Facts{
				Box:             lima.StateRunning,
				Bootstrapped:    true,
				Credential:      CredentialNotApplicable,
				ServiceDeclared: true,
				ServeInstalled:  true,
				ServeRunning:    true,
			},
			want: StepBrainInit,
		},
		{
			name: "brain ready but no projects registered",
			facts: func() Facts {
				f := hermesFacts()
				f.ProjectCount = 0
				return f
			}(),
			want: StepProjectAdd,
		},
		{
			name:  "hermes fully set up",
			facts: hermesFacts(),
			want:  StepDone,
		},
		{
			name:  "claude code fully set up",
			facts: claudeFacts(),
			want:  StepDone,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Next(tc.facts); got != tc.want {
				t.Fatalf("Next() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A box Lima cannot run is a state of its own, not a reason to suggest starting
// it. Routing it to StepVMStart would loop an operator through a command that
// cannot succeed, which is the failure the explicit state exists to prevent.
func TestNextRefusesToTreatAnUnusableBoxAsStartable(t *testing.T) {
	for _, state := range []lima.State{lima.StateBroken, lima.StateUnknownLima} {
		t.Run(string(state), func(t *testing.T) {
			if got := Next(Facts{Box: state}); got != StepBoxUnusable {
				t.Fatalf("Next() = %q, want %q", got, StepBoxUnusable)
			}
		})
	}
}

// Only a proven-absent credential sends an operator to a login. An unprovable
// one is not evidence of a logged-out box (ADR-0009: an unproven check is never
// reported as a state), and pushing a login on no evidence teaches the operator
// to ignore the step that means it.
func TestNextDemandsLoginOnlyForAProvenAbsentCredential(t *testing.T) {
	base := Facts{Box: lima.StateRunning, Bootstrapped: true, BrainReady: true, ProjectCount: 1}

	base.Credential = CredentialAbsent
	if got := Next(base); got != StepBackendLogin {
		t.Fatalf("absent credential: Next() = %q, want %q", got, StepBackendLogin)
	}

	for _, state := range []string{CredentialUnknown, CredentialPresent, CredentialNotApplicable} {
		base.Credential = state
		if got := Next(base); got == StepBackendLogin {
			t.Fatalf("credential %q must not route to %q", state, StepBackendLogin)
		}
	}
}

// A backend that installs no service must never be sent through serve steps.
// Claude Code declares no unit, so an unset ServeInstalled is not a missing
// install: there is nothing to install.
func TestNextSkipsServiceStepsForABackendWithoutAService(t *testing.T) {
	f := claudeFacts()
	f.BrainReady = false
	f.ServeInstalled = false
	f.ServeRunning = false

	if got := Next(f); got != StepBrainInit {
		t.Fatalf("Next() = %q, want %q", got, StepBrainInit)
	}
}

func TestPlanOmitsStepsTheBackendCannotHave(t *testing.T) {
	hermes := stepsOf(Plan(hermesFacts()))
	if contains(hermes, StepBackendLogin) {
		t.Errorf("hermes plan contains %q, but the backend declares no auth check", StepBackendLogin)
	}
	for _, want := range []Step{StepServeInstall, StepServeStart} {
		if !contains(hermes, want) {
			t.Errorf("hermes plan is missing %q", want)
		}
	}

	claude := stepsOf(Plan(claudeFacts()))
	if !contains(claude, StepBackendLogin) {
		t.Errorf("claude code plan is missing %q", StepBackendLogin)
	}
	for _, unwanted := range []Step{StepServeInstall, StepServeStart} {
		if contains(claude, unwanted) {
			t.Errorf("claude code plan contains %q, but the backend declares no service", unwanted)
		}
	}
}

// The rail an operator reads must agree with the step the wizard is running.
func TestPlanMarksExactlyOneCurrentStepAndItIsTheNextOne(t *testing.T) {
	f := hermesFacts()
	f.BrainReady = false

	plan := Plan(f)
	var current []Step
	for _, st := range plan {
		if st.State == StageCurrent {
			current = append(current, st.Step)
		}
	}
	if len(current) != 1 {
		t.Fatalf("plan marks %d current stages, want exactly 1: %v", len(current), current)
	}
	if current[0] != Next(f) {
		t.Fatalf("plan marks %q current, but Next() = %q", current[0], Next(f))
	}
}

// Everything before the current step is settled, and nothing after it is.
func TestPlanOrdersDoneBeforeCurrentBeforePending(t *testing.T) {
	f := hermesFacts()
	f.ServeRunning = false

	seenCurrent := false
	for _, st := range Plan(f) {
		switch {
		case st.State == StageCurrent:
			seenCurrent = true
		case !seenCurrent && st.State != StageDone:
			t.Fatalf("stage %q before the current one is %q, want %q", st.Step, st.State, StageDone)
		case seenCurrent && st.State == StageDone:
			t.Fatalf("stage %q after the current one is marked %q", st.Step, StageDone)
		}
	}
	if !seenCurrent {
		t.Fatal("plan marks no current stage")
	}
}

// A finished setup still renders its rail, with every stage settled.
func TestPlanOfAFinishedSetupMarksEveryStageDone(t *testing.T) {
	for _, st := range Plan(hermesFacts()) {
		if st.State != StageDone {
			t.Fatalf("stage %q is %q, want %q", st.Step, st.State, StageDone)
		}
	}
}

// Every step the graph can return has operator-facing copy. A step whose title
// is empty renders as a blank instruction, which is worse than no wizard.
func TestEveryStepDescribesItself(t *testing.T) {
	steps := []Step{
		StepVMInit, StepVMStart, StepBoxUnusable, StepBootstrap, StepBackendLogin,
		StepServeInstall, StepServeStart, StepBrainInit, StepProjectAdd, StepDone,
	}
	for _, s := range steps {
		d := Describe(s)
		if d.Title == "" {
			t.Errorf("step %q has no title", s)
		}
		if d.Detail == "" {
			t.Errorf("step %q has no detail", s)
		}
	}
}

func stepsOf(plan []Stage) []Step {
	out := make([]Step, 0, len(plan))
	for _, st := range plan {
		out = append(out, st.Step)
	}
	return out
}

func contains(steps []Step, want Step) bool {
	for _, s := range steps {
		if s == want {
			return true
		}
	}
	return false
}
