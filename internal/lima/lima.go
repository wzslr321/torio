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
// Torio never *manages* several at once — one invocation starts, stops,
// bootstraps and opens sessions on exactly this instance. What ADR-0001 changed
// is that the operator chooses which one, so a test run and a day's work do not
// share a Brain.
//
// It is a variable rather than a constant for exactly one reason: internal/cli
// assigns it once during startup, from a value internal/config has already
// validated, before any command runs and before anything touches a VM. Nothing
// else may write it. Read it freely — by the time any command executes it is
// fixed for the life of the process.
//
// A status poll reads several instances (ADR-0012) and is the one caller that
// addresses a box this name does not name. It does so through ForInstance,
// which returns an adapter carrying its own target, and never by assigning
// here: a global that a loop writes and restores is how guest commands once
// ended up in the wrong VM.
var InstanceName = config.DefaultInstance

// bin is the default limactl executable name, resolved via PATH.
const bin = "limactl"

// Adapter is the Lima adapter. The zero value is not usable; construct with
// New.
type Adapter struct {
	// Runner executes limactl. Tests inject a fake; production wires
	// execx.ExecRunner.
	Runner execx.Runner
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

	// instance, when set, is the instance this adapter addresses instead of
	// InstanceName. Only ForInstance sets it, and nothing clears it: an
	// adapter's target is fixed when it is made, so a caller holding one cannot
	// be redirected by whatever a neighbouring loop is doing.
	instance string
}

// ForInstance returns a copy of a addressed at the named instance.
//
// It exists for the status poll, which asks several boxes the same question in
// one invocation (ADR-0012). The alternative — assigning InstanceName around
// each iteration — is the shape that already produced a wrong-VM bug once, and
// it would leave the global holding the last polled instance for whatever ran
// next. A copy carries its target in the value the caller is holding, so there
// is no window in which the process disagrees with itself about which box it is
// talking to.
//
// The name is not validated here. The one caller reads names out of `limactl
// list --json` and validates them there, where an invalid name is a fact about
// the enumeration rather than a programming error at the call site.
func (a *Adapter) ForInstance(name string) *Adapter {
	c := *a
	c.instance = name
	return &c
}

// target is the instance this adapter addresses: its own when ForInstance gave
// it one, and otherwise the invocation's.
func (a *Adapter) target() string {
	if a.instance != "" {
		return a.instance
	}
	return InstanceName
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
	a := &Adapter{Runner: runner}
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
	return a.Runner.Run(ctx, execx.Command{Name: bin, Args: args})
}
