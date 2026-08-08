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
	got := parseProcesses([]byte("  1234 600\nnonsense\n  -1 5\n  1400 notanumber\n  1500 12\n"))

	if len(got) != 2 {
		t.Fatalf("processes = %+v, want the two readable lines", got)
	}
	if got[1234].elapsed != 600*time.Second {
		t.Errorf("elapsed = %v, want 600s", got[1234].elapsed)
	}
	if _, ok := got[1500]; !ok {
		t.Error("a readable line after an unreadable one was dropped")
	}
}

func TestParseStatEntries(t *testing.T) {
	out := "/home/agent/a|agent|600|1754600000\ngarbage\n/home/agent/b|agent|644|1754600060\n"

	got := parseStatEntries([]byte(out))

	if len(got) != 2 {
		t.Fatalf("entries = %+v, want the two readable lines", got)
	}
	if got["/home/agent/b"].mtime.Unix() != 1754600060 {
		t.Errorf("mtime = %v, want the second the guest reported", got["/home/agent/b"].mtime)
	}
	if got["/home/agent/a"].owner != "agent" || got["/home/agent/a"].mode != "600" {
		t.Errorf("entry = %+v, want owner and mode carried through", got["/home/agent/a"])
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

func TestStatArgvIsOneCallOverEveryPath(t *testing.T) {
	got := statArgv([]string{"/a", "/b"})
	want := []string{"stat", "-c", statFormat, "/a", "/b"}
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %v, want %v", got, want)
		}
	}
}
