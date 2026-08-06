package lima

import (
	"context"
	"fmt"
)

// Start starts InstanceName. It is idempotent: if the instance is already
// Running, Start returns success without invoking `limactl start` again. A
// missing instance or one in an ambiguous state (Broken/Unknown) fails
// closed rather than guessing or implicitly creating/recreating it.
func (a *Adapter) Start(ctx context.Context) error {
	const op = "start"

	rec, err := a.currentInstance(ctx, op)
	if err != nil {
		return err
	}
	if rec == nil {
		return &Error{Op: op, Kind: KindNotFound, Err: fmt.Errorf("instance %q does not exist; run init first", InstanceName)}
	}
	st, ok := mapLimaStatus(rec.Status)
	if !ok {
		return &Error{Op: op, Kind: KindMalformedOutput, Err: fmt.Errorf("unrecognized lima status %q", rec.Status)}
	}
	switch st {
	case StateRunning:
		return nil // idempotent success
	case StateStopped:
		// fall through to the real start
	default:
		return &Error{Op: op, Kind: KindAmbiguousState, Err: fmt.Errorf("instance %q is in ambiguous state %q", InstanceName, rec.Status)}
	}

	res, err := a.run(ctx, "start", InstanceName)
	if err != nil {
		return classifyRunErr(op, err)
	}
	if res.ExitCode != 0 {
		return commandFailed(op, res.ExitCode, res.Stderr)
	}

	// A clean exit from `start` is not sufficient proof the instance is
	// actually running: re-query and verify before reporting success.
	verified, err := a.currentInstance(ctx, op)
	if err != nil {
		return err
	}
	if verified == nil {
		return &Error{Op: op, Kind: KindPostconditionFailed, Err: fmt.Errorf("instance %q not found after start", InstanceName)}
	}
	vst, ok := mapLimaStatus(verified.Status)
	if !ok || vst != StateRunning {
		return &Error{Op: op, Kind: KindPostconditionFailed, Err: fmt.Errorf("instance %q is %q after start, want running", InstanceName, verified.Status)}
	}
	return a.refreshSession(ctx, op)
}

// refreshSession replaces the multiplexed ssh session `limactl start` leaves
// behind.
//
// Provisioning runs over that very session, and its user stage adds the Lima
// login user to TorioProjectsGroup — after the session authenticated. Lima's
// generated ssh.config sets ControlMaster auto with ControlPersist yes, so every
// later `limactl shell` and `limactl copy` multiplexes over the same master and
// inherits the identity the login had *before* the group existed. The operator
// then cannot traverse HermesHome, which is 0710 root of the shared group, and
// `torio brain import` failed inside rsync with "cannot stat destination" on a
// guest where the group was correctly configured. Bootstrap read the group
// database and reported the operator a member, because it was: only the live
// session disagreed.
//
// Start is the one step after which the login identity can change, so the stale
// master is dropped here. Only after a real start: the idempotent path opened no
// new session, and tearing the master down under a running `torio project shell`
// would take the operator's own session with it.
func (a *Adapter) refreshSession(ctx context.Context, op string) error {
	prefix := guestShellArgs()
	args := append([]string{"shell", "--reconnect"}, prefix[1:]...)
	res, err := a.runRaw(ctx, append(args, "true")...)
	if err != nil {
		return classifyRunErr(op, err)
	}
	if res.ExitCode != 0 {
		return &Error{Op: op, Kind: KindPostconditionFailed, Err: fmt.Errorf("instance %q started but no guest session could be established", InstanceName)}
	}
	return nil
}

// Stop stops InstanceName. It is the mirror of Start: idempotent (an already
// Stopped instance returns success without invoking `limactl stop` again), a
// missing instance is KindNotFound, and an ambiguous state (Broken/Unknown)
// fails closed rather than being silently mutated. `stop` is graceful — it
// never passes --force, and per docs/contracts/cli.md it removes neither the
// VM nor its data. As with Start, a
// clean exit from `stop` is not accepted as proof: Stop re-queries and requires
// the observed post-state to be Stopped, else fails closed
// (KindPostconditionFailed).
func (a *Adapter) Stop(ctx context.Context) error {
	const op = "stop"

	rec, err := a.currentInstance(ctx, op)
	if err != nil {
		return err
	}
	if rec == nil {
		return &Error{Op: op, Kind: KindNotFound, Err: fmt.Errorf("instance %q does not exist", InstanceName)}
	}
	st, ok := mapLimaStatus(rec.Status)
	if !ok {
		return &Error{Op: op, Kind: KindMalformedOutput, Err: fmt.Errorf("unrecognized lima status %q", rec.Status)}
	}
	switch st {
	case StateStopped:
		return nil // idempotent success
	case StateRunning:
		// fall through to the real stop
	default:
		return &Error{Op: op, Kind: KindAmbiguousState, Err: fmt.Errorf("instance %q is in ambiguous state %q", InstanceName, rec.Status)}
	}

	res, err := a.run(ctx, "stop", InstanceName)
	if err != nil {
		return classifyRunErr(op, err)
	}
	if res.ExitCode != 0 {
		return commandFailed(op, res.ExitCode, res.Stderr)
	}

	// A clean exit from `stop` is not sufficient proof the instance actually
	// stopped: re-query and verify before reporting success.
	verified, err := a.currentInstance(ctx, op)
	if err != nil {
		return err
	}
	if verified == nil {
		return &Error{Op: op, Kind: KindPostconditionFailed, Err: fmt.Errorf("instance %q not found after stop", InstanceName)}
	}
	vst, ok := mapLimaStatus(verified.Status)
	if !ok || vst != StateStopped {
		return &Error{Op: op, Kind: KindPostconditionFailed, Err: fmt.Errorf("instance %q is %q after stop, want stopped", InstanceName, verified.Status)}
	}
	return nil
}
