package lima

import (
	"context"
	"errors"
	"testing"

	"github.com/wzslr321/torio/internal/execx"
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
	want := []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--"}
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
	want := []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "echo", "hello world", "--looks-like-a-flag"}
	if !equalArgs(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
}

func TestSSHUsesInstanceSelectedAfterPackageInitialization(t *testing.T) {
	previous := InstanceName
	InstanceName = "torio-e2e"
	t.Cleanup(func() { InstanceName = previous })

	for _, tc := range []struct {
		name string
		call func(*Adapter) error
	}{
		{name: "SSH", call: func(a *Adapter) error {
			_, err := a.SSH(context.Background(), []string{"true"})
			return err
		}},
		{name: "SSHInput", call: func(a *Adapter) error {
			_, err := a.SSHInput(context.Background(), []byte("payload"), []string{"tee", "/tmp/x"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fr := &fakeRunner{script: []scriptedResponse{{result: exitResult(0, "", "")}}}
			if err := tc.call(New(fr)); err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.name, err)
			}
			got := fr.callArgs(0)
			if len(got) < 5 || got[4] != "torio-e2e" {
				t.Fatalf("argv = %v, want selected instance at argv[4]", got)
			}
		})
	}
}

// TestSSHPinsTheGuestWorkingDirectory guards the flag that keeps guest commands
// out of the Lima login user's home.
//
// Without it `limactl shell` falls back to that home whenever the host's working
// directory does not exist on the guest — which is always, since the V1 template
// mounts nothing. Torio runs its guest commands as hermes, which cannot enter
// the operator's home, so a command that restores its initial directory before
// exiting fails there after producing correct output: GNU find exits 1 with
// "Failed to restore initial working directory", and `torio brain init` reported
// a failed guest command on a healthy machine.
func TestSSHPinsTheGuestWorkingDirectory(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(a *Adapter) error
	}{
		{"SSH", func(a *Adapter) error {
			_, err := a.SSH(context.Background(), []string{"find", "/home/hermes/brain"})
			return err
		}},
		{"SSHInput", func(a *Adapter) error {
			_, err := a.SSHInput(context.Background(), []byte("x"), []string{"tee", "/tmp/x"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fr := &fakeRunner{script: []scriptedResponse{{result: exitResult(0, "", "")}}}
			a := New(fr)
			if err := tc.call(a); err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.name, err)
			}
			got := fr.callArgs(0)
			at := -1
			for i, arg := range got {
				if arg == "--workdir" {
					at = i
					break
				}
			}
			if at < 0 || at+1 >= len(got) {
				t.Fatalf("argv = %v, want a --workdir flag", got)
			}
			if got[at+1] != "/" {
				t.Fatalf("--workdir = %q, want %q", got[at+1], "/")
			}
			// limactl parses its own flags before the instance name; a --workdir
			// after it would be handed to the remote command instead.
			for i, arg := range got {
				if arg == InstanceName {
					if i < at {
						t.Fatalf("argv = %v, want --workdir before the instance name", got)
					}
					break
				}
			}
		})
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

func TestSSHInputExactArgvAndStdin(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{{result: exitResult(0, "", "")}}}
	a := New(fr)

	payload := []byte("[Unit]\nDescription=x\n")
	res, err := a.SSHInput(context.Background(), payload, []string{"tee", "/tmp/x"})
	if err != nil {
		t.Fatalf("SSHInput: unexpected error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", res.ExitCode)
	}
	got := fr.callArgs(0)
	want := []string{"shell", "--tty=false", "--workdir", "/", InstanceName, "--", "tee", "/tmp/x"}
	if !equalArgs(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	if string(fr.callStdin(0)) != string(payload) {
		t.Fatalf("stdin = %q, want %q", fr.callStdin(0), payload)
	}
}

func TestSSHInputBinaryUnavailable(t *testing.T) {
	fr := &fakeRunner{script: []scriptedResponse{
		{result: execx.Result{ExitCode: -1}, err: errors.New("run limactl: executable file not found in $PATH")},
	}}
	a := New(fr)

	_, err := a.SSHInput(context.Background(), []byte("x"), []string{"tee", "/tmp/x"})
	var lerr *Error
	if !errors.As(err, &lerr) {
		t.Fatalf("error is not *lima.Error: %v", err)
	}
	if lerr.Kind != KindBinaryUnavailable {
		t.Fatalf("Kind = %v, want %v", lerr.Kind, KindBinaryUnavailable)
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
