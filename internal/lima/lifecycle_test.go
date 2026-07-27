package lima

import (
	"context"
	"errors"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
)

// --- Start ---

func TestStartAlreadyRunningIsIdempotent(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))},
	}}
	a := New(fr)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	if fr.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1 (must not issue `limactl start` when already running)", fr.callCount())
	}
}

func TestStartFromStopped(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Stopped"))},
		{result: exitResult(0, "", "")},
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))}, // postcondition re-query
	}}
	a := New(fr)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	if fr.callCount() != 3 {
		t.Fatalf("callCount = %d, want 3 (pre-check, start, postcondition re-query)", fr.callCount())
	}
	got := fr.callArgs(1)
	want := []string{"start", InstanceName, "--tty=false"}
	if !equalArgs(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	if !equalArgs(fr.callArgs(2), []string{"list", "--json", "--tty=false"}) {
		t.Fatalf("postcondition argv = %v, want `list --json --tty=false`", fr.callArgs(2))
	}
}

func TestStartMissingInstanceIsError(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("")},
	}}
	a := New(fr)

	err := a.Start(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindNotFound {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindNotFound)
	}
	if fr.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1 (must not attempt implicit create)", fr.callCount())
	}
}

func TestStartAmbiguousStateIsError(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Broken"))},
	}}
	a := New(fr)

	err := a.Start(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindAmbiguousState {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindAmbiguousState)
	}
	if fr.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1 (must not mutate on ambiguous state)", fr.callCount())
	}
}

func TestStartCommandFailed(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Stopped"))},
		{result: exitResult(1, "", "start failed")},
	}}
	a := New(fr)

	err := a.Start(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindCommandFailed {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindCommandFailed)
	}
}

func TestStartTimeout(t *testing.T) {
	fr := &fakeRunner{
		script: []scriptedResponse{
			{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Stopped"))},
		},
		respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
			return execx.Result{ExitCode: -1}, wrapErr(context.DeadlineExceeded)
		},
	}
	a := New(fr)

	err := a.Start(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindTimeout {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindTimeout)
	}
}

func TestStartFailsClosedWhenCommandExitsZeroButStillStopped(t *testing.T) {
	// A clean exit 0 from `limactl start` is not sufficient proof the instance
	// is running. The adapter must re-query and fail closed if the observed
	// post-state is not Running (mutation-postcondition rule).
	callN := 0
	fr := &fakeRunner{respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
		callN++
		switch callN {
		case 1:
			return stdoutResult(fixtureInstanceJSON(InstanceName, "Stopped")), nil // pre-check
		case 2:
			return exitResult(0, "", ""), nil // `limactl start` claims success
		case 3:
			return stdoutResult(fixtureInstanceJSON(InstanceName, "Stopped")), nil // postcondition: still stopped
		}
		t.Fatalf("unexpected call #%d: %v", callN, cmd.Args)
		return execx.Result{}, nil
	}}
	a := New(fr)

	err := a.Start(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("Start must fail when the instance is still Stopped after a `start` that exited 0: %v", err)
	}
	if lerr.Kind != KindPostconditionFailed {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindPostconditionFailed)
	}
}

// --- Stop ---

func TestStopAlreadyStoppedIsIdempotent(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Stopped"))},
	}}
	a := New(fr)

	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: unexpected error: %v", err)
	}
	if fr.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1 (must not issue `limactl stop` when already stopped)", fr.callCount())
	}
}

func TestStopFromRunning(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))},
		{result: exitResult(0, "", "")},
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Stopped"))}, // postcondition re-query
	}}
	a := New(fr)

	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: unexpected error: %v", err)
	}
	if fr.callCount() != 3 {
		t.Fatalf("callCount = %d, want 3 (pre-check, stop, postcondition re-query)", fr.callCount())
	}
	got := fr.callArgs(1)
	want := []string{"stop", InstanceName, "--tty=false"}
	if !equalArgs(got, want) {
		t.Fatalf("argv = %v, want %v (graceful stop, never --force, never delete)", got, want)
	}
	if !equalArgs(fr.callArgs(2), []string{"list", "--json", "--tty=false"}) {
		t.Fatalf("postcondition argv = %v, want `list --json --tty=false`", fr.callArgs(2))
	}
}

func TestStopMissingInstanceIsError(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult("")},
	}}
	a := New(fr)

	err := a.Stop(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindNotFound {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindNotFound)
	}
	if fr.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1", fr.callCount())
	}
}

func TestStopAmbiguousStateIsError(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Broken"))},
	}}
	a := New(fr)

	err := a.Stop(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindAmbiguousState {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindAmbiguousState)
	}
	if fr.callCount() != 1 {
		t.Fatalf("callCount = %d, want 1 (must not mutate on ambiguous state)", fr.callCount())
	}
}

func TestStopCommandFailed(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))},
		{result: exitResult(1, "", "stop failed")},
	}}
	a := New(fr)

	err := a.Stop(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindCommandFailed {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindCommandFailed)
	}
}

func TestStopTimeout(t *testing.T) {
	fr := &fakeRunner{
		script: []scriptedResponse{
			{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))},
		},
		respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
			return execx.Result{ExitCode: -1}, wrapErr(context.DeadlineExceeded)
		},
	}
	a := New(fr)

	err := a.Stop(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindTimeout {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindTimeout)
	}
}

func TestStopFailsClosedWhenCommandExitsZeroButStillRunning(t *testing.T) {
	// A clean exit 0 from `limactl stop` is not sufficient proof the instance
	// stopped. Re-query and fail closed if the observed post-state is not
	// Stopped (mutation-postcondition rule, symmetric with Start).
	fr := &fakeRunner{script: []scriptedResponse{
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))}, // pre-check
		{result: exitResult(0, "", "")},                                      // `limactl stop` claims success
		{result: stdoutResult(fixtureInstanceJSON(InstanceName, "Running"))}, // postcondition: still running
	}}
	a := New(fr)

	err := a.Stop(context.Background())
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("Stop must fail when the instance is still Running after a `stop` that exited 0: %v", err)
	}
	if lerr.Kind != KindPostconditionFailed {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindPostconditionFailed)
	}
}
