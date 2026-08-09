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

func markerDocJSON(kind string, pid int) string {
	if pid == 0 {
		return fmt.Sprintf(`{"schema_version":%q,"kind":%q}`, legacyMarkerSchemaVersion, kind)
	}
	return fmt.Sprintf(`{"schema_version":%q,"kind":%q,"pid":%d}`, legacyMarkerSchemaVersion, kind, pid)
}

func pollWithMarker(t *testing.T, env guestEnv) WaitingField {
	t.Helper()
	g := &fakeGuest{env: env}
	return pollOne(g, testBackend{spec: specWith(testProcess, true)}).Waiting
}

// The marker's whole purpose: an agent asked for a decision, and the operator
// is told which box is holding.
func TestWaitingIsReportedForAFreshOwnedMarkerWithALiveProcess(t *testing.T) {
	env := markerEnv(testUser, "600", testGuestNow-120, markerDocJSON(KindPermission, 1234))

	got := pollWithMarker(t, env)

	if got.State != Known || !got.Waiting {
		t.Fatalf("waiting = %+v, want a proven wait", got)
	}
	if got.Kind != KindPermission {
		t.Errorf("kind = %q, want %q", got.Kind, KindPermission)
	}
	if got.AgeSeconds != 120 {
		t.Errorf("age = %d, want the marker's own age on the guest clock", got.AgeSeconds)
	}
}

// Liveness outranks the marker. The process that asked is gone, so nobody is
// coming to answer a question nobody is asking any more.
func TestWaitingIsFalseWhenTheProcessThatAskedIsGone(t *testing.T) {
	env := markerEnv(testUser, "600", testGuestNow-120, markerDocJSON(KindPermission, 9999))

	got := pollWithMarker(t, env)

	if got.State != Known || got.Waiting {
		t.Fatalf("waiting = %+v, want a proven not-waiting", got)
	}
}

// A marker nobody cleared expires by itself rather than becoming a plea that
// never ends. It is unknown and not false: something was written and this poll
// cannot say what became of it.
func TestWaitingIsUnknownForAnExpiredMarker(t *testing.T) {
	env := markerEnv(testUser, "600", testGuestNow-int64(MarkerTTL.Seconds())-1, markerDocJSON(KindPermission, 1234))

	got := pollWithMarker(t, env)

	if got.State != Unknown {
		t.Fatalf("waiting state = %q, want %q for an expired marker", got.State, Unknown)
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
			env := markerEnv(tc.owner, tc.mode, testGuestNow-60, markerDocJSON(KindPermission, 1234))
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
		{"unknown field", `{"schema_version":"1","kind":"permission","detail":"rm -rf /"}`},
		{"trailing document", markerDocJSON(KindPermission, 1234) + markerDocJSON(KindPermission, 1234)},
		{"unsupported schema version", `{"schema_version":"99","kind":"permission"}`},
		{"legacy fields in aggregate schema", `{"schema_version":"2","kind":"permission","waits":[]}`},
		{"aggregate fields in legacy schema", `{"schema_version":"1","kind":"permission","waits":[]}`},
		{"unrecognized kind", `{"schema_version":"1","kind":"[31mred"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := markerEnv(testUser, "600", testGuestNow-60, tc.body)

			got := pollWithMarker(t, env)

			if got.State != Unknown {
				t.Fatalf("waiting state = %q, want %q", got.State, Unknown)
			}
			if got.Kind != "" {
				t.Errorf("kind = %q, want nothing from a marker that was refused", got.Kind)
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
		t.Error("the marker path was stat'd for a backend that declares no marker")
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

// A marker that names its session makes the answer actionable: on a box running
// two agents, "something here wants you" is only half an answer.
func TestWaitingCarriesTheSessionTheMarkerNamed(t *testing.T) {
	env := markerEnv(testUser, "600", testGuestNow-30, markerDocJSON(KindNotification, 1234))

	got := pollWithMarker(t, env)

	if got.State != Known || !got.Waiting {
		t.Fatalf("waiting = %+v, want a proven wait", got)
	}
	if got.PID != 1234 {
		t.Errorf("pid = %d, want the session the marker named", got.PID)
	}
}

// One fixed document may carry several sessions. Updating or clearing one hook
// entry must not erase another session that is still waiting on the same box.
func TestWaitingReportsEveryLiveSessionInTheMarker(t *testing.T) {
	env := markerEnv(testUser, "600", testGuestNow-30, fmt.Sprintf(
		`{"schema_version":"`+MarkerSchemaVersion+`","waits":[`+
			`{"session_id":"session-a","kind":"notification","pid":1234,"since_unix":%d},`+
			`{"session_id":"session-b","kind":"permission","pid":1235,"since_unix":%d}]}`,
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

// A marker with no session is still an answer about the box, ranked against
// every session on it. The field says so by carrying no pid rather than a zero
// that reads like one.
func TestWaitingWithoutASessionCarriesNoPID(t *testing.T) {
	env := markerEnv(testUser, "600", testGuestNow-30, markerDocJSON(KindNotification, 0))

	got := pollWithMarker(t, env)

	if got.State != Known || !got.Waiting {
		t.Fatalf("waiting = %+v, want a proven wait", got)
	}
	if got.PID != 0 {
		t.Errorf("pid = %d, want none", got.PID)
	}
}
