# Torio

[![ci](https://github.com/wzslr321/torio/actions/workflows/ci.yml/badge.svg)](https://github.com/wzslr321/torio/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/wzslr321/torio)](https://github.com/wzslr321/torio/releases)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Your AI second brain, on a Linux VM you actually control.

Torio is a thin control plane over [Lima](https://lima-vm.io): one Go binary
that creates a VM on your macOS or Linux workstation, runs the Hermes backend
as a service on the guest's own loopback, keeps a private Markdown Second
Brain, and attaches the repositories you name. It has no daemon, no state
beyond one non-secret config file, and no credentials at all.

The narrowness is the point. Torio is not the AI, not the VM, and not the chat
window. It is the layer that brings those three into a known-good state and
then gets out of the way. Everything that writes stays in your hands: the
commit, the push, the tunnel, the credential.

Documentation lives at **[torio.dev](https://torio.dev)**.

## How it fits together

```mermaid
flowchart LR
    subgraph host["Your machine"]
        desktop["Hermes Desktop"]
        cli["torio CLI"]
        agent["Your SSH agent"]
    end

    subgraph vm["The Lima VM: the trust boundary"]
        backend["hermes serve on 127.0.0.1:9119"]
        brain["Second Brain"]
        work["Checkouts"]
    end

    origin[("Git origin")]

    desktop -->|"the ssh -L tunnel you open"| backend
    cli ==>|"create, verify, reconcile"| vm
    agent -.->|"forwarded only in torio project shell"| work
    backend --> brain
    backend -->|"read only"| work
    origin -->|"clone and fetch"| work
    work -.->|"commit and push, by you"| origin
```

The dashed edges are the whole design. The persistent backend has read access
to your checkouts and nothing more: it cannot push, and no credential of yours
is stored anywhere it could reach. Write capability against a remote exists
only inside a `torio project shell` session, which forwards your own SSH
agent, lasts exactly as long as you keep it open, and takes the capability
with it when you exit.

## Quick start

You need macOS on Apple Silicon or Linux on x86_64, with
[`limactl`](https://lima-vm.io) on your `PATH`. Torio refuses anything else
rather than creating a VM it could never verify.

Install a release. The installer resolves the latest stable version, verifies
`SHA256SUMS` before the binary is copied anywhere, and authenticates to
nothing:

```bash
scripts/install.sh                     # into ~/.local/bin
```

or build from source: `go build -o torio ./cmd/torio`.

Then bring the stack up. Every step is idempotent: rerunning a finished setup
changes nothing and exits `0`.

```bash
torio vm init                          # pinned Lima template; no --force exists
torio vm start
torio vm bootstrap --timeout 10m       # install + verify the guest; drift fails closed

torio serve install --timeout 2m       # render + validate the systemd unit
torio serve start   --timeout 2m       # up when /api/status answers 200, not before

torio brain init                       # private Markdown vault, versioned, searchable
torio serve restart --timeout 2m       # pick up the Brain's retrieval skill

torio project add my-service https://github.com/you/my-service --use
```

For a private repository, grant the guest read access yourself before
`project add`. Torio never stores or prompts for a credential, so a remote the
guest cannot already read fails closed.

The backend binds `127.0.0.1:9119` inside the VM. Torio adds no tunnel
feature, so network exposure is never a side effect of running a command. You
open the forward yourself:

```bash
ssh -F ~/.lima/torio/ssh.config -L 19119:127.0.0.1:9119 -N -f \
    -o ExitOnForwardFailure=yes lima-torio
```

Point Hermes Desktop at `http://127.0.0.1:19119`, set the session token, pick
a provider, and work. The token and provider steps, and the full verification
story behind every command above, are in
[`docs/runbooks/first-run.md`](docs/runbooks/first-run.md).

## The daily loop

`torio serve status` is the one command to remember: it reports the systemd
unit, the loopback endpoint and the Hermes version, so a backend that stopped
answering names itself instead of leaving you to guess.

Once you run more than one box, `torio status` is the other one. It polls every
box Torio owns and gives you a row each: whether it is running, what its agent
has going, whether anything there is waiting on you, and when it last did work.
Fields it cannot prove say so — `?` for a question it could not answer, `—` for
one that backend does not answer at all — because a status line that guesses is
one you stop reading.

```console
$ torio status
INSTANCE            BOX      BACKEND      SESSION  WAITING          PROGRESS
torio               running  hermes       —        ?                24s
torio-claude-code   running  claude-code  1        notification 3m  —
```

It exits 0 whatever it finds, so something can call it on a timer and put the
answer where you already look. `torio status setup tmux` prints the block that
does that; the same report collapses to one chip per box, and only the box that
wants you is loud. Details and the other surfaces are in
[`docs/runbooks/ambient-status.md`](docs/runbooks/ambient-status.md).

Work happens in the checkouts: from Desktop, from your own editor, or from
`torio project enter <id>`. Edit, run checks that read rather than write,
review `git diff`. When you decide something should leave the VM:

```bash
torio project shell my-service         # your SSH agent, forwarded for this session
git commit && git push
exit                                   # the capability leaves with you
```

Every command takes `--json` and emits a single machine-readable document on
stdout; flags, exit codes and each command's contract are in the
[reference](https://torio.dev/reference.html).

## What Torio will not do

- **Hold your Git or model-provider credentials.** Git write arrives only as
  your forwarded SSH agent in a session you opened. MCP OAuth is the deliberate
  exception: an interactive login stores it under the separate `torio-mcp`
  guest identity, never under the agent uid or on the host.
- **Expose the backend.** It remains loopback-only inside the VM; its working
  tunnel is yours to open and close. MCP login opens only its fixed loopback
  OAuth callback forward and closes it with the command.
- **Write history.** No commit, push, merge, tag, or release. The persistent
  backend reads only.
- **Take data out.** Import brings a vault in; there is no export. Copying the
  Brain back out is a `limactl copy` you run yourself.
- **Delete anything.** It never re-images or removes a VM, and removing a
  project leaves its checkout on disk.
- **Run agents for you.** No task queue, no dispatcher, no autonomous workers.
- **Grant MCP tools implicitly.** `torio mcp install` ships the broker and relay,
  but policy is a root-owned, exact list written by the operator. OAuth begins
  only with `torio mcp login <service>`, and the broker refuses tools outside
  the verified grant.
- **Stop an agent leaking what it has read.** The trust boundary is the edge
  of the VM; the threat model covers prompt injection and a confused agent,
  not an adversarial one. Data exfiltration is unsolved and DNS is an accepted
  covert channel. [`SECURITY.md`](SECURITY.md) says exactly what is claimed.

## Which agent runs inside

One VM runs one agent identity, and a second backend means a second VM — never
a second agent inside one, because two identities sharing a workspace would
contend over the same checkouts and make every custody statement ambiguous.

You do not track which VM that is. `--backend` names the agent and Torio finds
its box: the default one is `torio`, the rest are `torio-<backend>`.

| | `hermes` (default) | `claude-code` |
| --- | --- | --- |
| Shape | a guest service on loopback | a per-session process |
| Reached by | a client through a tunnel you open | `torio project agent <id>` |
| Project registry | yes, driven by Torio | none — a project is a directory |
| Pinned by | an upstream commit | a version, checksum-verified |

```bash
torio vm init --backend claude-code
torio vm start --backend claude-code
torio vm bootstrap --backend claude-code --timeout 10m
torio backend login --backend claude-code       # the box gets its own grant
torio project add demo --backend claude-code    # already attached elsewhere? clone it here
torio project agent demo --backend claude-code  # the agent works here
torio project shell demo --backend claude-code  # you review, and push
```

The project registry is shared, so `demo` is attached once and `project list`
says the same thing whichever backend you select. The checkouts are not shared
and cannot be — each is owned by one guest identity — so `project add <id>`
with no remote is the step that gives a backend its own working tree, from the
remote already on record. What passes between the two trees is what you push.

`TORIO_INSTANCE` still names a box directly, for a test VM or a second box
running the same backend.

Inside the box the agent runs without permission prompts, and that is the
inversion worth understanding. A prompt is a control inside the agent's own
process — it can be ignored, and in practice it is clicked through. The box
replaces it with controls the agent cannot reach: an unprivileged identity with
no `sudo`, a closed group set, a binary it cannot rewrite, no credential that
reaches a Git remote, and the edge of a VM. The agent commits; you push, after
reading what it did.

MCP uses the same custody boundary on both backends. Write one strict policy
document per service as `root:root 0644`, then install, authorize, and verify:

```bash
torio mcp install --backend claude-code
torio mcp login atlassian --backend claude-code
torio mcp status --backend claude-code
```

The backend launches a credential-free stdio relay. OAuth state belongs to the
separate `torio-mcp` uid, and the broker exposes only tools present in both the
root-owned grant and upstream discovery. Claude Code's route is root-managed;
Hermes' agent-owned config remains a drift detector, while the socket and policy
are the enforcement boundary. See
[ADR-0013](docs/adr/0013-mcp-managed-client-config-and-activation.md).

## The brain, without the VM

The vault has a written standard of its own, and it ships as a plugin you can
install into Claude Code with no VM under it at all:

```
/plugin marketplace add wzslr321/torio
/plugin install brain-kit@torio
/brain-kit:init
```

That gives you the vault, its format, and the rituals that keep it worth having
— capture, inbox triage, daily notes, meetings, people, retrieval. It works
against a directory of notes you already have: a note without frontmatter stays
valid to read, so nothing is rewritten on arrival.

How much of that is real rather than well written is measured, not claimed:
[`brainkit/evals/`](brainkit/evals/README.md) hands an agent a fixture vault and
checks what it actually did — including whether it leaves the vault alone when
the task has nothing to do with it.

What it does not give you is a boundary. Those are instructions to a model
running on your workstation with your permissions, which is the gap the VM
closes and the reason the rest of this README exists. Same standard, same vault
shape, either way — [`brainkit/README.md`](brainkit/README.md) and
[`brainkit/STANDARD.md`](brainkit/STANDARD.md).

## Supported hosts

| Host | VM type | Guest |
| --- | --- | --- |
| macOS / Apple Silicon | `vz` | `aarch64` |
| Linux / x86_64 | `qemu` | `x86_64` |

Both pin the same Ubuntu build by digest, so the two hosts do not run
measurably different guests. An unsupported host is refused once, up front,
not deep inside the first command that needs a pin. This table is read as a
guarantee, so a row lands only after something has actually booted it.

## Roadmap

Torio does less than it will. Where it is going, roughly in order:

- **More hosts.** arm64 Linux is one table row plus an image digest away; it
  waits on someone booting and verifying it, not on design.
- **Editor integration.** A Neovim panel already ships in
  [`integrations/neovim`](integrations/neovim/README.md): `:Torio` lists
  projects, opens routine or push-capable terminals, reports health, and shows
  Hermes sessions. It is not packaged for a plugin manager yet, and no other
  editor has an equivalent.

If one of these is yours, [`CONTRIBUTING.md`](CONTRIBUTING.md) has the how and
[`AGENTS.md`](AGENTS.md) has the boundaries no change may cross.

## Documentation

- **[torio.dev](https://torio.dev)**: tutorials, how-to guides, the full
  command reference, and the reasoning, organised by
  [Diátaxis](https://diataxis.fr).
- [`docs/runbooks/first-run.md`](docs/runbooks/first-run.md): the complete
  first run, every command in order.
- [`brainkit/STANDARD.md`](brainkit/STANDARD.md): what a Torio vault is — the
  note types, the naming, the links, and what an agent may do to it.
- [`brainkit/evals/`](brainkit/evals/README.md): the behavioural benchmark that
  measures whether an agent holding the kit actually behaves that way, and the
  reports it has produced.
- [`docs/adr/`](docs/adr/README.md): the accepted decisions. Why the VM is the
  trust boundary, why write capability is operator-carried, why the egress
  allowlist was rejected.
- [`SECURITY.md`](SECURITY.md), [`CONTRIBUTING.md`](CONTRIBUTING.md),
  [`CHANGELOG.md`](CHANGELOG.md).

The site and the runbooks are generated from [`docs/content/`](docs/content/)
by [`scripts/build_docs.py`](scripts/build_docs.py); generated files are
committed, and the site deploys to torio.dev straight from `site/` with no
build step. Edit sources, never outputs: `make docs && make validate`. This
README is not generated; edit it directly.

MIT. See [LICENSE](LICENSE).
