# ADR-0021: The hub rebinds to another backend through the seam that bound it first

- Status: Accepted
- Date: 2026-08-11
- Applies to: `internal/cli`, `internal/tui`
- Supersedes: ADR-0019 in part, the fixed-lifetime binding clause in its
  Consequences ("The hub is fixed for its lifetime to the instance and backend
  resolved before dispatch")

## Decision

**The hub may rebind, during its lifetime, to a different backend and the
instance that runs it.** Rebinding goes through one seam the command layer
fills: it re-runs the same resolution dispatch runs, returns a whole new
`Deps`, and the hub swaps the struct, discards every probed fact, and probes
again from nothing. The hub still holds no constructor and resolves nothing
itself. A rebind while an operation is in flight is refused, the same way a
second operation is. What ADR-0019 fixed for the lifetime of the program is
now fixed for the lifetime of one binding.

### Premises

- P1. Binding resolution belongs to the command layer; a hub holding only
  seams cannot drift from the invocation that filled them.
- P2. Operators move between backends within one sitting; quit, relaunch and
  renavigate is a cost paid on every switch, not once.
- P3. A rebind that re-runs pre-dispatch resolution cannot disagree with an
  invocation, because it is the same code, run the same way, once per binding.
- P4. A screen that discards all probed state on rebind can show nothing that
  belongs to the previous box.

## Walkthrough

Before: the operator is in the hub on hermes and wants Claude Code on the same
project. They press q, run `torio --backend claude-code`, wait for the new hub
to probe, press 3, and look for the project, which is there only if it was
added on that instance too.

After: they press the rebind key, pick claude-code, the header changes, the
hub re-probes from nothing, and Enter on the project opens the agent session.
Switching changes the box, not the registry it shows: the project still has to
exist on the instance switched to.

## Context

Operator feedback from real use (2026-08-11): working a project in the hermes
hub, the operator pressed Enter expecting to choose how to continue, and the
best the hub could offer was the gateway panel plus quit-and-relaunch for
anything else. ADR-0019 wrote the limitation into its consequences with the
rationale that resolution "happens once and a hub that redid it could disagree
with the invocation it belongs to". That conflates where resolution happens
with how often it may run. The rule worth protecting is P1, and P1 survives a
rebind that goes through the command layer.

## Consequences

Implementation waits on the feature freeze (#43): this record moves the
architecture, not the binary. `Deps` gains a rebind seam, which is one more
function and still no constructor in the hub. Every screen must now survive
its state being reset mid-session, which is new: until this decision a
screen's facts could only go stale, never vanish. The backend chooser this
enables on a project is only as useful as the project's presence on the other
instance; registering one project on two instances is issue #32's settlement,
deliberately not decided here.

## Rejected

**Keeping quit-and-relaunch.** It preserves ADR-0019's clause at the operator's
expense: the whole launch, probe and navigation cost, paid on every switch, to
answer a question the hub already understands. The feedback that opened this
record is an operator declining to pay it.

**The hub resolving backends itself.** Importing the constructors into the hub
answers the same feedback and dissolves the one rule that keeps two surfaces
from disagreeing about what an operation does. ADR-0019 got that part right,
and this record keeps it: resolution stays in the command layer.

**Two live bindings at once.** A split view of hermes and claude doubles guest
connections and probed state, and the one-operation-at-a-time rule has no
honest home when two boxes are on screen. Every action would need to name its
box, which reintroduces the ambiguity the fixed binding existed to prevent.

**Auto-registering the project on the target instance during a switch.** It
hides a mutation inside navigation. Adding a project is an explicit act with a
deploy key a human authorizes (ADR-0018), and it belongs to the settlement of
issue #32, not to a keypress that changes what the header says.
