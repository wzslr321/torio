// Package lima is the typed, testable adapter over `limactl` (ADR-0003 / ADR-0015).
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

	"github.com/wzslr321/torio/internal/execx"
)

// InstanceName is the single Lima VM instance Torio manages. ADR-0003
// places exactly one Linux arm64 VM as the trust boundary for Demo A; torio
// does not manage multiple named instances.
const InstanceName = "torio"

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
}

// New returns an Adapter backed by runner.
func New(runner execx.Runner) *Adapter {
	return &Adapter{Runner: runner, Bin: bin}
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
