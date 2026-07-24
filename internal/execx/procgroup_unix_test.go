//go:build unix

package execx

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRunKillsDescendantsOnTimeout proves that a timeout cleans up the whole
// spawned process tree, not just the direct child. The helper spawns a
// long-lived `sleep` descendant in its process group and announces its PID; if
// group-kill were broken that descendant would outlive the poll window and the
// test would fail.
func TestRunKillsDescendantsOnTimeout(t *testing.T) {
	if !processGroupSupported {
		t.Skip("process-group cleanup not supported on this platform")
	}
	r := &ExecRunner{}
	cmd := helperCommand("spawn-descendant")
	cmd.Timeout = 150 * time.Millisecond
	res, err := r.Run(context.Background(), cmd)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}

	descPID := parseDescPID(t, string(res.Stdout))

	// The descendant must be gone (reaped -> ESRCH) shortly after the group kill.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Kill(descPID, 0); errors.Is(err, syscall.ESRCH) {
			return // descendant reaped: tree cleanup worked
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant pid %d still alive after timeout; process tree leaked", descPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func parseDescPID(t *testing.T, stdout string) int {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		if rest, ok := strings.CutPrefix(line, "DESC_PID="); ok {
			pid, err := strconv.Atoi(strings.TrimSpace(rest))
			if err != nil {
				t.Fatalf("bad DESC_PID line %q: %v", line, err)
			}
			return pid
		}
	}
	t.Fatalf("no DESC_PID in helper stdout: %q", stdout)
	return 0
}
