package execx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"hermes-box.local/hb/internal/redact"
)

// ExecRunner must satisfy the Runner interface.
var _ Runner = (*ExecRunner)(nil)

func TestRunCapturesStdoutAndZeroExit(t *testing.T) {
	r := &ExecRunner{}
	res, err := r.Run(context.Background(), Command{Name: "echo", Args: []string{"hello", "world"}})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if got := strings.TrimSpace(string(res.Stdout)); got != "hello world" {
		t.Errorf("stdout = %q, want %q", got, "hello world")
	}
}

func TestRunCapturesNonZeroExitWithoutError(t *testing.T) {
	r := &ExecRunner{}
	res, err := r.Run(context.Background(), Command{Name: "false"})
	if err != nil {
		t.Fatalf("Run returned error for clean non-zero exit: %v", err)
	}
	if res.ExitCode == 0 {
		t.Errorf("ExitCode = 0, want non-zero for `false`")
	}
}

// TestRunDoesNotUseShell proves arguments are passed as an array to the
// executable, not interpreted by a shell (no sh -c).
func TestRunDoesNotUseShell(t *testing.T) {
	r := &ExecRunner{}
	arg := "$HOME ; rm -rf / `id`"
	res, err := r.Run(context.Background(), Command{Name: "echo", Args: []string{arg}})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := strings.TrimSpace(string(res.Stdout)); got != arg {
		t.Errorf("argument was interpreted by a shell: stdout=%q, want literal %q", got, arg)
	}
}

// TestRunHonorsCommandTimeout proves the context timeout cancels a long-running
// process promptly.
func TestRunHonorsCommandTimeout(t *testing.T) {
	r := &ExecRunner{}
	start := time.Now()
	_, err := r.Run(context.Background(), Command{Name: "sleep", Args: []string{"5"}, Timeout: 50 * time.Millisecond})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected a timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

// TestRunHonorsContextCancellation proves an externally cancelled context stops
// the process.
func TestRunHonorsContextCancellation(t *testing.T) {
	r := &ExecRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Run(ctx, Command{Name: "sleep", Args: []string{"5"}})
	if err == nil {
		t.Fatalf("expected a cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

// TestRunRedactsSecretsInDiagnostics proves a secret in the argument array does
// not leak into the runner's error string.
func TestRunRedactsSecretsInDiagnostics(t *testing.T) {
	const secret = "swordfish-6b1e-canary"
	r := &ExecRunner{Redactor: redact.New(secret)}
	// A binary that does not exist forces a start error whose wrapper includes
	// the (redacted) command description.
	_, err := r.Run(context.Background(), Command{
		Name: "hb-nonexistent-binary-xyz",
		Args: []string{"--token", secret},
	})
	if err == nil {
		t.Fatalf("expected a start error, got nil")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error string leaked the secret")
	}
	if !strings.Contains(err.Error(), redact.Placeholder) {
		t.Errorf("error string missing redaction placeholder: %q", err.Error())
	}
}

func TestRunRejectsEmptyName(t *testing.T) {
	r := &ExecRunner{}
	if _, err := r.Run(context.Background(), Command{}); err == nil {
		t.Errorf("expected error for empty command name")
	}
}
