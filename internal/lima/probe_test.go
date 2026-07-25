package lima

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"hermes-box.local/hb/internal/execx"
)

// wrapErr mirrors how execx wraps a context error: fmt.Errorf(...: %w...), so
// errors.Is still finds the sentinel through the adapter's own error type.
func wrapErr(sentinel error) error {
	return fmt.Errorf("run limactl --version: %w", sentinel)
}

func TestProbeSuccessUnpinned(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("limactl version 2.2.0\n")},
	}}
	a := New(fr)

	got, err := a.Probe(context.Background(), "")
	if err != nil {
		t.Fatalf("Probe: unexpected error: %v", err)
	}
	if got.Version != "2.2.0" {
		t.Fatalf("Version = %q, want 2.2.0", got.Version)
	}
	if got.Pinned {
		t.Fatalf("Pinned = true, want false (no pin supplied)")
	}

	if fr.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1", fr.callCount())
	}
	args := fr.callArgs(0)
	want := []string{"--version"}
	if !equalArgs(args, want) {
		t.Fatalf("argv = %v, want %v", args, want)
	}
}

func TestProbeSuccessPinnedMatch(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("limactl version 2.2.0\n")},
	}}
	a := New(fr)

	got, err := a.Probe(context.Background(), "2.2.0")
	if err != nil {
		t.Fatalf("Probe: unexpected error: %v", err)
	}
	if !got.Pinned {
		t.Fatalf("Pinned = false, want true")
	}
}

func TestProbePinnedMismatchFailsClosed(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("limactl version 2.2.0\n")},
	}}
	a := New(fr)

	_, err := a.Probe(context.Background(), "2.3.0")
	if err == nil {
		t.Fatalf("Probe: expected mismatch error, got nil")
	}
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindVersionMismatch {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindVersionMismatch)
	}
}

func TestProbeBinaryUnavailable(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: execx.Result{ExitCode: -1}, err: errors.New("run limactl --version: exec: \"limactl\": executable file not found in $PATH")},
	}}
	a := New(fr)

	_, err := a.Probe(context.Background(), "")
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindBinaryUnavailable {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindBinaryUnavailable)
	}
}

func TestProbeNonZeroExit(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: exitResult(1, "", "some failure")},
	}}
	a := New(fr)

	_, err := a.Probe(context.Background(), "")
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindCommandFailed {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindCommandFailed)
	}
}

func TestProbeMalformedOutput(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("not a version string\n")},
	}}
	a := New(fr)

	_, err := a.Probe(context.Background(), "")
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindMalformedOutput {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindMalformedOutput)
	}
}

func TestProbeTimeout(t *testing.T) {
	fr := &fakeRunner{respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: -1}, wrapErr(context.DeadlineExceeded)
	}}
	a := New(fr)

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	_, err := a.Probe(ctx, "")
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindTimeout {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindTimeout)
	}
}

func TestProbeCancellation(t *testing.T) {
	fr := &fakeRunner{respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: -1}, wrapErr(context.Canceled)
	}}
	a := New(fr)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.Probe(ctx, "")
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindCancelled {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindCancelled)
	}
}

func TestProbeRejectsNonSemverVersionGrammar(t *testing.T) {
	// The real, documented `limactl --version` output is a semver core
	// "MAJOR.MINOR.PATCH" (verified: "limactl version 2.2.0"; evidence in
	// docs/spike-results/evidence/etap-0d-lima-adapter/limactl-version.txt).
	// A permissive `\S+` capture would accept junk that is not a version and
	// hand a caller-facing "version" that no version-lock pin could ever
	// legitimately match — so anything that is not a semver version must fail
	// closed as malformed, not be echoed back as a version.
	bad := []string{
		"limactl version 2\n",       // missing minor/patch
		"limactl version 2.2\n",     // missing patch
		"limactl version 2.2.0.1\n", // extra component
		"limactl version v2.2.0\n",  // leading v is not in the CLI output grammar
		"limactl version banana\n",  // not a version at all
	}
	for _, out := range bad {
		fr := &fakeRunner{script: []scriptedResponse{{result: stdoutResult(out)}}}
		a := New(fr)

		_, err := a.Probe(context.Background(), "")
		var lerr *Error
		if !errors.As(err, &lerr) {
			t.Fatalf("Probe(%q): error is not *lima.Error: %v", out, err)
		}
		if lerr.Kind != KindMalformedOutput {
			t.Fatalf("Probe(%q): Kind = %v, want %v", out, lerr.Kind, KindMalformedOutput)
		}
	}
}

func TestProbeAcceptsSemverWithPreReleaseAndBuild(t *testing.T) {
	// The accepted grammar is a semver core plus the standard optional
	// pre-release/build metadata, so a legitimate dev/rc build is not
	// misclassified as malformed.
	cases := map[string]string{
		"limactl version 2.2.0\n":          "2.2.0",
		"limactl version 2.2.0-rc.1\n":     "2.2.0-rc.1",
		"limactl version 2.2.0-8-gdeadb\n": "2.2.0-8-gdeadb",
	}
	for out, want := range cases {
		fr := &fakeRunner{script: []scriptedResponse{{result: stdoutResult(out)}}}
		a := New(fr)

		got, err := a.Probe(context.Background(), "")
		if err != nil {
			t.Fatalf("Probe(%q): unexpected error: %v", out, err)
		}
		if got.Version != want {
			t.Fatalf("Probe(%q): Version = %q, want %q", out, got.Version, want)
		}
	}
}

// equalArgs compares two argv slices for exact equality (order matters: this
// is a typed argument array, never a shell string).
func equalArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
