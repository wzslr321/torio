package mcpbroker

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeWindowFile(t *testing.T, dir, service, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, service), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadWriteWindowOpenUntilExpiry(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	writeWindowFile(t, dir, "atlassian", now.Add(15*time.Minute).Format(time.RFC3339)+"\n")

	if w := ReadWriteWindow(dir, "atlassian", now); !w.Open {
		t.Error("window inside its validity reported closed")
	}
	if w := ReadWriteWindow(dir, "atlassian", now.Add(14*time.Minute)); !w.Open {
		t.Error("window one minute before expiry reported closed")
	}
	if w := ReadWriteWindow(dir, "atlassian", now.Add(15*time.Minute)); w.Open {
		t.Error("window open exactly at its expiry; the boundary must close it")
	}
	if w := ReadWriteWindow(dir, "atlassian", now.Add(time.Hour)); w.Open {
		t.Error("expired window reported open")
	}
}

// TestReadWriteWindowClosedByDefault is the whole point of the mechanism: a
// service with no window file has no window. Absence is the normal state, not
// an error state.
func TestReadWriteWindowClosedByDefault(t *testing.T) {
	if w := ReadWriteWindow(t.TempDir(), "atlassian", time.Now()); w.Open {
		t.Error("a service with no window file reported open")
	}
	if w := ReadWriteWindow(filepath.Join(t.TempDir(), "nonexistent"), "atlassian", time.Now()); w.Open {
		t.Error("a missing window directory reported open")
	}
}

// TestReadWriteWindowAnythingUnreadableIsClosed pins the fail-closed shape. A
// caller that had to distinguish "closed" from "could not tell" would sooner or
// later treat the second as the first's opposite, so the function offers only
// one answer for both.
func TestReadWriteWindowAnythingUnreadableIsClosed(t *testing.T) {
	now := time.Now()
	cases := map[string]string{
		"empty":        "",
		"whitespace":   "   \n",
		"not a time":   "soon\n",
		"wrong format": "2026-07-29 12:00:00\n",
		"no timezone":  "2026-07-29T12:00:00\n",
		"two lines":    time.Now().Add(time.Hour).Format(time.RFC3339) + "\nextra\n",
		"leading junk": "x" + time.Now().Add(time.Hour).Format(time.RFC3339),
		"huge":         string(make([]byte, 4096)),
	}
	for label, content := range cases {
		t.Run(label, func(t *testing.T) {
			dir := t.TempDir()
			writeWindowFile(t, dir, "atlassian", content)
			if w := ReadWriteWindow(dir, "atlassian", now); w.Open {
				t.Errorf("%s window content reported open", label)
			}
		})
	}
}

// TestReadWriteWindowRejectsUnvalidatedService keeps the service name from
// reaching the filesystem unchecked: the name arrives from a client request, so
// a traversal here would let a caller point the lookup at any readable file.
func TestReadWriteWindowRejectsUnvalidatedService(t *testing.T) {
	dir := t.TempDir()
	writeWindowFile(t, dir, "atlassian", time.Now().Add(time.Hour).Format(time.RFC3339))

	for _, bad := range []string{"../atlassian", "atlassian/../atlassian", "/etc/passwd", "Atlassian", ""} {
		if w := ReadWriteWindow(dir, bad, time.Now()); w.Open {
			t.Errorf("service name %q was accepted", bad)
		}
	}
}

func TestWriteWindowReasonIsDistinct(t *testing.T) {
	if ReasonWriteWindowClosed == ReasonToolNotGranted || ReasonWriteWindowClosed == ReasonGranted {
		t.Fatal("the write-window denial must be its own reason")
	}
	if got := ReasonWriteWindowClosed.String(); got != "write_window_closed" {
		t.Errorf("String() = %q, want write_window_closed", got)
	}
}
