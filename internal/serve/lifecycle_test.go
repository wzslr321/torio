package serve

import (
	"context"
	"testing"
	"time"
)

func TestStartHappyPathProvesReadiness(t *testing.T) {
	f := newFake(defaultEnv())
	a := New(f)

	rep, err := a.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	if !rep.Ready || !rep.Active || !rep.EndpointReady {
		t.Fatalf("report = %+v, want Ready/Active/EndpointReady", rep)
	}
	if rep.EndpointCode != 200 {
		t.Errorf("EndpointCode = %d, want 200", rep.EndpointCode)
	}
	if rep.Version != "0.19.0" {
		t.Errorf("Version = %q, want 0.19.0", rep.Version)
	}
	// Start must actually run the start verb and then re-query is-active.
	if !f.sawCommand("systemctl --user start " + UnitName) {
		t.Errorf("start verb not issued")
	}
	if !f.sawCommand("systemctl --user is-active") {
		t.Errorf("start did not re-query is-active as a postcondition")
	}
	if !f.sawCommand("curl") {
		t.Errorf("start did not probe the loopback endpoint")
	}
}

func TestStartNotInstalledIsPrecondition(t *testing.T) {
	env := defaultEnv()
	env.installed = false
	f := newFake(env)
	a := New(f)

	_, err := a.Start(context.Background())
	assertKind(t, err, KindNotInstalled)
	if f.sawCommand("systemctl --user start") {
		t.Errorf("must not start when the unit is not installed")
	}
}

func TestStartPostconditionInactiveFailsClosed(t *testing.T) {
	// systemctl start exits 0 but the re-queried state is not active.
	env := defaultEnv()
	env.active = "failed"
	f := newFake(env)
	a := New(f)

	_, err := a.Start(context.Background())
	assertKind(t, err, KindPostconditionFailed)
}

func TestStartActiveButEndpointDeadFailsClosed(t *testing.T) {
	// THE negative case: the service is active but the loopback endpoint never
	// answers 200 (connection refused → code 000). This must fail closed as a
	// verification failure, not be reported as ready.
	old := endpointRetryDelay
	endpointRetryDelay = time.Millisecond
	defer func() { endpointRetryDelay = old }()

	env := defaultEnv()
	env.active = "active"
	env.endpointCode = "000" // curl could not connect
	f := newFake(env)
	a := New(f)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	rep, err := a.Start(ctx)
	assertKind(t, err, KindEndpointUnready)
	if !rep.Active {
		t.Errorf("Active = false, want true (the process IS active)")
	}
	if rep.EndpointReady || rep.Ready {
		t.Errorf("must not report endpoint/overall ready: %+v", rep)
	}
}

func TestStartEndpointComesUpAfterRetries(t *testing.T) {
	// The endpoint is not ready on the first probes, then answers 200: Start must
	// poll and succeed rather than give up on the first non-200.
	old := endpointRetryDelay
	endpointRetryDelay = time.Millisecond
	defer func() { endpointRetryDelay = old }()

	env := defaultEnv()
	env.endpointCodeSeq = []string{"000", "000", "200"}
	f := newFake(env)
	a := New(f)

	rep, err := a.Start(context.Background())
	if err != nil {
		t.Fatalf("Start: unexpected error after retryable endpoint: %v", err)
	}
	if !rep.Ready {
		t.Fatalf("report = %+v, want Ready after the endpoint came up", rep)
	}
}

func TestStartEndpointActiveButNon200FailsClosed(t *testing.T) {
	// Active + a listening endpoint that returns 503 (degraded/erroring) is still
	// not ready: readiness requires exactly 200.
	old := endpointRetryDelay
	endpointRetryDelay = time.Millisecond
	defer func() { endpointRetryDelay = old }()

	env := defaultEnv()
	env.endpointCode = "503"
	f := newFake(env)
	a := New(f)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := a.Start(ctx)
	assertKind(t, err, KindEndpointUnready)
}

func TestStart200WithoutHermesVersionFailsClosed(t *testing.T) {
	// A foreign listener on port 9119 answering a bare 200 (no Hermes version)
	// must NOT satisfy Start's readiness. awaitEndpoint keeps polling and Start
	// fails closed on the deadline rather than reporting a not-Hermes backend ready.
	old := endpointRetryDelay
	endpointRetryDelay = time.Millisecond
	defer func() { endpointRetryDelay = old }()

	env := defaultEnv()
	env.active = "active"
	env.endpointCode = "200"
	env.endpointBody = `{"served_by":"some-other-app"}` // 200 but no version
	f := newFake(env)
	a := New(f)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	rep, err := a.Start(ctx)
	assertKind(t, err, KindEndpointUnready)
	if rep.EndpointReady || rep.Ready {
		t.Errorf("must not report ready for a 200 without a Hermes version: %+v", rep)
	}
	if rep.Version != "" {
		t.Errorf("Version = %q, want empty", rep.Version)
	}
}

func TestRestartProvesReadiness(t *testing.T) {
	f := newFake(defaultEnv())
	a := New(f)

	rep, err := a.Restart(context.Background())
	if err != nil {
		t.Fatalf("Restart: unexpected error: %v", err)
	}
	if !rep.Ready {
		t.Fatalf("report = %+v, want Ready", rep)
	}
	if !f.sawCommand("systemctl --user restart " + UnitName) {
		t.Errorf("restart verb not issued")
	}
}

func TestStopIdempotentWhenAlreadyInactive(t *testing.T) {
	env := defaultEnv()
	env.active = "inactive"
	f := newFake(env)
	a := New(f)

	rep, err := a.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop: unexpected error: %v", err)
	}
	if rep.Active {
		t.Errorf("Active = true, want false")
	}
	if f.sawCommand("systemctl --user stop") {
		t.Errorf("must not issue stop when already inactive (idempotent)")
	}
}

func TestStopStopsActiveServiceWithPostcondition(t *testing.T) {
	// is-active returns active (pre), then inactive (post-stop).
	env := defaultEnv()
	env.activeSeq = []string{"active", "inactive"}
	f := newFake(env)
	a := New(f)

	rep, err := a.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop: unexpected error: %v", err)
	}
	if rep.Active {
		t.Errorf("Active = true after stop, want false")
	}
	if !f.sawCommand("systemctl --user stop " + UnitName) {
		t.Errorf("stop verb not issued")
	}
}

func TestStopPostconditionStillActiveFailsClosed(t *testing.T) {
	// systemctl stop exits 0 but the service is still active on re-query.
	env := defaultEnv()
	env.activeSeq = []string{"active", "active"}
	f := newFake(env)
	a := New(f)

	_, err := a.Stop(context.Background())
	assertKind(t, err, KindPostconditionFailed)
}

func TestStopNotInstalledIsPrecondition(t *testing.T) {
	env := defaultEnv()
	env.installed = false
	f := newFake(env)
	a := New(f)

	_, err := a.Stop(context.Background())
	assertKind(t, err, KindNotInstalled)
}
