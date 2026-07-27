package lima

import (
	"context"
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
		return stdoutResult("limactl version 2.2.0\n"), nil
	}}
	a := New(fr)

	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	if _, err := a.Probe(ctx, ""); err != nil {
		t.Fatalf("Probe: unexpected error: %v", err)
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
