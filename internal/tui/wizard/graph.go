// Package wizard derives first-run setup from what a box can be proven to be.
//
// The order it encodes is not new. It is the order the command surface already
// implies through the `next:` line each command prints, which no single place
// held: a bootstrap knew what follows a bootstrap, and nothing knew what
// follows a box that was never created. Holding the whole order in one function
// over one fact struct is what lets a screen say where an operator is rather
// than only what to type next, and lets the same answer drive both the wizard
// and the dashboard's guidance.
//
// Everything here is a pure function of Facts. Nothing in this package runs a
// command, reads a file, or holds a deadline; gathering the facts is the
// caller's job, and every step is derived from a fact that was proven rather
// than from a step the operator is assumed to have taken.
package wizard

import "github.com/wzslr321/torio/internal/lima"

// Step is one stage of setup, or the state that setup is finished.
type Step string

const (
	// StepVMInit is a host with no box for this backend yet.
	StepVMInit Step = "vm-init"
	// StepVMStart is a box that exists and is not running.
	StepVMStart Step = "vm-start"
	// StepBoxUnusable is a box Lima reports as broken or cannot describe. It is
	// a dead end for the wizard on purpose: no step it could offer would repair
	// the box, and offering one anyway would loop the operator.
	StepBoxUnusable Step = "box-unusable"
	// StepBootstrap is a running box whose guest postconditions are unproven.
	StepBootstrap Step = "bootstrap"
	// StepBackendLogin is a backend whose credential was proven absent.
	StepBackendLogin Step = "backend-login"
	// StepBrainInit is a box with no initialized Second Brain.
	StepBrainInit Step = "brain-init"
	// StepProjectAdd is a set-up box with nothing registered to work on.
	StepProjectAdd Step = "project-add"
	// StepDone is a box with nothing left that Torio can prove is missing.
	StepDone Step = "done"
)

// The credential vocabulary, which is the one `torio backend status` already
// answers in. "unknown" and "not-applicable" are different silences and the
// graph treats them differently from each other and from "absent".
const (
	CredentialPresent       = "present"
	CredentialAbsent        = "absent"
	CredentialUnknown       = "unknown"
	CredentialNotApplicable = "not-applicable"
)

// Facts is everything the graph needs, and nothing it could derive itself.
//
// Each field is an observation, not an assumption: the caller proves it by
// asking Lima, the guest, or the registry. A field left at its zero value is
// read as "not proven", which routes toward doing the work again rather than
// toward skipping it.
type Facts struct {
	// Box is the state Lima reports for this instance.
	Box lima.State
	// Bootstrapped is whether a verifying bootstrap passed every check.
	Bootstrapped bool
	// Credential is the backend's auth state in the vocabulary above. The empty
	// string is read as unknown, so a caller that could not ask is never taken
	// to have proven the credential absent.
	Credential string
	// BrainReady is whether the Second Brain is initialized and undrifted.
	BrainReady bool
	// ProjectCount is how many projects the registry holds for this instance.
	ProjectCount int
}

// Next answers the one thing to do now.
//
// The order is the dependency order of the work: nothing can be verified on a
// box that is not running, no credential can be granted to a guest that was
// never bootstrapped, and no agent can open a project on a box with no agent
// installed. A step whose capability the backend does not declare is skipped
// rather than reported unmet.
func Next(f Facts) Step {
	switch f.Box {
	case lima.StateNotFound:
		return StepVMInit
	case lima.StateBroken, lima.StateUnknownLima:
		return StepBoxUnusable
	case lima.StateStopped:
		return StepVMStart
	}
	if !f.Bootstrapped {
		return StepBootstrap
	}
	// Only a proven-absent credential is a reason to log in. Unknown means the
	// check could not be read, which is not evidence of a logged-out box.
	if f.Credential == CredentialAbsent {
		return StepBackendLogin
	}
	if !f.BrainReady {
		return StepBrainInit
	}
	if f.ProjectCount == 0 {
		return StepProjectAdd
	}
	return StepDone
}

// StageState is how far along one stage of the plan is.
type StageState string

const (
	StageDone    StageState = "done"
	StageCurrent StageState = "current"
	StagePending StageState = "pending"
)

// Stage is one step of the plan and how far along it is.
type Stage struct {
	Step  Step
	State StageState
}

// Plan is the whole route for this backend, so a screen can show where the
// operator is rather than only what is next. Steps the backend cannot have are
// absent from it entirely: a Claude Code box has no service to install, and a
// rail that listed one greyed out would describe a route that does not exist.
func Plan(f Facts) []Stage {
	steps := applicable(f)
	current := Next(f)

	// Everything up to the current step is settled. A finished setup has no
	// current step in the list, so every stage in it reads as done.
	out := make([]Stage, 0, len(steps))
	passed := false
	for _, s := range steps {
		state := StagePending
		switch {
		case s == current:
			state = StageCurrent
			passed = true
		case !passed:
			state = StageDone
		}
		out = append(out, Stage{Step: s, State: state})
	}
	return out
}

// applicable is the route this backend actually has, in dependency order.
func applicable(f Facts) []Step {
	steps := []Step{StepVMInit, StepVMStart, StepBootstrap}
	// A backend with no auth check of its own has no login to perform, and the
	// wizard must not list a step that would never be reachable.
	if f.Credential != CredentialNotApplicable {
		steps = append(steps, StepBackendLogin)
	}
	return append(steps, StepBrainInit, StepProjectAdd)
}

// Description is the operator-facing copy for one step.
type Description struct {
	// Title is the imperative the screen leads with.
	Title string
	// Detail says what the step does and, where the wait is long enough to look
	// like a hang, how long it can take.
	Detail string
	// Command is the equivalent command surface, so an operator learns the CLI
	// by using the hub rather than instead of it. Empty where there is none.
	Command string
}

// Describe returns the copy for a step. An unknown step gets a truthful
// placeholder rather than an empty screen.
func Describe(s Step) Description {
	switch s {
	case StepVMInit:
		return Description{
			Title:   "Create the box",
			Detail:  "Lima builds an isolated virtual machine for this backend. Nothing on the host is mounted into it.",
			Command: "torio vm init",
		}
	case StepVMStart:
		return Description{
			Title:   "Start the box",
			Detail:  "The box exists but is not running. Starting it is safe to repeat.",
			Command: "torio vm start",
		}
	case StepBoxUnusable:
		return Description{
			Title:  "This box cannot be used",
			Detail: "Lima reports a state Torio cannot work with. Inspect it with limactl before continuing; the wizard has no step that would repair it.",
		}
	case StepBootstrap:
		return Description{
			Title:   "Bootstrap the guest",
			Detail:  "Verifies the guest identity, its tools, and the backend at its pinned version, installing what is missing. This can take up to ten minutes on a fresh box.",
			Command: "torio vm bootstrap",
		}
	case StepBackendLogin:
		return Description{
			Title:   "Sign in to the backend",
			Detail:  "The backend holds no credential yet. Torio hands you the real login session and returns here when it ends.",
			Command: "torio backend login",
		}
	case StepBrainInit:
		return Description{
			Title:   "Create the Second Brain",
			Detail:  "Builds the Markdown vault on the guest, makes its first commit, and registers it as a project.",
			Command: "torio brain init",
		}
	case StepProjectAdd:
		return Description{
			Title:   "Add a project",
			Detail:  "Registers a repository and materializes its checkout on the guest, so the agent has something to work on.",
			Command: "torio project add",
		}
	case StepDone:
		return Description{
			Title:  "Setup is complete",
			Detail: "Every step Torio can verify has passed. The dashboard shows this box from here on.",
		}
	default:
		return Description{
			Title:  string(s),
			Detail: "This step has no description in this build.",
		}
	}
}
