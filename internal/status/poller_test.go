package status

import (
	"context"
	"errors"
	"testing"

	"github.com/wzslr321/torio/internal/backend"
)

func TestPollReportsALiveSessionTheGuestConfirms(t *testing.T) {
	g := &fakeGuest{env: defaultEnv()}
	b := testBackend{spec: specWith(claiming(backend.SessionFact{PID: 1234, StartedAt: startedSecondsAgo(600)}), true)}

	got := pollOne(g, b)

	if got.Session.State != Known {
		t.Fatalf("session state = %q, want %q", got.Session.State, Known)
	}
	if len(got.Session.Sessions) != 1 || got.Session.Sessions[0].PID != 1234 {
		t.Fatalf("sessions = %+v, want the confirmed pid", got.Session.Sessions)
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

// A record that claims a pid nothing is running is the exact lie this design
// exists to refuse: no backend reports its own death, so a killed agent leaves
// its record behind saying it is working.
func TestPollDropsASessionNoLiveProcessConfirms(t *testing.T) {
	env := defaultEnv()
	env.ps = " 1400 12\n"
	g := &fakeGuest{env: env}
	b := testBackend{spec: specWith(claiming(backend.SessionFact{PID: 1234}), false)}

	got := pollOne(g, b)

	if got.Session.State != Known {
		t.Fatalf("session state = %q, want %q", got.Session.State, Known)
	}
	if len(got.Session.Sessions) != 0 {
		t.Fatalf("sessions = %+v, want the unconfirmed claim dropped", got.Session.Sessions)
	}
}

// A pid the guest handed to a different process must not stand in for the agent
// that died holding it.
func TestPollDropsASessionWhoseStartTimeDisagrees(t *testing.T) {
	g := &fakeGuest{env: defaultEnv()}
	b := testBackend{spec: specWith(claiming(backend.SessionFact{PID: 1234, StartedAt: startedSecondsAgo(90_000)}), false)}

	got := pollOne(g, b)

	if len(got.Session.Sessions) != 0 {
		t.Fatalf("sessions = %+v, want the recycled pid rejected", got.Session.Sessions)
	}
}

// A record that cannot be read is not a quiet box. Reading absence as quiet is
// how a status surface reports a working agent as gone.
func TestPollReportsUnknownWhenTheRecordCannotBeRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  func(guestEnv) guestEnv
		spec *backend.StatusSpec
	}{
		{
			name: "command failed",
			env:  func(e guestEnv) guestEnv { e.recordRC = 1; return e },
			spec: specWith(claiming(), false),
		},
		{
			name: "output truncated",
			env:  func(e guestEnv) guestEnv { e.truncate = "record"; return e },
			spec: specWith(claiming(), false),
		},
		{
			name: "output unparseable",
			env:  func(e guestEnv) guestEnv { return e },
			spec: specWith(unparseable, false),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &fakeGuest{env: tc.env(defaultEnv())}
			got := pollOne(g, testBackend{spec: tc.spec})
			if got.Session.State != Unknown {
				t.Fatalf("session state = %q, want %q", got.Session.State, Unknown)
			}
			if got.Session.Sessions == nil {
				t.Error("sessions = null, want an empty array so a reader can count it unconditionally")
			}
		})
	}
}

// One unprovable fact must not take the others down with it: a box whose
// process list is unreadable can still report when it last progressed.
func TestPollDegradesOneFactAtATime(t *testing.T) {
	env := defaultEnv()
	env.truncate = "processes"
	g := &fakeGuest{env: env}
	b := testBackend{spec: specWith(claiming(backend.SessionFact{PID: 1234}), true)}

	got := pollOne(g, b)

	if got.Session.State != Unknown {
		t.Fatalf("session state = %q, want %q", got.Session.State, Unknown)
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
	b := testBackend{spec: specWith(claiming(backend.SessionFact{PID: 1234}), true)}

	got := pollBox(g, b, Box{Name: "torio-test", State: "stopped", Running: false})

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
	b := testBackend{spec: specWith(claiming(), false)}
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
	b := testBackend{spec: specWith(claiming(), true)}

	got := pollOne(g, b)

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

// The probe runs as the backend's identity, never as the Lima login user, which
// holds passwordless root.
func TestPollRunsEveryProbeAsTheBackendIdentity(t *testing.T) {
	g := &fakeGuest{env: defaultEnv()}
	b := testBackend{spec: specWith(claiming(), true)}

	pollOne(g, b)

	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.calls) == 0 {
		t.Fatal("no guest calls recorded")
	}
	for _, c := range g.calls {
		if !hasPrefixFields(c, "sudo -n -u "+testUser+" --") {
			t.Errorf("guest call %q does not run as the backend identity", c)
		}
	}
}

func hasPrefixFields(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
