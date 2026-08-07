package serve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// assertKind asserts err is a *serve.Error with the wanted kind.
func assertKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	var serr *Error
	if !errors.As(err, &serr) {
		t.Fatalf("error is not *serve.Error: %v", err)
	}
	if serr.Kind != want {
		t.Fatalf("Kind = %v, want %v (err: %v)", serr.Kind, want, err)
	}
}

// wrapErr mirrors execx's %w wrapping so errors.Is finds the context cause.
func wrapErr(cause error) error { return fmt.Errorf("run limactl: %w", cause) }

func TestInstallFreshInstallsValidatesEnables(t *testing.T) {
	env := defaultEnv()
	env.existingAbsent = true // no unit yet → a fresh write
	f := newFake(env)
	a := newTestAdapter(f)

	rep, err := a.Install(context.Background())
	if err != nil {
		t.Fatalf("Install: unexpected error: %v", err)
	}
	if !rep.Changed || !rep.LingerEnabled || !rep.Validated || !rep.Enabled {
		t.Fatalf("report = %+v, want all of Changed/LingerEnabled/Validated/Enabled", rep)
	}
	// The generated unit must be written via a fed stdin (tee), never as an argv
	// element, and it must be the exact golden bytes.
	stdin, ok := f.stdinFor("tee " + stagingPath)
	if !ok {
		t.Fatalf("unit was not written via a stdin-fed tee")
	}
	if string(stdin) != string(renderUnit()) {
		t.Errorf("written unit != rendered unit")
	}
	if !strings.Contains(string(stdin), "--host 127.0.0.1") || !strings.Contains(string(stdin), "Restart=always") {
		t.Errorf("written unit missing loopback bind / restart policy")
	}
}

func TestInstallValidatesBeforeActivation(t *testing.T) {
	env := defaultEnv()
	env.existingAbsent = true
	f := newFake(env)
	a := newTestAdapter(f)
	if _, err := a.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	verify := f.indexOf("systemd-analyze --user verify")
	mv := f.indexOf("mv -f " + stagingPath)
	reload := f.indexOf("systemctl --user daemon-reload")
	enable := f.indexOf("systemctl --user enable")
	if verify < 0 || mv < 0 || reload < 0 || enable < 0 {
		t.Fatalf("missing a step: verify=%d mv=%d reload=%d enable=%d", verify, mv, reload, enable)
	}
	// verify must precede placing the unit and any activation.
	if !(verify < mv && verify < reload && verify < enable) {
		t.Errorf("validation did not precede activation: verify=%d mv=%d reload=%d enable=%d", verify, mv, reload, enable)
	}
}

func TestInstallIdempotentWhenUnchanged(t *testing.T) {
	// existing == rendered (defaultEnv): no rewrite, but still validated+enabled.
	f := newFake(defaultEnv())
	a := newTestAdapter(f)

	rep, err := a.Install(context.Background())
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if rep.Changed {
		t.Errorf("Changed = true, want false for an unchanged unit")
	}
	if f.sawCommand("tee "+stagingPath) || f.sawCommand("mv -f "+stagingPath) {
		t.Errorf("unchanged install must not rewrite the unit file")
	}
	if !rep.Validated || !rep.Enabled {
		t.Errorf("unchanged install must still validate and enable: %+v", rep)
	}
}

func TestInstallEnablesLingerWhenAbsent(t *testing.T) {
	env := defaultEnv()
	env.lingerYes = false
	f := newFake(env)
	a := newTestAdapter(f)
	if _, err := a.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !f.sawCommand("loginctl enable-linger " + "hermes") {
		t.Errorf("linger was not enabled when absent")
	}
}

func TestInstallSkipsLingerWhenPresent(t *testing.T) {
	f := newFake(defaultEnv()) // lingerYes true
	a := newTestAdapter(f)
	if _, err := a.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if f.sawCommand("enable-linger") {
		t.Errorf("linger must not be re-enabled when already yes")
	}
}

func TestInstallRejectsInvalidUnitBeforeActivation(t *testing.T) {
	env := defaultEnv()
	env.existingAbsent = true
	env.verifyOK = false // systemd-analyze rejects the unit
	f := newFake(env)
	a := newTestAdapter(f)

	_, err := a.Install(context.Background())
	assertKind(t, err, KindValidationFailed)
	// The rejected staging file must be rolled back, and nothing activated.
	if !f.sawCommand("rm -f " + stagingPath) {
		t.Errorf("rejected staging unit was not rolled back")
	}
	if f.sawCommand("mv -f "+stagingPath) || f.sawCommand("systemctl --user enable") {
		t.Errorf("an invalid unit must never be placed or enabled")
	}
}

func TestInstallPostconditionEnableFails(t *testing.T) {
	env := defaultEnv()
	env.enabled = "disabled" // is-enabled still reports disabled after enable
	f := newFake(env)
	a := newTestAdapter(f)

	_, err := a.Install(context.Background())
	assertKind(t, err, KindPostconditionFailed)
}

func TestInstallTransportErrorIsTimeout(t *testing.T) {
	f := &fakeGuest{env: defaultEnv(), transportErr: wrapErr(context.DeadlineExceeded)}
	a := newTestAdapter(f)
	_, err := a.Install(context.Background())
	assertKind(t, err, KindTimeout)
}
