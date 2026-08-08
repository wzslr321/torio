// Package serve is the typed, testable lifecycle for the Hermes backend
// (`hermes serve`) as a custom user systemd service on the Torio guest.
//
// The backend is the JSON-RPC/WebSocket gateway Hermes Desktop and remote
// clients connect to. Demo A binds it to guest loopback only (127.0.0.1); the
// Mac reaches it through an operator-controlled SSH tunnel (ADR-0002,
// docs/contracts/cli.md). This package generates and manages a
// narrow user unit for the dedicated non-root `hermes` identity, never a broad
// remote-shell facility: every guest action is a fixed argv run through the
// injected Guest transport (limactl shell in production), with bounded, redacted
// output. It renders no CLI output and makes no exit-code decisions; that is
// internal/cli's job.
//
// Discovery that fixes this design, probed against a live Hermes Agent v0.19.0
// backend on the guest:
//   - `hermes serve` defaults to --host 127.0.0.1 --port 9119 and exposes an
//     unauthenticated readiness endpoint GET /api/status -> 200 (JSON with a
//     version). /api/health, /api/info and /api/version all answer 401, so
//     /api/status is the only endpoint usable as an unauthenticated probe.
//     --skip-build serves the backend without an npm build step.
//   - `hermes serve --stop/--status` use naive process matching and are
//     unreliable: --status counted the querying process itself, and --stop
//     killed the real backend, then failed to kill its own query process
//     ("Operation not permitted") and exited 1. So the process is managed via
//     systemd and readiness is proven by systemd state + the HTTP probe.
//   - The `hermes` user is uid-distinct and needs linger enabled for a
//     Restart=always user service to run without a login session; user systemctl
//     is reached via XDG_RUNTIME_DIR=/run/user/<uid>.
package serve

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/wzslr321/torio/internal/backend"
	"github.com/wzslr321/torio/internal/execx"
	"github.com/wzslr321/torio/internal/guestexec"
	"github.com/wzslr321/torio/internal/lima"
)

// Guest runs a fixed argv inside the Torio VM and returns the bounded,
// redacted result. *lima.Adapter satisfies it (SSH / SSHInput over
// `limactl shell`), so serve is testable against a fake without a real VM.
type Guest interface {
	SSH(ctx context.Context, command []string) (execx.Result, error)
	SSHInput(ctx context.Context, stdin []byte, command []string) (execx.Result, error)
}

// Compile-time proof the production Lima adapter is a usable Guest.
var _ Guest = (*lima.Adapter)(nil)

// Adapter is the serve-lifecycle adapter. The zero value is not usable;
// construct with New.
type Adapter struct {
	// Guest reaches the VM. Tests inject a fake; production wires *lima.Adapter.
	Guest Guest

	// identity is the guest identity the unit runs as; spec is the service the
	// configured backend declares, nil when it declares none.
	identity backend.Identity
	spec     *backend.ServiceSpec
}

// New returns an Adapter driving b's declared service over guest. A backend
// that declares no service still produces a usable Adapter: every operation
// then answers Declared() == false rather than failing on a nil field, so the
// CLI can report the truthful state instead of an invented failure.
func New(guest Guest, b backend.Backend) *Adapter {
	return &Adapter{Guest: guest, identity: b.Identity(), spec: b.Service()}
}

// Declared reports whether the configured backend runs a guest service at all.
func (a *Adapter) Declared() bool { return a.spec != nil }

// Backend is the configured backend's identity name, for reports that have to
// say which backend answered.
func (a *Adapter) Backend() string { return a.identity.Name }

// UnitName is the user unit Torio owns for the backend, empty when the backend
// declares no service.
func (a *Adapter) UnitName() string {
	if a.spec == nil {
		return ""
	}
	return a.spec.UnitName
}

func (a *Adapter) unitDir() string  { return a.spec.UnitDir }
func (a *Adapter) unitPath() string { return a.spec.UnitDir + "/" + a.spec.UnitName }

// stagingPath is where a freshly rendered unit is written and validated before
// it is atomically moved into place. It keeps a .service suffix so
// `systemd-analyze verify` accepts it, and lives in the same directory as the
// unit so the move is an atomic same-filesystem rename.
func (a *Adapter) stagingPath() string {
	return a.spec.UnitDir + "/" + strings.TrimSuffix(a.spec.UnitName, ".service") + "-staging.service"
}

// EndpointURL is the loopback readiness URL probed on the guest.
func (a *Adapter) EndpointURL() string { return a.spec.EndpointURL() }

// user is the guest identity every unit-scoped command runs as.
func (a *Adapter) user() string { return a.identity.GuestUser }

// runtimeDir resolves the hermes user's XDG_RUNTIME_DIR (/run/user/<uid>) by
// probing `id -u hermes`. A user unit is reached through this runtime directory;
// resolving the uid (rather than hardcoding it) keeps the adapter correct if the
// guest is rebuilt, and fails closed on any unparseable/absent uid.
func (a *Adapter) runtimeDir(ctx context.Context, op string) (string, *Error) {
	res, err := a.Guest.SSH(ctx, []string{"id", "-u", a.user()})
	if err != nil {
		return "", fromGuestErr(op, err)
	}
	if res.ExitCode != 0 {
		return "", &Error{Op: op, Kind: KindGuestCommandFailed, Err: cmdErr("id -u "+a.user(), res)}
	}
	uidStr := strings.TrimSpace(string(res.Stdout))
	uid, perr := strconv.Atoi(uidStr)
	if perr != nil || uid < 0 {
		return "", &Error{Op: op, Kind: KindGuestCommandFailed, Err: fmt.Errorf("could not resolve the %s uid", a.user())}
	}
	return "/run/user/" + strconv.Itoa(uid), nil
}

// userctl builds `sudo -n -u <user> -- env XDG_RUNTIME_DIR=<rt> systemctl --user <args...>`.
// systemctl --user needs the runtime directory to reach the user manager.
func (a *Adapter) userctl(rt string, args ...string) []string {
	return a.userEnv(rt, append([]string{"systemctl", "--user"}, args...)...)
}

// userEnv builds `sudo -n -u <user> -- env XDG_RUNTIME_DIR=<rt> <args...>` for a
// non-systemctl guest command that still needs the user runtime dir (journalctl
// --user, systemd-analyze --user).
func (a *Adapter) userEnv(rt string, args ...string) []string {
	return guestexec.UserExecAs(a.user(), append([]string{"env", "XDG_RUNTIME_DIR=" + rt}, args...)...)
}

// noServiceErr is the fail-closed answer to a request to manage a service the
// configured backend does not declare. It names the backend, because the
// operator's next question is always which one they are talking to.
func (a *Adapter) noServiceErr(op string) *Error {
	return &Error{Op: op, Kind: KindNoService,
		Err: fmt.Errorf("backend %q declares no guest service; there is nothing to %s", a.identity.Name, op)}
}
