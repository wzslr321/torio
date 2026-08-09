package status

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// markerEnv is defaultEnv plus a marker file with the given stat facts and body.
func markerEnv(owner, mode string, mtime int64, body string) guestEnv {
	e := defaultEnv()
	e.statLines = statLine(testProgressPath, testUser, "600", testGuestNow-30) + "\n" +
		statLine(testHome+"/"+MarkerFileName, owner, mode, mtime) + "\n"
	e.marker = body
	return e
}

func markerDocJSON(pid int, since int64) string {
	return fmt.Sprintf(
		`{"schema_version":%q,"waits":[{"session_id":"session-a","pid":%d,"since_unix":%d}]}`,
		MarkerSchemaVersion, pid, since)
}

func pollWithMarker(t *testing.T, env guestEnv) WaitingField {
	t.Helper()
	g := &fakeGuest{env: env}
	return pollOne(g, testBackend{spec: specWith(testProcess, true)}).Waiting
}

// The marker's whole purpose: an agent asked for a decision, and the operator
// is told which box is holding.
func TestWaitingIsReportedForAFreshOwnedMarkerWithALiveProcess(t *testing.T) {
	env := markerEnv(testUser, "600", testGuestNow-120, markerDocJSON(1234, testGuestNow-120))

	got := pollWithMarker(t, env)

	if got.State != Known || !got.Waiting {
		t.Fatalf("waiting = %+v, want a proven wait", got)
	}
	if len(got.Waits) != 1 || got.Waits[0].AgeSeconds != 120 {
		t.Errorf("waits = %+v, want the wait age measured on the guest clock", got.Waits)
	}
}

// Liveness outranks the marker. The process that asked is gone, so nobody is
// coming to answer a question nobody is asking any more.
func TestWaitingIsFalseWhenTheProcessThatAskedIsGone(t *testing.T) {
	env := markerEnv(testUser, "600", testGuestNow-120, markerDocJSON(9999, testGuestNow-120))

	got := pollWithMarker(t, env)

	if got.State != Known || got.Waiting {
		t.Fatalf("waiting = %+v, want a proven not-waiting", got)
	}
}

// A marker nobody cleared expires by itself rather than becoming a plea that
// never ends. It is unknown and not false: something was written and this poll
// cannot say what became of it.
func TestWaitingIsUnknownForAnExpiredMarker(t *testing.T) {
	env := markerEnv(testUser, "600", testGuestNow-60,
		markerDocJSON(1234, testGuestNow-int64(MarkerTTL.Seconds())-1))
	env.ps = " 1234 7200 " + testProcess + "\n"

	got := pollWithMarker(t, env)

	if got.State != Unknown {
		t.Fatalf("waiting state = %q, want %q for an expired marker", got.State, Unknown)
	}
}

func TestAnOldEmptyMarkerStillProvesHookReadiness(t *testing.T) {
	env := markerEnv(testUser, "600", testGuestNow-int64(MarkerTTL.Seconds())-1,
		`{"schema_version":"2","waits":[]}`)

	got := pollWithMarker(t, env)

	if got.State != Known || got.Waiting {
		t.Fatalf("waiting = %+v, want a proven empty marker regardless of file age", got)
	}
}

// The ownership and mode gate runs before the content is fetched, so a marker
// some other identity could have written is never parsed.
func TestWaitingIsUnknownForAMarkerThatFailsItsGate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner string
		mode  string
	}{
		{"another identity owns it", "root", "600"},
		{"the group can write it", testUser, "660"},
		{"everyone can write it", testUser, "666"},
		{"the mode is unreadable", testUser, "rw-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := markerEnv(tc.owner, tc.mode, testGuestNow-60, markerDocJSON(1234, testGuestNow-60))
			g := &fakeGuest{env: env}

			got := pollOne(g, testBackend{spec: specWith(testProcess, true)})

			if got.Waiting.State != Unknown {
				t.Fatalf("waiting state = %q, want %q", got.Waiting.State, Unknown)
			}
			if g.saw("cat -- " + testHome + "/" + MarkerFileName) {
				t.Error("the marker was read despite failing its ownership and mode gate")
			}
		})
	}
}

// A marker this binary cannot vouch for is unknown, never a guess. It is
// written on the untrusted side of the VM boundary.
func TestWaitingIsUnknownForAMarkerThatCannotBeVouchedFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"not JSON", "{"},
		{"unknown field", `{"schema_version":"2","waits":[],"detail":"rm -rf /"}`},
		{"trailing document", markerDocJSON(1234, testGuestNow-60) + markerDocJSON(1234, testGuestNow-60)},
		{"unsupported schema version", `{"schema_version":"99","waits":[]}`},
		{"unshipped kind", `{"schema_version":"2","waits":[{"session_id":"a","kind":"permission","pid":1234,"since_unix":1}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := markerEnv(testUser, "600", testGuestNow-60, tc.body)

			got := pollWithMarker(t, env)

			if got.State != Unknown {
				t.Fatalf("waiting state = %q, want %q", got.State, Unknown)
			}
		})
	}
}

// A backend with no way to say is unknown, not not-waiting. An operator told an
// agent is not waiting stops looking at it.
func TestWaitingIsUnknownWhenTheBackendDeclaresNoMarker(t *testing.T) {
	g := &fakeGuest{env: defaultEnv()}

	got := pollOne(g, testBackend{spec: specWith(testProcess, false)})

	if got.Waiting.State != Unknown {
		t.Fatalf("waiting state = %q, want %q", got.Waiting.State, Unknown)
	}
	if g.saw(MarkerFileName) {
		t.Error("the marker path was read for a backend that declares no marker")
	}
}

func TestWaitingIsUnknownWhenADeclaredMarkerIsAbsent(t *testing.T) {
	env := defaultEnv()
	env.statLines = statLine(testProgressPath, testUser, "600", testGuestNow-30) + "\n"
	env.marker = ""
	g := &fakeGuest{env: env}

	got := pollOne(g, testBackend{spec: specWith(testProcess, true)}).Waiting

	if got.State != Unknown {
		t.Fatalf("waiting state = %q, want %q until bootstrap has initialized the marker", got.State, Unknown)
	}
	if g.saw("cat -- " + testHome + "/" + MarkerFileName) {
		t.Error("an absent marker was fetched")
	}
}

// Like session.sessions, waiting.waits is always an array. A renderer can count
// it after reading state without adding a null special case for quiet boxes.
func TestWaitingWaitsIsNeverNull(t *testing.T) {
	g := &fakeGuest{env: defaultEnv()}
	got := pollOne(g, testBackend{spec: specWith(testProcess, true)}).Waiting

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) == "" || !strings.Contains(string(body), `"waits":[]`) {
		t.Fatalf("waiting JSON = %s, want an empty waits array", body)
	}
}

func TestWaitingFieldHasNoDuplicateSummary(t *testing.T) {
	field := WaitingField{
		State:   Known,
		Waiting: true,
		Waits: []Wait{{
			SessionID:  "session-a",
			PID:        1234,
			AgeSeconds: 30,
		}},
	}
	body, err := json.Marshal(field)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"kind", "pid", "age_seconds"} {
		if _, exists := top[key]; exists {
			t.Errorf("waiting JSON duplicates waits[] in top-level %q: %s", key, body)
		}
	}
}

// A marker that names its session makes the answer actionable: on a box running
// two agents, "something here wants you" is only half an answer.
func TestWaitingCarriesTheSessionTheMarkerNamed(t *testing.T) {
	env := markerEnv(testUser, "600", testGuestNow-30, markerDocJSON(1234, testGuestNow-30))

	got := pollWithMarker(t, env)

	if got.State != Known || !got.Waiting {
		t.Fatalf("waiting = %+v, want a proven wait", got)
	}
	if len(got.Waits) != 1 || got.Waits[0].PID != 1234 {
		t.Errorf("waits = %+v, want the session the marker named", got.Waits)
	}
}

// One fixed document may carry several sessions. Updating or clearing one hook
// entry must not erase another session that is still waiting on the same box.
func TestWaitingReportsEveryLiveSessionInTheMarker(t *testing.T) {
	env := markerEnv(testUser, "600", testGuestNow-30, fmt.Sprintf(
		`{"schema_version":"`+MarkerSchemaVersion+`","waits":[`+
			`{"session_id":"session-a","pid":1234,"since_unix":%d},`+
			`{"session_id":"session-b","pid":1235,"since_unix":%d}]}`,
		testGuestNow-30, testGuestNow-10))
	env.ps = " 1234 600 " + testProcess + "\n 1235 300 " + testProcess + "\n"

	got := pollWithMarker(t, env)

	if got.State != Known || !got.Waiting {
		t.Fatalf("waiting = %+v, want a proven wait", got)
	}
	if len(got.Waits) != 2 {
		t.Fatalf("waits = %+v, want both live sessions", got.Waits)
	}
	if got.Waits[0].SessionID != "session-a" || got.Waits[1].SessionID != "session-b" {
		t.Errorf("waits = %+v, want stable marker order", got.Waits)
	}
}

func TestWaitingRejectsAWaitOlderThanTheReusedPID(t *testing.T) {
	env := markerEnv(testUser, "600", testGuestNow-5, fmt.Sprintf(
		`{"schema_version":"%s","waits":[{"session_id":"old-session","pid":1234,"since_unix":%d}]}`,
		MarkerSchemaVersion, testGuestNow-120))
	// PID 1234 belongs to a process started only 30 seconds ago. The marker is
	// older, so it cannot describe this process even though the number matches.
	env.ps = " 1234 30 " + testProcess + "\n"

	got := pollWithMarker(t, env)

	if got.State != Known || got.Waiting {
		t.Fatalf("waiting = %+v, want a reused PID to discard the stale wait", got)
	}
}
