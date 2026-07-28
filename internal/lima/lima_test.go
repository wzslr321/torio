package lima

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
)

type ctxKey struct{}

// TestContextIsPropagatedNotReplaced guards against an adapter method
// silently building its own context.Background() for the external call
// instead of forwarding (a derivative of) the caller's bounded context.
func TestContextIsPropagatedNotReplaced(t *testing.T) {
	var seen context.Context
	fr := &fakeRunner{respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
		seen = ctx
		return stdoutResult(`{"name":"` + InstanceName + `","status":"Running"}` + "\n"), nil
	}}
	a := New(fr)

	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	if _, err := a.Status(ctx); err != nil {
		t.Fatalf("Status: unexpected error: %v", err)
	}
	if seen == nil {
		t.Fatalf("runner never invoked")
	}
	if v, _ := seen.Value(ctxKey{}).(string); v != "marker" {
		t.Fatalf("adapter did not propagate the caller's context (marker missing)")
	}
}

func TestNewDefaultsBinToLimactl(t *testing.T) {
	a := New(&fakeRunner{})
	if a.bin() != "limactl" {
		t.Fatalf("bin() = %q, want %q", a.bin(), "limactl")
	}
}

// The instance name must reach limactl from the resolved value, never from a
// literal. This is the guard ADR-0021 promises: a new call site that hardcodes
// "torio" would work in production and silently ignore the operator's choice,
// which is the one failure this mechanism cannot tolerate.
func TestNoHardcodedInstanceLiteralInProductionCode(t *testing.T) {
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			code, _, _ := strings.Cut(line, "//")
			if strings.Contains(code, `"torio"`) {
				t.Errorf("%s:%d hardcodes the instance name; use InstanceName", path, i+1)
			}
		}
	}
}

// Reading it must follow the selected instance, and the ssh alias with it —
// Lima derives the alias from the instance name, so a fixed alias would open an
// operator shell on the wrong VM.
func TestInstanceNameDrivesTheSSHAlias(t *testing.T) {
	original := InstanceName
	t.Cleanup(func() { InstanceName = original })

	InstanceName = "torio-test"
	if got, want := sshHostAlias(), "lima-torio-test"; got != want {
		t.Errorf("sshHostAlias() = %q, want %q", got, want)
	}
}
