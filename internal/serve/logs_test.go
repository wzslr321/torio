package serve

import (
	"context"
	"strings"
	"testing"
)

func TestLogsBoundedByDefault(t *testing.T) {
	f := newFake(defaultEnv())
	a := New(f)

	rep, err := a.Logs(context.Background(), 0)
	if err != nil {
		t.Fatalf("Logs: unexpected error: %v", err)
	}
	if rep.Lines != DefaultLogLines {
		t.Errorf("Lines = %d, want default %d", rep.Lines, DefaultLogLines)
	}
	// It must ask journald for exactly this unit, bounded and non-paged.
	if !f.sawCommand("journalctl --user -u " + UnitName) {
		t.Errorf("logs did not scope journalctl to the backend unit")
	}
	if !f.sawCommand("--no-pager") {
		t.Errorf("logs must run journalctl non-interactively")
	}
	if !strings.Contains(strings.Join(f.joinedCalls(), " "), "-n 200") {
		t.Errorf("logs did not bound the line count")
	}
}

func TestLogsClampsExcessiveLineCount(t *testing.T) {
	f := newFake(defaultEnv())
	a := New(f)

	rep, err := a.Logs(context.Background(), 100000)
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if rep.Lines != maxLogLines {
		t.Errorf("Lines = %d, want clamped to %d", rep.Lines, maxLogLines)
	}
}

func TestLogsNotInstalled(t *testing.T) {
	env := defaultEnv()
	env.installed = false
	f := newFake(env)
	a := New(f)

	_, err := a.Logs(context.Background(), 50)
	assertKind(t, err, KindNotInstalled)
	if f.sawCommand("journalctl") {
		t.Errorf("must not read logs when the unit is not installed")
	}
}

func TestLogsScopedToUnitNeverProfileData(t *testing.T) {
	// Defense-in-depth: the journalctl invocation must be scoped to the unit and
	// must not read arbitrary files/paths (no profile/KB path in the argv).
	f := newFake(defaultEnv())
	a := New(f)
	if _, err := a.Logs(context.Background(), 10); err != nil {
		t.Fatalf("Logs: %v", err)
	}
	joined := strings.Join(f.joinedCalls(), " ")
	if strings.Contains(joined, ".hermes") || strings.Contains(joined, "/projects") {
		t.Errorf("logs argv referenced profile/KB data: %s", joined)
	}
}
