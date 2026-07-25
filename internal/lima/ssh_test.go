package lima

import (
	"context"
	"errors"
	"testing"

	"hermes-box.local/hb/internal/execx"
)

func TestSSHExactArgvNoCommand(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: exitResult(0, "", "")},
	}}
	a := New(fr)

	if _, err := a.SSH(context.Background(), nil); err != nil {
		t.Fatalf("SSH: unexpected error: %v", err)
	}
	got := fr.callArgs(0)
	want := []string{"shell", "--tty=false", InstanceName, "--"}
	if !equalArgs(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
}

func TestSSHExactArgvWithCommand(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: exitResult(0, "hello\n", "")},
	}}
	a := New(fr)

	res, err := a.SSH(context.Background(), []string{"echo", "hello world", "--looks-like-a-flag"})
	if err != nil {
		t.Fatalf("SSH: unexpected error: %v", err)
	}
	if string(res.Stdout) != "hello\n" {
		t.Fatalf("Stdout = %q, want %q", res.Stdout, "hello\n")
	}

	got := fr.callArgs(0)
	want := []string{"shell", "--tty=false", InstanceName, "--", "echo", "hello world", "--looks-like-a-flag"}
	if !equalArgs(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
}

func TestSSHArgumentsPreservedAsSeparateElements(t *testing.T) {
	// A single command argument containing spaces/shell metacharacters must
	// arrive at the runner as one argv element, never concatenated into a
	// shell string.
	fr := &fakeRunner{script: []scriptedResponse{{result: exitResult(0, "", "")}}}
	a := New(fr)

	dangerous := "; rm -rf / #"
	if _, err := a.SSH(context.Background(), []string{dangerous}); err != nil {
		t.Fatalf("SSH: unexpected error: %v", err)
	}
	got := fr.callArgs(0)
	if len(got) == 0 || got[len(got)-1] != dangerous {
		t.Fatalf("argv tail = %v, want last element to be the single literal %q", got, dangerous)
	}
}

func TestSSHNonZeroExitIsNotAdapterError(t *testing.T) {
	// A remote command's own non-zero exit is not an execution failure of
	// the adapter: the caller reads Result.ExitCode, same contract as execx.
	fr := &fakeRunner{script: []scriptedResponse{
		{result: exitResult(3, "", "remote command failed")},
	}}
	a := New(fr)

	res, err := a.SSH(context.Background(), []string{"false"})
	if err != nil {
		t.Fatalf("SSH: unexpected adapter error for a clean non-zero remote exit: %v", err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("ExitCode = %d, want 3", res.ExitCode)
	}
}

func TestSSHBinaryUnavailable(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: execx.Result{ExitCode: -1}, err: errors.New("run limactl: exec: \"limactl\": executable file not found in $PATH")},
	}}
	a := New(fr)

	_, err := a.SSH(context.Background(), []string{"true"})
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindBinaryUnavailable {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindBinaryUnavailable)
	}
}

func TestSSHTimeout(t *testing.T) {
	fr := &fakeRunner{respond: func(ctx context.Context, cmd execx.Command) (execx.Result, error) {
		return execx.Result{ExitCode: -1}, wrapErr(context.DeadlineExceeded)
	}}
	a := New(fr)

	_, err := a.SSH(context.Background(), []string{"true"})
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindTimeout {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindTimeout)
	}
}
