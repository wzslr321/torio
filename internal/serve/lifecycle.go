package serve

import (
	"context"
	"fmt"
	"time"
)

// endpointRetryDelay is the pause between loopback readiness probes while a
// freshly (re)started backend finishes binding. It is a package var so tests can
// shrink it; the overall wait is bounded by the caller's context deadline.
var endpointRetryDelay = 500 * time.Millisecond

// Start starts the backend service and proves readiness. It requires the unit to
// be installed (run `torio serve install` first), starts it via the user manager,
// then fails closed unless BOTH postconditions hold: the re-queried systemd
// state is active AND the loopback /api/status endpoint answers 200. An active
// process with a dead endpoint is a failure (docs/contracts/cli.md).
// Start is idempotent — starting an already-running, ready backend succeeds.
func (a *Adapter) Start(ctx context.Context) (StatusReport, error) {
	return a.activate(ctx, "start", "start")
}

// Restart restarts the backend and proves readiness with the same postconditions
// as Start. Unlike Start it always cycles the process (systemctl restart).
// Session/state persistence is the backend's own responsibility.
func (a *Adapter) Restart(ctx context.Context) (StatusReport, error) {
	return a.activate(ctx, "restart", "restart")
}

// activate runs `systemctl --user <verb> UNIT`, then verifies the active-state
// and loopback-endpoint postconditions.
func (a *Adapter) activate(ctx context.Context, op, verb string) (StatusReport, error) {
	rep := StatusReport{URL: EndpointURL()}

	rt, e := a.runtimeDir(ctx, op)
	if e != nil {
		return rep, e
	}

	inst, e := a.installed(ctx, op)
	if e != nil {
		return rep, e
	}
	if !inst {
		return rep, &Error{Op: op, Kind: KindNotInstalled, Err: fmt.Errorf("unit %q is not installed; run `torio serve install` first", UnitName)}
	}
	rep.Installed = true
	if en, e := a.enabledState(ctx, op, rt); e != nil {
		return rep, e
	} else {
		rep.Enabled = en == "enabled"
	}

	if r, e := a.sh(ctx, op, userctl(rt, verb, UnitName)); e != nil {
		return rep, e
	} else if r.ExitCode != 0 {
		return rep, &Error{Op: op, Kind: KindGuestCommandFailed, Err: cmdErr("systemctl "+verb, r)}
	}

	// Postcondition 1: re-query the active state — a clean `systemctl` exit is
	// not accepted as proof the service is running.
	act, e := a.activeState(ctx, op, rt)
	if e != nil {
		return rep, e
	}
	rep.ActiveState = act
	rep.Active = act == "active"
	if !rep.Active {
		return rep, &Error{Op: op, Kind: KindPostconditionFailed, Err: fmt.Errorf("service is %q after %s, want active", act, verb)}
	}

	// Postcondition 2: prove the loopback endpoint is actually serving, polling
	// until it answers 200 or the context deadline is reached.
	code, version, e := a.awaitEndpoint(ctx, op)
	if e != nil {
		return rep, e
	}
	rep.EndpointCode = code
	rep.EndpointReady = endpointReady(code, version)
	rep.Version = version
	rep.Ready = rep.Active && rep.EndpointReady
	if !rep.EndpointReady {
		return rep, &Error{Op: op, Kind: KindEndpointUnready, Err: endpointUnreadyErr(code, version)}
	}
	return rep, nil
}

// awaitEndpoint polls the loopback readiness endpoint until it is ready (200 AND
// a parseable Hermes version) or the context deadline is reached, returning the
// last observed code and parsed version. A bare 200 without a version keeps
// polling — a stale/foreign listener must not satisfy readiness. A transport
// failure is returned as an Error; a not-ready answer is returned as data so the
// caller can classify "active but endpoint dead" distinctly from a transport
// problem.
func (a *Adapter) awaitEndpoint(ctx context.Context, op string) (int, string, *Error) {
	var lastCode int
	var lastVersion string
	for {
		code, version, e := a.probeEndpoint(ctx, op)
		if e != nil {
			return 0, "", e
		}
		lastCode, lastVersion = code, version
		if endpointReady(code, version) {
			return code, version, nil
		}
		select {
		case <-ctx.Done():
			return lastCode, lastVersion, nil
		case <-time.After(endpointRetryDelay):
		}
	}
}

// StopReport is the structured outcome of Stop.
type StopReport struct {
	ActiveState string // raw is-active value after stop
	Active      bool   // still active? (must be false on success)
}

// Stop stops the backend service. It is idempotent: an already-inactive service
// succeeds without acting. As with Start it does not trust the `systemctl` exit
// code — it re-queries is-active and requires a non-active post-state, else fails
// closed. Stop never removes the unit, profile, or state.
func (a *Adapter) Stop(ctx context.Context) (StopReport, error) {
	const op = "stop"
	rep := StopReport{}

	rt, e := a.runtimeDir(ctx, op)
	if e != nil {
		return rep, e
	}

	inst, e := a.installed(ctx, op)
	if e != nil {
		return rep, e
	}
	if !inst {
		return rep, &Error{Op: op, Kind: KindNotInstalled, Err: fmt.Errorf("unit %q is not installed", UnitName)}
	}

	act, e := a.activeState(ctx, op, rt)
	if e != nil {
		return rep, e
	}
	if act != "active" && act != "activating" {
		// Already stopped (inactive/failed): idempotent success.
		rep.ActiveState = act
		rep.Active = false
		return rep, nil
	}

	if r, e := a.sh(ctx, op, userctl(rt, "stop", UnitName)); e != nil {
		return rep, e
	} else if r.ExitCode != 0 {
		return rep, &Error{Op: op, Kind: KindGuestCommandFailed, Err: cmdErr("systemctl stop", r)}
	}

	post, e := a.activeState(ctx, op, rt)
	if e != nil {
		return rep, e
	}
	rep.ActiveState = post
	rep.Active = post == "active"
	if rep.Active {
		return rep, &Error{Op: op, Kind: KindPostconditionFailed, Err: fmt.Errorf("service is %q after stop, want inactive", post)}
	}
	return rep, nil
}
