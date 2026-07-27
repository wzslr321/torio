package execx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/wzslr321/torio/internal/redact"
)

// TestHelperProcess is not a real test: when re-executed with HB_HELPER_MODE
// set, it acts as a controllable child process. This is the standard os/exec
// testing pattern and avoids any shell.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv("HB_HELPER_MODE") {
	case "":
		return // ordinary test run: no-op
	case "emit":
		payload := os.Getenv("HB_HELPER_PAYLOAD")
		fmt.Fprint(os.Stdout, "OUT:"+payload)
		fmt.Fprint(os.Stderr, "ERR:"+payload)
		os.Exit(0)
	case "spawn-descendant":
		// Start a long-lived descendant in this process's group, announce its
		// PID, then block. A process-group kill must take the descendant too.
		child := exec.Command("sleep", "60")
		if err := child.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "helper: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "DESC_PID=%d\n", child.Process.Pid)
		_ = os.Stdout.Sync()
		// A real sleep (not select{}) keeps a timer goroutine alive so the Go
		// runtime does not trip its deadlock detector and self-exit before the
		// caller's timeout fires.
		time.Sleep(60 * time.Second)
		os.Exit(0)
	default:
		os.Exit(3)
	}
}

// helperCommand builds a Command that re-executes this test binary in helper
// mode. env entries are additional KEY=VALUE pairs for the child.
func helperCommand(mode string, env ...string) Command {
	full := append(os.Environ(), "HB_HELPER_MODE="+mode)
	full = append(full, env...)
	return Command{
		Name: os.Args[0],
		Args: []string{"-test.run=^TestHelperProcess$"},
		Env:  full,
	}
}

func TestRunRedactsRetainedOutputBothStreams(t *testing.T) {
	const secret = "swordfish-6b1e-canary"
	r := &ExecRunner{Redactor: redact.New(secret)}

	res, err := r.Run(context.Background(), helperCommand("emit", "HB_HELPER_PAYLOAD="+secret))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
	for name, out := range map[string][]byte{"stdout": res.Stdout, "stderr": res.Stderr} {
		if strings.Contains(string(out), secret) {
			t.Errorf("%s leaked the registered-literal secret", name)
		}
		if !strings.Contains(string(out), redact.Placeholder) {
			t.Errorf("%s not redacted: %q", name, string(out))
		}
	}
}

func TestRunBoundsRetainedOutputPerStream(t *testing.T) {
	r := &ExecRunner{MaxOutputPerStream: 10}
	payload := strings.Repeat("A", 100)

	res, err := r.Run(context.Background(), helperCommand("emit", "HB_HELPER_PAYLOAD="+payload))
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(res.Stdout) != 10 {
		t.Errorf("retained stdout len = %d, want 10 (deterministic cap)", len(res.Stdout))
	}
	if !res.StdoutTruncated {
		t.Errorf("StdoutTruncated = false, want true")
	}
	if !res.StderrTruncated {
		t.Errorf("StderrTruncated = false, want true")
	}
}
