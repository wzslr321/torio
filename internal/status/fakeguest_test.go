package status

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

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
	record    string
	recordRC  int
	statLines string
	marker    string
	markerRC  int
	// truncate names the fact whose output arrives truncated ("clock",
	// "processes", "record", "stat", "marker").
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
	case strings.Contains(j, testRecordPath):
		return f.answer("record", f.env.recordRC, f.env.record), nil
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
	testUser       = "agent"
	testHome       = "/home/agent"
	testRecordPath = "/home/agent/.state/record.json"
	testGuestNow   = 1_754_600_000 // a fixed unix second; nothing here reads a real clock
)

// testBackend declares a probe whose parser is supplied per test, so a test can
// say "this record is unparseable" without inventing a wire format.
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

// specWith returns a probe declaring one record path, one progress path and the
// marker convention.
func specWith(parse func([]byte) ([]backend.SessionFact, error), marker bool) *backend.StatusSpec {
	return &backend.StatusSpec{
		SessionArgv:   []string{"cat", "--", testRecordPath},
		ParseSessions: parse,
		ProgressPaths: []string{testRecordPath},
		WaitingMarker: marker,
	}
}

// claiming is a parser that reports a fixed set of sessions.
func claiming(facts ...backend.SessionFact) func([]byte) ([]backend.SessionFact, error) {
	return func([]byte) ([]backend.SessionFact, error) { return facts, nil }
}

// unparseable is a parser that refuses, the way a strict decoder refuses a
// document it cannot vouch for.
func unparseable(_ []byte) ([]backend.SessionFact, error) {
	return nil, fmt.Errorf("unknown field in record")
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
		ps:        " 1234 600\n 1400 12\n",
		record:    "{}",
		statLines: statLine(testRecordPath, testUser, "600", testGuestNow-30) + "\n",
	}
}

// startedSecondsAgo is the wall-clock start a backend would have recorded for a
// process the guest reports as having run for that long.
func startedSecondsAgo(n int64) time.Time {
	return time.Unix(testGuestNow-n, 0).UTC()
}
