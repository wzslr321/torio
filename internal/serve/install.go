package serve

import (
	"context"
	"fmt"
	"strings"

	"github.com/wzslr321/torio/internal/guestexec"
	"github.com/wzslr321/torio/internal/lima"
)

// InstallReport is the structured outcome of Install: what was ensured so the
// operator sees exactly what a (re)run did.
type InstallReport struct {
	UnitPath      string
	Changed       bool // the unit file was (re)written this run
	LingerEnabled bool
	Validated     bool // the unit passed `systemd-analyze verify` before activation
	Enabled       bool // the unit is enabled for boot
}

// Install generates and installs the custom user unit for the Hermes backend
// and enables it for boot. It is idempotent and narrow. The hermes user gets
// linger enabled so a Restart=always user service runs without an interactive
// login session and survives reboot.
//
// Install does NOT start the backend; `Start` does. It accepts no secrets and
// writes no profile/provider data.
func (a *Adapter) Install(ctx context.Context) (InstallReport, error) {
	const op = "install"
	rep := InstallReport{UnitPath: unitPath}

	rt, e := a.runtimeDir(ctx, op)
	if e != nil {
		return rep, e
	}

	lres, e := a.sh(ctx, op, guestexec.RootExec("loginctl", "show-user", lima.HermesUser, "--property=Linger"))
	if e != nil {
		return rep, e
	}
	lingerYes := lres.ExitCode == 0 && strings.Contains(string(lres.Stdout), "Linger=yes")
	if !lingerYes {
		en, e := a.sh(ctx, op, guestexec.RootExec("loginctl", "enable-linger", lima.HermesUser))
		if e != nil {
			return rep, e
		}
		if en.ExitCode != 0 {
			return rep, &Error{Op: op, Kind: KindGuestCommandFailed, Err: cmdErr("loginctl enable-linger", en)}
		}
	}
	rep.LingerEnabled = true

	if md, e := a.sh(ctx, op, guestexec.UserExec("mkdir", "-p", unitDir)); e != nil {
		return rep, e
	} else if md.ExitCode != 0 {
		return rep, &Error{Op: op, Kind: KindGuestCommandFailed, Err: cmdErr("mkdir -p unit dir", md)}
	}

	rendered := renderUnit()
	cur, e := a.sh(ctx, op, guestexec.UserExec("cat", unitPath))
	if e != nil {
		return rep, e
	}
	existing := ""
	if cur.ExitCode == 0 {
		existing = string(cur.Stdout)
	}
	rep.Changed = existing != string(rendered)

	if rep.Changed {
		// Atomic write: stage → validate the staged file → rename into place. The
		// unit is never activated (or even placed) until it validates.
		if w, e := a.shInput(ctx, op, rendered, guestexec.UserExec("tee", stagingPath)); e != nil {
			return rep, e
		} else if w.ExitCode != 0 {
			return rep, &Error{Op: op, Kind: KindGuestCommandFailed, Err: cmdErr("write staging unit", w)}
		}
		if e := a.validateUnit(ctx, op, rt, stagingPath); e != nil {
			// Never leave a rejected staging file behind.
			_, _ = a.sh(ctx, op, guestexec.UserExec("rm", "-f", stagingPath))
			return rep, e
		}
		if mv, e := a.sh(ctx, op, guestexec.UserExec("mv", "-f", stagingPath, unitPath)); e != nil {
			return rep, e
		} else if mv.ExitCode != 0 {
			return rep, &Error{Op: op, Kind: KindGuestCommandFailed, Err: cmdErr("install unit", mv)}
		}
	} else {
		if e := a.validateUnit(ctx, op, rt, unitPath); e != nil {
			return rep, e
		}
	}
	rep.Validated = true

	if dr, e := a.sh(ctx, op, userctl(rt, "daemon-reload")); e != nil {
		return rep, e
	} else if dr.ExitCode != 0 {
		return rep, &Error{Op: op, Kind: KindGuestCommandFailed, Err: cmdErr("daemon-reload", dr)}
	}
	if ena, e := a.sh(ctx, op, userctl(rt, "enable", UnitName)); e != nil {
		return rep, e
	} else if ena.ExitCode != 0 {
		return rep, &Error{Op: op, Kind: KindGuestCommandFailed, Err: cmdErr("enable unit", ena)}
	}
	en, e := a.enabledState(ctx, op, rt)
	if e != nil {
		return rep, e
	}
	if en != "enabled" {
		return rep, &Error{Op: op, Kind: KindPostconditionFailed, Err: fmt.Errorf("unit is %q after enable, want enabled", en)}
	}
	rep.Enabled = true
	return rep, nil
}

// validateUnit runs `systemd-analyze --user verify` on a unit file and fails
// closed if it is rejected. This is the pre-activation gate: a wrong/incompatible
// ExecStart surface or a malformed directive is caught before the unit is
// enabled or started.
func (a *Adapter) validateUnit(ctx context.Context, op, rt, path string) *Error {
	res, e := a.sh(ctx, op, userEnv(rt, "systemd-analyze", "--user", "verify", path))
	if e != nil {
		return e
	}
	if res.ExitCode != 0 {
		return &Error{Op: op, Kind: KindValidationFailed, Err: fmt.Errorf("generated unit rejected by systemd-analyze: %s", bound(string(res.Stderr)))}
	}
	return nil
}
