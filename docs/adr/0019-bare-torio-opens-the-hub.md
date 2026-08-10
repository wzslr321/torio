# ADR-0019: Running `torio` with no command on a terminal opens an interactive hub

- Status: Accepted
- Date: 2026-08-10
- Applies to: `internal/cli`, `internal/tui`, `docs/contracts/cli.md`

## Context

Setting Torio up for the first time takes between seven and ten commands, and
the operator learns them from a runbook rather than from the binary. The route
is `vm init`, `vm start`, `vm bootstrap` with a timeout the default does not
cover, then either `backend login` or `serve install` and `serve start`
depending on which backend the instance runs, then `brain init`, then
`project add`. Two of those commands need a non-default `--timeout` that the
operator has to remember, and which branch to take after bootstrap depends on
facts the operator cannot see without running another command.

The binary does guide the operator, one step at a time: most commands end by
printing a `next:` line. That guidance is spread across `vm.go`, `serve.go`,
`project.go` and `status.go`, and no single place holds the whole route. The
consequences are visible in the surface. A command knows what follows itself
and nothing else, so a box that was never created has nothing to print at all,
and the operator who lost their place has no way to ask where they are. The
branch after bootstrap is decided by a chain of conditions in
`writeBootstrapNextStep` that no other surface can reuse, so the tmux status
line and the table view cannot say what to do next even though they already
read the facts that would answer it.

The long operations compound this. Bootstrap issues many bounded guest probes
and may build a backend from source, which on a fresh box takes minutes. It
prints nothing until it returns. An operator who has not been told that cannot
tell a slow bootstrap from a hung one, and the honest answer is not available
anywhere in the output.

None of this is a defect in any one command. Each behaves correctly on its own.
What is missing is a surface that holds the order, and a terminal program is
the shape that fits: it can hold state between steps, redraw while work is in
flight, and derive its guidance from the same facts the commands prove.

## Decision

**Running `torio` with no command on a terminal opens an interactive hub.**
The hub covers setup and the day-to-day surfaces: the cross-box poll, the
project registry, the Second Brain, and the guest service.

**Every existing command keeps working exactly as it does now, and the hub adds
no logic of its own.** Each action it offers calls the same manager the
equivalent command calls, through seams the command layer fills in
(`internal/cli/ui.go`). The hub is a second way to reach the operations, never a
second implementation of them, so the two surfaces cannot drift into
disagreeing about what an operation does.

**What is not a terminal keeps the answer it has today, byte for byte.** A
piped invocation, a CI job, and a `--json` caller all still read
`torio: no command given; run 'torio --help'` on stderr and exit 2. Only both
standard input and standard output being a terminal opens the hub: input is
where the hub reads keys and output is where it draws, and a full-screen
program written into a pipe produces escape sequences nobody can read.

**`torio ui` names the hub explicitly.** A wrapper or a keybinding that means
to open it gets a precondition failure (exit 3, `NOT_A_TERMINAL`) where no
terminal exists, rather than a usage error that reads as a misspelled command.
The empty invocation stays a usage error there, because that is what it has
always been.

**The hub emits no JSON.** `--json` remains exactly one document on stdout, and
a hub is not a document. Asking for both is a usage error, which is the rule
`torio status` already applies to `--json` with a non-default `--format`.

**The order of setup becomes one pure function.** `wizard.Next(Facts)` in
`internal/tui/wizard` derives the next step from facts that were proven, and
`wizard.Plan(Facts)` derives the whole route for the backend in hand. The
dashboard's guidance is the same call, so the hub cannot tell an operator one
thing while a command tells them another. The scattered `next:` lines stay
where they are: they are correct, they serve the operator who is using the
commands directly, and removing them would be a separate change with its own
risk.

**Steps a backend does not declare are absent, not unmet.** A Claude Code box
has no service to install and its route contains no serve steps. A Hermes box
declares no auth check and its route contains no login. This is ADR-0009's rule
applied to guidance: an absent capability is a state, and a route that listed
unreachable steps would describe a journey that does not exist.

**A credential is only pushed toward a login when it was proven absent.** An
unprovable credential is not evidence of a logged-out box, and routing it to a
login on no evidence teaches the operator to ignore the step that means it.

**Operations bound themselves without a flag.** An ordinary operation uses the
invocation's timeout; bootstrap, brain initialization and service installation
use the policy maximum, which is the value the runbook already tells the
operator to pass by hand. One operation runs at a time, because two concurrent
guest operations against one box race over the same state.

**Interactive sessions get the real terminal.** Backend login, agent sessions
and shells are handed the terminal through the same argv the commands build.
The hub releases the screen, the session runs as itself with the operator's
interrupt reaching it rather than the hub, and the hub redraws when it ends.

The hub depends on `bubbletea`, `lipgloss` and `bubbles` for rendering and on
`golang.org/x/term` for the terminal check. The standard library has no
cell-addressed renderer, no key and escape-sequence decoder, and no raw-mode
state manager. Hand-rolling those is more code and more failure modes than the
pinned dependency, and a terminal left in raw mode by a program that crashed is
a failure an operator cannot recover from without knowing to type `reset`.

## Consequences

The root module's dependency graph grows by roughly a dozen modules. They are
render-path only: nothing in the set reads configuration, spawns a process, or
touches the credential boundary, and every one is pure Go, so release builds
stay `CGO_ENABLED=0 -trimpath`. This is the cost of the decision and it is paid
in the module that holds the credential boundary. It is worth naming plainly
rather than discovering at review.

The order of setup now exists in two places that must agree: the graph in
`internal/tui/wizard`, and the `next:` lines the commands print. The graph is
tested against every edge, including the ones that decide between backends. A
change to what follows what has to move both, and a change that moves only the
lines leaves the hub telling the truth and the command surface lying, which is
the direction that fails visibly rather than silently.

`torio` with no arguments now has two outcomes rather than one, decided by
something no argument shows. An operator debugging a script that behaves
differently in their terminal than in CI has one more thing to know about. The
non-terminal answer being byte-identical is what keeps that knowable: the
difference is visible in the first line of output, not in a subtle change of
behaviour.

The hub silences the logger while it owns the screen. `--verbose` therefore
means nothing in the hub, and diagnostics for an operation the hub ran have to
be obtained by running the equivalent command. This is a real loss and the
alternative is worse: a slog line arriving on stderr underneath a full-screen
program corrupts the frame without informing anybody.

Bootstrap still reports nothing until it returns, because the adapter
accumulates its checks internally and streams none of them. The hub shows a
spinner, the elapsed time, and how long the step can legitimately take, which
distinguishes slow from hung without claiming progress it cannot observe. Live
per-check output would need a callback on `lima.BootstrapOptions`, which is a
change to the adapter and belongs to its own decision.

The hub is fixed for its lifetime to the instance and backend resolved before
dispatch, because that resolution happens once and a hub that redid it could
disagree with the invocation it belongs to. Switching backends means quitting
and running again with `--backend`. The dashboard still shows every box on the
host, so nothing is hidden by the limitation.

## Rejected

**A separate `torio-ui` binary.** It would need its own copy of instance
resolution, configuration loading and backend resolution, which is the exact
sequence ADR-0001 keeps in one place, and a second binary would drift from the
first the moment either changed. It also solves nothing the subcommand does not:
the discoverability problem is that operators do not know what to run, and a
second thing to install is not an answer to that.

**Opening the hub whenever `torio` is run, terminal or not.** Every pipeline,
CI job and wrapper script that runs the binary without arguments today reads a
stable error. Answering them with escape sequences would break each one, and
would break them in the way that is hardest to diagnose, since the output is
unreadable and the program does not exit.

**Making the hub a `--format` of `torio status`.** The format set is closed and
every member of it renders the same document to a different shape. A hub is not
a rendering of the status document: it runs operations, holds state between
them, and covers surfaces the poll does not describe.

**Deprecating the setup commands in favour of the hub.** They are what scripts,
CI and the platform end-to-end suite drive, and they are the only surface that
produces machine-readable output. The hub exists because the commands are hard
to sequence by hand, which is not a reason to take them away from the callers
that sequence them correctly.

**`huh` for the forms.** The hub collects a machine size, a project id and a
remote. Three text inputs and a confirmation do not justify a second form
abstraction and its dependency tree, and AGENTS asks that a mechanism not be
added merely because a library offers it.

**Golden-frame tests through `teatest`.** It lives under an experimental module
path, and pinned frames of rendered escape sequences break on any styling
change without a defect having occurred. The models are pure functions of
messages, so their behaviour is tested directly and their output is asserted on
by content.

**Removing the `next:` lines from the commands now that the graph exists.**
They are the guidance for the operator using the commands directly, which the
platform suite and every script still do. Deleting them would take working
guidance away from that surface in a change whose subject is a different one.
