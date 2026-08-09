//go:build darwin

package sshagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wzslr321/torio/internal/execx"
)

type fakeRunner struct {
	result execx.Result
	err    error
	seen   execx.Command
}

func (r *fakeRunner) Run(_ context.Context, cmd execx.Command) (execx.Result, error) {
	r.seen = cmd
	return r.result, r.err
}

// TestOsascriptProgramIsAFixedConstant is the injection guard. The dialog is an
// AppleScript program, and a project name, branch or remote concatenated into it
// would be a program the guest helped write. Everything variable travels in the
// environment instead.
func TestOsascriptProgramIsAFixedConstant(t *testing.T) {
	if !strings.Contains(osascriptProgram, `system attribute "`+promptEnvVar+`"`) {
		t.Error("the dialog text does not come from the environment")
	}
	for _, forbidden := range []string{"%s", "%v", "\" & ", " & \""} {
		if strings.Contains(osascriptProgram, forbidden) {
			t.Errorf("the AppleScript program contains %q; it must take no interpolation", forbidden)
		}
	}
	// Return, Escape and the close box must all refuse. Approval is the only
	// outcome that takes an aimed click.
	if !strings.Contains(osascriptProgram, `default button "Deny"`) || !strings.Contains(osascriptProgram, `cancel button "Deny"`) {
		t.Error("the dialog does not default to Deny on both Return and Escape")
	}
}

func TestAskOperatorApprovesOnlyOnAnAimedAllow(t *testing.T) {
	for name, tc := range map[string]struct {
		result   execx.Result
		err      error
		approved bool
	}{
		"allow":                {result: execx.Result{ExitCode: 0, Stdout: []byte("button returned:Allow")}, approved: true},
		"deny":                 {result: execx.Result{ExitCode: 1, Stdout: []byte("")}},
		"closed":               {result: execx.Result{ExitCode: 1, Stdout: []byte("execution error: User canceled. (-128)")}},
		"killed on expiry":     {result: execx.Result{ExitCode: -1}},
		"no dialog":            {err: context.DeadlineExceeded},
		"zero exit, no answer": {result: execx.Result{ExitCode: 0, Stdout: []byte("")}},
	} {
		runner := &fakeRunner{result: tc.result, err: tc.err}
		err := askOperator(context.Background(), runner, time.Second, "message")
		if tc.approved && err != nil {
			t.Errorf("%s: askOperator() error = %v, want approval", name, err)
		}
		if !tc.approved && err == nil {
			t.Errorf("%s: askOperator() approved the signature", name)
		}
	}
}

// TestAskOperatorBoundsTheDialogWithTheCommand proves the timeout kills the
// process that owns the window. A deadline checked after the fact would leave an
// expired dialog on screen for someone to click later.
func TestAskOperatorBoundsTheDialogWithTheCommand(t *testing.T) {
	runner := &fakeRunner{result: execx.Result{ExitCode: 1}}
	if err := askOperator(context.Background(), runner, 90*time.Second, "message"); err == nil {
		t.Fatal("askOperator() approved a denial")
	}
	if runner.seen.Timeout != 90*time.Second {
		t.Errorf("dialog command timeout = %v, want the confirmer's", runner.seen.Timeout)
	}
	if runner.seen.Name != "osascript" {
		t.Errorf("dialog command = %q, want osascript", runner.seen.Name)
	}
	var carried bool
	for _, kv := range runner.seen.Env {
		if strings.HasPrefix(kv, promptEnvVar+"=") {
			carried = true
			if !strings.Contains(kv, "message") {
				t.Errorf("dialog message did not reach the environment: %q", kv)
			}
		}
	}
	if !carried {
		t.Errorf("dialog command does not carry %s", promptEnvVar)
	}
}
