// Package lima is the typed, testable adapter over `limactl` (ADR-0002 / ADR-0003).
// The V1 surface covers probe, status, init (trusted embedded template), start,
// stop, bootstrap, and ssh for the single Torio target VM. Every external call
// is an execx.Command argument array — never a shell string — run through the
// injected execx.Runner, so the whole surface is testable with a fake runner
// and never touches a real VM in unit tests. This package renders no CLI
// output and makes no exit-code decisions; that is internal/cli's
// responsibility.
package lima

import (
	"context"
	"fmt"
	"runtime"

	"github.com/wzslr321/torio/internal/config"
	"github.com/wzslr321/torio/internal/execx"
)

// InstanceName is the Lima VM instance this invocation manages.
//
// ADR-0002 still holds: exactly one Linux arm64 VM is the trust boundary, and
// Torio never manages several at once. What ADR-0001 changed is that the
// operator chooses which one, so a test run and a day's work do not share a
// Brain.
//
// It is a variable rather than a constant for exactly one reason: internal/cli
// assigns it once during startup, from a value internal/config has already
// validated, before any command runs and before anything touches a VM. Nothing
// else may write it. Read it freely — by the time any command executes it is
// fixed for the life of the process.
var InstanceName = config.DefaultInstance

// bin is the default limactl executable name, resolved via PATH.
const bin = "limactl"

// Adapter is the Lima adapter. The zero value is not usable; construct with
// New.
type Adapter struct {
	// Runner executes limactl. Tests inject a fake; production wires
	// execx.ExecRunner.
	Runner execx.Runner
	// Bin overrides the limactl executable name/path. Empty uses "limactl".
	Bin string
	// MCPGuestBinaryDir is the directory containing the two MCP guest payloads
	// shipped beside the host CLI. Empty resolves beside the running
	// executable. Tests set it to a private fixture directory.
	MCPGuestBinaryDir string
	// Profile carries the host-derived instance pins. New resolves it from the
	// running platform; tests set it explicitly so a pin assertion states which
	// host it is about rather than inheriting whichever machine ran it.
	//
	// It is a field and not a package-level variable on purpose. InstanceName is
	// one because internal/cli fixes it during startup, and that is already the
	// riskier shape: a package-level value captured at initialization time is
	// exactly how guest commands once ended up addressing the default VM while
	// lifecycle commands addressed the selected one.
	Profile Profile
}

// New returns an Adapter backed by runner, pinned to the host's profile.
//
// An unsupported host leaves the zero Profile rather than failing here, because
// a constructor that cannot report an error would have to panic. Every
// operation that depends on a pin calls profile() and fails closed with a
// message naming the host; internal/cli additionally rejects an unsupported
// host during startup, so the deep failure is a backstop and not the path an
// operator meets.
func New(runner execx.Runner) *Adapter {
	a := &Adapter{Runner: runner, Bin: bin}
	if p, err := HostProfile(); err == nil {
		a.Profile = p
	}
	return a
}

// profile returns the adapter's pins, or an error if it has none. Callers must
// not fall back to a default: comparing an instance against empty pins would
// accept every instance, which is the opposite of what the pins are for.
func (a *Adapter) profile() (Profile, error) {
	if !a.Profile.valid() {
		return Profile{}, fmt.Errorf("no instance pins for host %s/%s; Torio supports %s",
			runtime.GOOS, runtime.GOARCH, SupportedHosts())
	}
	return a.Profile, nil
}

func (a *Adapter) bin() string {
	if a.Bin != "" {
		return a.Bin
	}
	return bin
}

// run executes `limactl <args...>` with a fixed, explicit --tty=false so
// subcommands that could open an editor or prompt (list/create/start/stop/
// shell) never depend on stdout TTY auto-detection: every invocation is
// non-interactive by construction, not by accident of a captured pipe.
func (a *Adapter) run(ctx context.Context, args ...string) (execx.Result, error) {
	full := append(append([]string{}, args...), "--tty=false")
	return a.runRaw(ctx, full...)
}

// runRaw executes `limactl <args...>` verbatim, with no flags added. Used
// for top-level, read-only invocations (--version) that carry no
// interactivity risk and whose argv is a stable, documented contract.
func (a *Adapter) runRaw(ctx context.Context, args ...string) (execx.Result, error) {
	return a.Runner.Run(ctx, execx.Command{Name: a.bin(), Args: args})
}
