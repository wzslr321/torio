package serve

import (
	"context"
	"fmt"
	"strconv"
)

// DefaultLogLines is the number of recent journal lines Logs returns when the
// caller does not specify. It is bounded so `logs` never dumps an unbounded
// history, and execx additionally caps/redacts the retained bytes.
const DefaultLogLines = 200

// maxLogLines caps the caller-requested line count so a huge -n cannot be used
// to pull an unbounded journal through the transport.
const maxLogLines = 2000

// LogsReport carries the bounded, redacted recent journal for the backend unit.
type LogsReport struct {
	Unit  string
	Lines int
	Text  string
}

// Logs returns the last `lines` journal entries for the backend unit via
// `journalctl --user -u UNIT -n <lines> --no-pager`. It reads only this unit's
// own stdout/stderr journal — never profile, KB, or provider data — and the
// output is bounded (by -n and by execx's per-stream cap) and redacted. lines <=
// 0 uses DefaultLogLines; values above maxLogLines are clamped.
func (a *Adapter) Logs(ctx context.Context, lines int) (LogsReport, error) {
	if a.spec == nil {
		return LogsReport{}, a.noServiceErr("logs")
	}
	const op = "logs"
	if lines <= 0 {
		lines = DefaultLogLines
	}
	if lines > maxLogLines {
		lines = maxLogLines
	}
	rep := LogsReport{Unit: a.spec.UnitName, Lines: lines}

	rt, e := a.runtimeDir(ctx, op)
	if e != nil {
		return rep, e
	}

	inst, e := a.installed(ctx, op)
	if e != nil {
		return rep, e
	}
	if !inst {
		return rep, &Error{Op: op, Kind: KindNotInstalled, Err: fmt.Errorf("unit %q is not installed", a.spec.UnitName)}
	}

	res, e := a.sh(ctx, op, a.userEnv(rt, "journalctl", "--user", "-u", a.spec.UnitName, "-n", strconv.Itoa(lines), "--no-pager"))
	if e != nil {
		return rep, e
	}
	if res.ExitCode != 0 {
		return rep, &Error{Op: op, Kind: KindGuestCommandFailed, Err: cmdErr("journalctl", res)}
	}
	rep.Text = string(res.Stdout)
	return rep, nil
}
