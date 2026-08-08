package status

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/execx"
)

// fakeGuest answers guest commands by matching the joined argv, the same shape
// internal/serve's fake uses. Matching on the argv rather than on a call index
// is what lets a test say "the roster read fails" without also pinning how many
// other reads happen before it.
type fakeGuest struct {
	mu       sync.Mutex
	calls    []string
	env      guestEnv
	failWith error
}

// guestEnv is the box a fake guest presents. Every field is the raw output of
// one probe, so a test states what the guest said rather than what the poll
// should conclude.
type guestEnv struct {
	now       string
	ps        string
	statLines string
	marker    string
	markerRC  int
	// truncate names the fact whose output arrives truncated ("clock",
	// "processes", "stat", "marker").
	truncate string
}

func (f *fakeGuest) SSH(_ context.Context, argv []string) (execx.Result, error) {
	j := strings.Join(argv, " ")
	f.mu.Lock()
	f.calls = append(f.calls, j)
	f.mu.Unlock()

	if f.failWith != nil {
		return execx.Result{ExitCode: -1}, f.failWith
	}

	switch {
	case strings.Contains(j, "date +%s"):
		return f.answer("clock", 0, f.env.now), nil
	case strings.Contains(j, "ps -o pid=,etimes="):
		return f.answer("processes", 0, f.env.ps), nil
	case strings.Contains(j, "stat -c"):
		return f.answer("stat", 0, f.env.statLines), nil
	case strings.Contains(j, "cat -- "+testHome+"/"+MarkerFileName):
		return f.answer("marker", f.env.markerRC, f.env.marker), nil
	}
	return execx.Result{}, fmt.Errorf("fakeGuest: unrouted command: %s", j)
}

func (f *fakeGuest) answer(fact string, code int, out string) execx.Result {
	res := execx.Result{ExitCode: code, Stdout: []byte(out)}
	if f.env.truncate == fact {
		res.StdoutTruncated = true
	}
	return res
}

func (f *fakeGuest) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeGuest) saw(sub string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

// The guest layout the tests are written against.
const (
	testUser         = "agent"
	testHome         = "/home/agent"
	testProcess      = "agent-cli"
	testProgressPath = "/home/agent/.state/transcript.jsonl"
	testGuestNow     = 1_754_600_000 // a fixed unix second; nothing here reads a real clock
)

// testBackend declares a probe a test shapes per case.
type testBackend struct {
	backend.Backend
	spec *backend.StatusSpec
}

func (b testBackend) Identity() backend.Identity {
	return backend.Identity{Name: "test", GuestUser: testUser, Home: testHome}
}
func (b testBackend) Status() *backend.StatusSpec { return b.spec }

// nullBackend declares no probe at all.
type nullBackend struct{ backend.Backend }

func (nullBackend) Identity() backend.Identity {
	return backend.Identity{Name: "null", GuestUser: testUser, Home: testHome}
}
func (nullBackend) Status() *backend.StatusSpec { return nil }

// specWith returns a probe declaring the agent process name, one progress path
// and whether the marker convention is written.
func specWith(process string, marker bool) *backend.StatusSpec {
	return &backend.StatusSpec{
		SessionProcess: process,
		ProgressPaths:  []string{testProgressPath},
		WaitingMarker:  marker,
	}
}

// statLine renders one `stat -c` line the way the guest would.
func statLine(path, owner, mode string, mtime int64) string {
	return fmt.Sprintf("%s|%s|%s|%d", path, owner, mode, mtime)
}

// pollOne runs a poll over a single running box backed by g.
func pollOne(g *fakeGuest, b backend.Backend) Instance {
	return pollBox(g, b, Box{Name: "torio-test", State: "running", Running: true})
}

func pollBox(g *fakeGuest, b backend.Backend, box Box) Instance {
	p := &Poller{
		Instances: func(context.Context) ([]Box, error) { return []Box{box}, nil },
		Transport: func(string) backend.Transport { return g },
		Resolve:   func(string) Resolution { return Resolution{Backend: b, Name: b.Identity().Name} },
	}
	rep, err := p.Poll(context.Background())
	if err != nil {
		panic(err)
	}
	return rep.Instances[0]
}

// defaultEnv is a box with one live agent process, a record that claims it, and
// no marker.
func defaultEnv() guestEnv {
	return guestEnv{
		now:       fmt.Sprintf("%d\n", testGuestNow),
		ps:        " 1234 600 " + testProcess + "\n 1400 12 bash\n",
		statLines: statLine(testProgressPath, testUser, "600", testGuestNow-30) + "\n",
	}
}
