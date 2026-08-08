package status

import (
	"context"
	"errors"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
)

func TestPollReportsTheAgentProcessesTheGuestIsRunning(t *testing.T) {
	g := &fakeGuest{env: defaultEnv()}

	got := pollOne(g, testBackend{spec: specWith(testProcess, true)})

	if got.Session.State != Known {
		t.Fatalf("session state = %q, want %q", got.Session.State, Known)
	}
	if len(got.Session.Sessions) != 1 || got.Session.Sessions[0].PID != 1234 {
		t.Fatalf("sessions = %+v, want only the agent's own process", got.Session.Sessions)
	}
	if got.Session.Sessions[0].AgeSeconds != 600 {
		t.Errorf("age = %d, want the guest's own elapsed seconds", got.Session.Sessions[0].AgeSeconds)
	}
	if got.Progress.State != Known || got.Progress.AgeSeconds != 30 {
		t.Errorf("progress = %+v, want a known age measured on the guest clock", got.Progress)
	}
	if got.Waiting.State != Known || got.Waiting.Waiting {
		t.Errorf("waiting = %+v, want a proven not-waiting with no marker present", got.Waiting)
	}
}

// The match is on the whole process name. A prefix or substring match is how a
// status surface starts counting an editor, a pager or a grep the agent spawned
// as a second agent.
func TestPollCountsOnlyProcessesNamedExactly(t *testing.T) {
	env := defaultEnv()
	env.ps = " 1234 600 " + testProcess + "\n 1300 20 " + testProcess + "-helper\n 1400 12 bash\n"
	g := &fakeGuest{env: env}

	got := pollOne(g, testBackend{spec: specWith(testProcess, false)})

	if len(got.Session.Sessions) != 1 || got.Session.Sessions[0].PID != 1234 {
		t.Fatalf("sessions = %+v, want only the exact name matched", got.Session.Sessions)
	}
}

// Nothing running is a proven answer, not an absent one: the process table was
// read and the agent was not in it.
func TestPollReportsAnEmptyProcessTableAsProvenQuiet(t *testing.T) {
	env := defaultEnv()
	env.ps = " 1400 12 bash\n"
	g := &fakeGuest{env: env}

	got := pollOne(g, testBackend{spec: specWith(testProcess, false)})

	if got.Session.State != Known {
		t.Fatalf("session state = %q, want %q", got.Session.State, Known)
	}
	if len(got.Session.Sessions) != 0 {
		t.Fatalf("sessions = %+v, want none", got.Session.Sessions)
	}
}

// A backend that runs no process a session corresponds to has declared that,
// and the field says so rather than reporting an agent that is not running.
func TestPollReportsNotApplicableForABackendWithNoSessionProcess(t *testing.T) {
	g := &fakeGuest{env: defaultEnv()}

	got := pollOne(g, testBackend{spec: &backend.StatusSpec{ProgressPaths: []string{testProgressPath}}})

	if got.Session.State != NotApplicable {
		t.Fatalf("session state = %q, want %q", got.Session.State, NotApplicable)
	}
	if g.saw("ps -o") {
		t.Error("the process table was read for a backend that declares no session process")
	}
	if got.Progress.State != Known {
		t.Errorf("progress state = %q, want the declared half still answered", got.Progress.State)
	}
}

// One unprovable fact must not take the others down with it: a box whose
// process table is unreadable can still report when it last progressed.
func TestPollDegradesOneFactAtATime(t *testing.T) {
	env := defaultEnv()
	env.truncate = "processes"
	g := &fakeGuest{env: env}

	got := pollOne(g, testBackend{spec: specWith(testProcess, true)})

	if got.Session.State != Unknown {
		t.Fatalf("session state = %q, want %q", got.Session.State, Unknown)
	}
	if got.Session.Sessions == nil {
		t.Error("sessions = null, want an empty array so a reader can count it unconditionally")
	}
	if got.Progress.State != Known {
		t.Errorf("progress state = %q, want it unaffected by the session read", got.Progress.State)
	}
	if got.Waiting.State != Unknown {
		t.Errorf("waiting state = %q, want unknown: it ranks below a liveness nobody could read", got.Waiting.State)
	}
}

// A backend that declares no probe is answered from the declaration, and the
// guest is not touched at all. Asking anyway would be inventing work to justify
// an answer already given.
func TestPollAsksNothingOfABackendThatDeclaresNoProbe(t *testing.T) {
	g := &fakeGuest{env: defaultEnv()}

	got := pollOne(g, nullBackend{})

	if g.callCount() != 0 {
		t.Fatalf("guest calls = %d, want none for an undeclared probe", g.callCount())
	}
	for _, tc := range []struct {
		field string
		state FieldState
	}{
		{"session", got.Session.State},
		{"waiting", got.Waiting.State},
		{"progress", got.Progress.State},
	} {
		if tc.state != NotApplicable {
			t.Errorf("%s state = %q, want %q", tc.field, tc.state, NotApplicable)
		}
	}
}

// A stopped box runs no processes, which proves both that nothing is running
// and that nobody on it is waiting — without a guest command. When it last
// progressed is a different question, and stays unknown.
func TestPollAnswersAStoppedBoxWithoutEnteringIt(t *testing.T) {
	g := &fakeGuest{env: defaultEnv()}

	got := pollBox(g, testBackend{spec: specWith(testProcess, true)},
		Box{Name: "torio-test", State: "stopped", Running: false})

	if g.callCount() != 0 {
		t.Fatalf("guest calls = %d, want none for a stopped box", g.callCount())
	}
	if got.Session.State != Known || len(got.Session.Sessions) != 0 {
		t.Errorf("session = %+v, want a proven empty set", got.Session)
	}
	if got.Waiting.State != Known || got.Waiting.Waiting {
		t.Errorf("waiting = %+v, want a proven not-waiting", got.Waiting)
	}
	if got.Progress.State != Unknown {
		t.Errorf("progress state = %q, want %q: the evidence is inside a VM that is not running", got.Progress.State, Unknown)
	}
}

// A box whose own document could not be read is one unknown row, never a reason
// to stop answering about the others.
func TestPollReportsAnUnresolvedBackendAsUnknownAndKeepsGoing(t *testing.T) {
	g := &fakeGuest{env: defaultEnv()}
	b := testBackend{spec: specWith(testProcess, false)}
	p := &Poller{
		Instances: func(context.Context) ([]Box, error) {
			return []Box{
				{Name: "torio", State: "running", Running: true},
				{Name: "torio-test", State: "running", Running: true},
			}, nil
		},
		Transport: func(string) backend.Transport { return g },
		Resolve: func(instance string) Resolution {
			if instance == "torio" {
				return Resolution{}
			}
			return Resolution{Backend: b, Name: "test"}
		},
	}

	rep, err := p.Poll(context.Background())
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(rep.Instances) != 2 {
		t.Fatalf("instances = %d, want both boxes reported", len(rep.Instances))
	}
	if rep.Instances[0].Backend.State != Unknown {
		t.Errorf("unresolved backend state = %q, want %q", rep.Instances[0].Backend.State, Unknown)
	}
	if rep.Instances[0].Session.State != Unknown {
		t.Errorf("unresolved box session state = %q, want %q", rep.Instances[0].Session.State, Unknown)
	}
	if rep.Instances[1].Backend.Name != "test" {
		t.Errorf("second box backend = %q, want it answered normally", rep.Instances[1].Backend.Name)
	}
}

// A box that cannot be reached at all reports every agent fact as unknown, and
// the box state it was enumerated with stays.
func TestPollReportsAnUnreachableBoxAsUnknown(t *testing.T) {
	g := &fakeGuest{env: defaultEnv(), failWith: errors.New("transport failed")}

	got := pollOne(g, testBackend{spec: specWith(testProcess, true)})

	if got.Box != "running" {
		t.Errorf("box = %q, want the enumerated state kept", got.Box)
	}
	for _, tc := range []struct {
		field string
		state FieldState
	}{
		{"session", got.Session.State},
		{"waiting", got.Waiting.State},
		{"progress", got.Progress.State},
	} {
		if tc.state != Unknown {
			t.Errorf("%s state = %q, want %q", tc.field, tc.state, Unknown)
		}
	}
}

// The enumeration is the one failure a poll does not survive: without it there
// is nothing to report on, and an empty report would read as "no boxes".
func TestPollFailsWhenTheEnumerationFails(t *testing.T) {
	want := errors.New("limactl unavailable")
	p := &Poller{Instances: func(context.Context) ([]Box, error) { return nil, want }}

	if _, err := p.Poll(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Poll error = %v, want %v", err, want)
	}
}

// Every probe runs as the backend's identity, never as the Lima login user,
// which holds passwordless root.
func TestPollRunsEveryProbeAsTheBackendIdentity(t *testing.T) {
	g := &fakeGuest{env: defaultEnv()}

	pollOne(g, testBackend{spec: specWith(testProcess, true)})

	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.calls) == 0 {
		t.Fatal("no guest calls recorded")
	}
	prefix := "sudo -n -u " + testUser + " --"
	for _, c := range g.calls {
		if len(c) < len(prefix) || c[:len(prefix)] != prefix {
			t.Errorf("guest call %q does not run as the backend identity", c)
		}
	}
}
