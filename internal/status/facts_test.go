package status

import (
	"testing"
	"time"
)

func TestParseGuestNow(t *testing.T) {
	got, err := parseGuestNow([]byte("1754600000\n"))
	if err != nil {
		t.Fatalf("parseGuestNow: %v", err)
	}
	if got.Unix() != 1754600000 {
		t.Errorf("guest now = %v, want the unix second the guest printed", got)
	}
	if _, err := parseGuestNow([]byte("Tue Aug  8 12:00:00 UTC 2026")); err == nil {
		t.Error("parseGuestNow accepted a formatted date; every age is measured against this")
	}
}

// A line the process list reader cannot follow is skipped rather than failing
// the whole reading: one unreadable line cannot make a pid that is there
// absent, and failing closed on it would report every session on the box as
// unknown because of a process that has nothing to do with any of them.
func TestParseProcessesSkipsWhatItCannotRead(t *testing.T) {
	got := parseProcesses([]byte("  1234 600 claude\nnonsense\n  -1 5 claude\n  1400 notanumber bash\n  1500 12 claude\n"))

	if len(got) != 2 {
		t.Fatalf("processes = %+v, want the two readable lines", got)
	}
	if got[0].pid != 1234 || got[0].elapsed != 600*time.Second || got[0].name != "claude" {
		t.Errorf("process = %+v, want pid 1234 running 600s as claude", got[0])
	}
	if got[1].pid != 1500 {
		t.Error("a readable line after an unreadable one was dropped")
	}
}

// The kernel allows a space in a process name, and comm is the last column, so
// everything after the two numeric columns belongs to the name.
func TestParseProcessesKeepsANameWithASpace(t *testing.T) {
	got := parseProcesses([]byte("  42 7 my agent\n"))

	if len(got) != 1 || got[0].name != "my agent" {
		t.Fatalf("processes = %+v, want the whole name kept", got)
	}
}

// Selecting by name is what turns a process table into sessions, and it matches
// the whole name so a helper the agent spawned is not counted as an agent.
func TestSessionsNamed(t *testing.T) {
	now := time.Unix(1754600000, 0).UTC()
	live := []process{
		{pid: 10, elapsed: 60 * time.Second, name: "claude"},
		{pid: 11, elapsed: 5 * time.Second, name: "claude-helper"},
		{pid: 12, elapsed: 5 * time.Second, name: "bash"},
	}

	got := sessionsNamed("claude", live, now)

	if got.State != Known || len(got.Sessions) != 1 || got.Sessions[0].PID != 10 {
		t.Fatalf("sessions = %+v, want only the exact match", got)
	}
	if got.Sessions[0].StartedAt != now.Add(-60*time.Second).Format(time.RFC3339) {
		t.Errorf("started at = %q, want it derived from the guest clock", got.Sessions[0].StartedAt)
	}
	if empty := sessionsNamed("nothing", live, now); empty.State != Known || len(empty.Sessions) != 0 {
		t.Errorf("sessions = %+v, want a proven empty set", empty)
	}
}

func TestParsePathFact(t *testing.T) {
	got, err := parsePathFact([]byte("/home/agent/a|agent|600|1754600000.123\n"), "/home/agent/a")
	if err != nil {
		t.Fatalf("parsePathFact: %v", err)
	}
	if got == nil || got.mtime.Unix() != 1754600000 || got.owner != "agent" || got.mode != "600" {
		t.Fatalf("entry = %+v, want the exact path fact", got)
	}
	if absent, err := parsePathFact(nil, "/home/agent/a"); err != nil || absent != nil {
		t.Fatalf("absent fact = %+v, %v; want nil, nil", absent, err)
	}
	for _, out := range []string{
		"garbage\n",
		"/home/agent/other|agent|600|1754600000\n",
		"/home/agent/a|agent|600|1754600000\n/home/agent/a|agent|600|1754600001\n",
	} {
		if _, err := parsePathFact([]byte(out), "/home/agent/a"); err == nil {
			t.Errorf("parsePathFact accepted %q", out)
		}
	}
}

func TestWritableBeyondOwner(t *testing.T) {
	for _, tc := range []struct {
		mode string
		want bool
	}{
		{"600", false},
		{"644", false},
		{"660", true},
		{"666", true},
		{"602", true},
		{"not-a-mode", true},
	} {
		if got := writableBeyondOwner(tc.mode); got != tc.want {
			t.Errorf("writableBeyondOwner(%q) = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

// Progress is the newest evidence among several paths, and a path that has
// never been written is not a zero: a backend that has never run has written
// none of them, and the honest answer is that nothing is known.
func TestNewestProgress(t *testing.T) {
	now := time.Unix(1754600000, 0).UTC()
	entries := map[string]statEntry{
		"/a": {path: "/a", mtime: now.Add(-5 * time.Minute)},
		"/b": {path: "/b", mtime: now.Add(-30 * time.Second)},
	}

	got := newestProgress([]string{"/a", "/b", "/never-written"}, entries, now)
	if got.State != Known || got.AgeSeconds != 30 {
		t.Fatalf("progress = %+v, want the newest of the paths present", got)
	}

	if absent := newestProgress([]string{"/never-written"}, entries, now); absent.State != Unknown {
		t.Errorf("progress = %+v, want unknown when no evidence exists yet", absent)
	}

	future := map[string]statEntry{"/a": {path: "/a", mtime: now.Add(time.Hour)}}
	if got := newestProgress([]string{"/a"}, future, now); got.State != Unknown {
		t.Errorf("progress = %+v, want unknown for an mtime in the future", got)
	}
}

func TestPathFactArgvReadsOnlyTheFixedName(t *testing.T) {
	got := pathFactArgv("/home/agent/.torio-waiting.json")
	want := []string{"find", "/home/agent", "-maxdepth", "1", "-name", ".torio-waiting.json", "-type", "f", "-printf", pathFactFormat}
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %v, want %v", got, want)
		}
	}
}
