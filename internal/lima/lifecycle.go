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
	return nil
}
