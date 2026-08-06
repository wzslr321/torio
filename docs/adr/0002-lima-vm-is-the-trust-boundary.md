# ADR-0002: The Lima VM is the trust boundary

- Status: Accepted
- Date: 2026-08-05
- Consolidates: the Lima trust boundary and the pinned-image rule. The superseded
  originals are recoverable at `git show archive/pre-oss:docs/adr/…` (`0003`,
  `0010`).
- Applies to: `internal/lima`, `internal/serve`

## Context

An agent with a full terminal on the operator's machine has too large a blast
radius. Dev
Containers do not by themselves separate the daemon and runtime from the host,
and Dev Container metadata is an environment format, not a policy: it can carry
build steps, lifecycle commands, arbitrary mounts, capabilities and privileged
mode.

Torio therefore needs one place where the boundary is drawn, and that place has
to be provable rather than declared. A VM created from whatever image happened to
resolve at the time, then adjusted by hand, is not a boundary anyone can reason
about a month later.

## Decision

**The Linux Lima VM is the boundary. What is inside it is created from a
pinned image and verified, never trusted.**

1. **What lives where.** The Hermes runtime, the repositories and the Brain live
   in the guest. The host keeps the Desktop, the IDE and the operator's terminal.

2. **No broad host mount.** Repositories and guest state are on the guest's own
   ext4, not on a 9p/virtiofs share. The absence of any host-share mount is
   a verified postcondition, not a convention.

3. **The image is pinned by digest.** `vm init` builds from a promoted image URL
   and its SHA-256, both carried by the host's profile in `internal/lima`. The
   digest is part of what verification proves, so an instance built from a
   different image is drift.

4. **The pins are host-derived, and there is exactly one table.** The hypervisor
   driver, the guest architecture and the image are properties of the host Torio
   runs on: `vz` and `aarch64` on macOS/Apple Silicon, `qemu` and `x86_64` on
   Linux/x86_64. `internal/lima.Profile` holds both rows, and the same struct
   both renders the template and verifies the created instance.

   This does not weaken the pins. They were never a claim about isolation
   strength -- both drivers are hardware virtualization, and the threat model in
   `SECURITY.md` does not admit an adversarial agent. They answer one question:
   *is this instance the one Torio would have created here?* That question is
   only meaningful against a single expected answer, and it stays exactly as
   sharp when the answer depends on the host asking it.

   What would weaken them is two places holding the same literal. A renderer and
   a verifier with independent opinions about the same instance drift silently,
   and the drift looks like a passing check.

   The supported matrix is `darwin/arm64` and `linux/amd64`. Intel Macs are out:
   `vz` requires Apple Silicon. arm64 Linux is out because nothing here has ever
   booted it -- a row in that table reads as a guarantee, and an unproven row is
   a claim.

5. **Verification proves every postcondition and fails closed.** The guest
   architecture is the host profile's; the `hermes` user exists; the
   `torio-projects` group exists with `hermes` and the operator in it; `hermes` is **not** in the `docker` group,
   which is root-equivalent; each required path is a directory with the expected
   owner, group and mode on native ext4; `hermes --version` works through the
   documented command path. A clean exit code is not accepted as evidence of any
   of these.

6. **Drift is reported, not repaired.** A mismatch in architecture, image,
   mounts or ownership exits non-zero with a remediation message. Torio never
   re-images, resets or deletes an instance.

7. **The backend binds the guest loopback.** `hermes-serve.service` is a user
   systemd unit, validated with `systemd-analyze verify` before it is ever
   activated, and proven ready by an actual `200` from the endpoint. Reaching it
   from the host is an `ssh -L` forward the operator opens; network exposure is
   never a side effect of running a command.

## Consequences

- Compromise of an ordinary workload stays inside the VM, subject to VM escape.
- A laptop that sleeps is not a 24/7 host, and Torio does not pretend otherwise.
- Two supported hosts mean two guest images to promote together. A digest bump
  that moved only one would leave the platforms running different releases while
  every check still passed, so the profile table pins both to one Ubuntu build
  and a test asserts it.
- Supporting Linux is what makes the guest half of the platform journey
  gateable: hosted macOS runners are themselves VMs and cannot nest, so no
  automated job had ever booted a Torio guest. That is a verification
  consequence, not only a cost one.
- Because drift is never silently repaired, an instance that was hand-modified
  stays broken until the operator decides what to do. That is the intended cost.
- The pinned image ages. Promoting it is a deliberate change to a constant with
  its own review, not an automatic upgrade.

## Rejected

- **Hermes directly on macOS.** The blast radius this ADR exists to bound.
- **Mounting the host home or workspace into the guest.** Reintroduces the host
  filesystem as reachable state.
- **Keeping `vz`/`aarch64` as literals and adding a second code path.** Two
  paths through the same check are two chances for the renderer and the verifier
  to disagree.
- **Listing arm64 Linux as supported because it probably works.** This table is
  read as a guarantee. Adding the row costs one digest; earning it costs a boot.
- **A Dev Container as the only isolation boundary.** It does not separate the
  daemon from the host, and its metadata can request privileges Torio would then
  be granting by accident.
- **A tag instead of a digest.** A tag is a moving target and cannot be evidence.
- **Repairing drift automatically.** A control plane that quietly fixes what it
  finds cannot be used to prove what state the machine was in.
