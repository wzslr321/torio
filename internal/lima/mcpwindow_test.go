package lima

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOpenWriteWindowWritesUnderTheBrokerIdentity(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("")}, // install -d write-windows
		{result: stdoutResult("")}, // tee the expiry as torio-mcp
		{result: stdoutResult("")}, // chmod 0600
	}}
	until := time.Date(2026, 7, 29, 12, 15, 0, 0, time.UTC)

	rep, err := New(fr).OpenWriteWindow(context.Background(), "atlassian", until)
	if err != nil {
		t.Fatalf("OpenWriteWindow: %v", err)
	}
	if !rep.Until.Equal(until) {
		t.Errorf("Until = %v, want %v", rep.Until, until)
	}

	var all []string
	for i := 0; i < fr.callCount(); i++ {
		all = append(all, strings.Join(fr.callArgs(i), " "))
	}
	joined := strings.Join(all, "\n")

	// The file must land under the broker's identity, not root's: the broker
	// reads it as itself, and a root-owned file in a 0700 broker directory is a
	// window the broker may not be able to replace later.
	if !strings.Contains(joined, "-u "+TorioMCPUser+" -- tee") {
		t.Errorf("the window was not written as %s:\n%s", TorioMCPUser, joined)
	}
	if !strings.Contains(joined, TorioMCPHome+"/write-windows") {
		t.Errorf("the window was not written into the broker home:\n%s", joined)
	}
	if !strings.Contains(joined, "chmod 600") && !strings.Contains(joined, "chmod 0600") {
		t.Errorf("the window file mode was never constrained:\n%s", joined)
	}

	// The payload is the instant, delivered as stdin rather than as an argv
	// element, so it never appears in a process listing.
	if got := string(fr.callStdin(1)); !strings.Contains(got, "2026-07-29T12:15:00Z") {
		t.Errorf("stdin = %q, want the RFC 3339 expiry", got)
	}
}

// TestOpenWriteWindowRejectsUnvalidatedService: the service name becomes a
// filename on the guest, so it is checked against the one shared rule before it
// reaches any command.
func TestOpenWriteWindowRejectsUnvalidatedService(t *testing.T) {
	for _, bad := range []string{"", "../etc/passwd", "Atlassian", "a b"} {
		fr := &fakeRunner{}
		_, err := New(fr).OpenWriteWindow(context.Background(), bad, time.Now().Add(time.Minute))
		if err == nil {
			t.Errorf("service %q was accepted", bad)
		}
		if fr.callCount() != 0 {
			t.Errorf("service %q reached the guest before validation", bad)
		}
	}
}

// TestOpenWriteWindowRefusesAnExpiredWindow: opening a window that is already
// closed would report success and change nothing an operator could observe.
func TestOpenWriteWindowRefusesAnExpiredWindow(t *testing.T) {
	fr := &fakeRunner{}
	if _, err := New(fr).OpenWriteWindow(context.Background(), "atlassian", time.Now().Add(-time.Minute)); err == nil {
		t.Fatal("an already-expired window was accepted")
	}
	if fr.callCount() != 0 {
		t.Error("an expired window still reached the guest")
	}
}
