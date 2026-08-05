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
// Discovery that fixes this design (sanitized evidence under
// archive/pre-v1:docs/spike-results/evidence/d5-serve-discovery-*):
//   - `hermes serve` defaults to --host 127.0.0.1 --port 9119 and exposes an
//     unauthenticated readiness endpoint GET /api/status -> 200 (JSON with a
//     version). --skip-build serves the backend without an npm build step.
//   - `hermes serve --stop/--status` use naive process matching (they count the
//     querying process itself) and are unreliable, so the process is managed via
//     systemd and readiness is proven by systemd state + the HTTP probe.
//   - The `hermes` user is uid-distinct and needs linger enabled for a
//     Restart=always user service to run without a login session; user systemctl
//     is reached via XDG_RUNTIME_DIR=/run/user/<uid>.
package serve

import (
	"context"
	"strconv"
	"strings"

	"github.com/wzslr321/torio/internal/execx"
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
}

// New returns an Adapter backed by guest.
func New(guest Guest) *Adapter { return &Adapter{Guest: guest} }

// The fixed, repository-controlled service facts. Loopback bind is a hard
// invariant (docs/contracts/cli.md); the values are constants —
// never caller input — so the generated unit and every probe target a single,
// auditable loopback endpoint and can never be widened to a public bind.
const (
	// UnitName is the custom user unit Torio owns for the backend.
	UnitName = "hermes-serve.service"
	// unitDir is the hermes user's systemd unit directory.
	unitDir = lima.HermesHome + "/.config/systemd/user"
	// unitPath is the installed unit's absolute path.
	unitPath = unitDir + "/" + UnitName
	// stagingPath is where a freshly rendered unit is written and validated
	// before it is atomically moved into place. It keeps a .service suffix so
	// `systemd-analyze verify` accepts it, and lives in the same directory as
	// unitPath so the move is an atomic same-filesystem rename.
	stagingPath = unitDir + "/hermes-serve-staging.service"

	// BindHost/BindPort are the discovered `hermes serve` loopback defaults. The
	// unit pins them explicitly so the bind can never drift off loopback.
	BindHost = "127.0.0.1"
	BindPort = 9119
	// StatusPath is the unauthenticated readiness endpoint (verified: 200 with a
	// JSON version; /api/health|info|version are 401).
	StatusPath = "/api/status"

	// hermesShim is the stable `hermes` launcher path the bootstrap installs on
	// sudo's secure_path; the unit's ExecStart uses it as an absolute path.
	hermesShim = "/usr/local/bin/hermes"
	// workingDir is the Hermes install directory (`hermes --version` reports it),
	// used as the service WorkingDirectory.
	workingDir = lima.HermesHome + "/hermes-agent"
)

// EndpointURL is the loopback readiness URL probed on the guest. It is fixed to
// the pinned loopback host/port and the status path.
func EndpointURL() string {
	return "http://" + BindHost + ":" + strconv.Itoa(BindPort) + StatusPath
}

// runtimeDir resolves the hermes user's XDG_RUNTIME_DIR (/run/user/<uid>) by
// probing `id -u hermes`. A user unit is reached through this runtime directory;
// resolving the uid (rather than hardcoding it) keeps the adapter correct if the
// guest is rebuilt, and fails closed on any unparseable/absent uid.
func (a *Adapter) runtimeDir(ctx context.Context, op string) (string, *Error) {
	res, err := a.Guest.SSH(ctx, []string{"id", "-u", lima.HermesUser})
	if err != nil {
		return "", fromGuestErr(op, err)
	}
	if res.ExitCode != 0 {
		return "", &Error{Op: op, Kind: KindGuestCommandFailed, Err: cmdErr("id -u hermes", res)}
	}
	uidStr := strings.TrimSpace(string(res.Stdout))
	uid, perr := strconv.Atoi(uidStr)
	if perr != nil || uid < 0 {
		return "", &Error{Op: op, Kind: KindGuestCommandFailed, Err: errf("could not resolve the hermes uid")}
	}
	return "/run/user/" + strconv.Itoa(uid), nil
}

// userctl builds `sudo -n -u hermes -- env XDG_RUNTIME_DIR=<rt> systemctl --user <args...>`.
// systemctl --user needs the runtime directory to reach the user manager.
func userctl(rt string, args ...string) []string {
	base := []string{"sudo", "-n", "-u", lima.HermesUser, "--", "env", "XDG_RUNTIME_DIR=" + rt, "systemctl", "--user"}
	return append(base, args...)
}

// userEnv builds `sudo -n -u hermes -- env XDG_RUNTIME_DIR=<rt> <args...>` for a
// non-systemctl guest command that still needs the user runtime dir (journalctl
// --user, systemd-analyze --user).
func userEnv(rt string, args ...string) []string {
	base := []string{"sudo", "-n", "-u", lima.HermesUser, "--", "env", "XDG_RUNTIME_DIR=" + rt}
	return append(base, args...)
}

// userExec builds `sudo -n -u hermes -- <args...>` for a plain guest command run
// as the hermes identity (no user-manager runtime dir needed).
func userExec(args ...string) []string {
	return append([]string{"sudo", "-n", "-u", lima.HermesUser, "--"}, args...)
}

// rootExec builds `sudo -n -- <args...>` for a system command run as the Lima
// login user with root (loginctl linger management).
func rootExec(args ...string) []string {
	return append([]string{"sudo", "-n", "--"}, args...)
}
