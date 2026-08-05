# Torio

Documentation source: [`site/`](site/). The public deployment and domain are
not configured yet.

> Torio is a thin, trusted control plane for running an AI second brain and your
> coding projects on a Linux VM on Apple Silicon Macs.

Torio is not the AI, not the VM, and not the chat window. It is the layer that
brings those three into a known-good state and then gets out of the way.

## What Torio does

- **Creates and reconciles the VM.** A Lima instance built from a pinned
  template, verified rather than trusted: architecture, image digest, ownership,
  modes, group membership, and the absence of any macOS host mount. Drift fails
  closed with remediation instead of being quietly repaired.
- **Runs the Hermes backend as a service.** A user systemd unit bound to the
  guest's own loopback, validated before it is ever activated, and proven ready
  by an actual `200` from the endpoint — not by a clean exit code.
- **Keeps a private Second Brain.** A Markdown vault on the guest, versioned by
  its own Git repository and registered with Hermes so any session can search
  it. An existing vault can be imported through verified staging.
- **Attaches the repositories you name.** Each one clones into a path derived
  from its id, gets shared access for you and the service identity, and is
  registered with Hermes. The model sees the projects you registered and no
  others.
- **Prepares a separate MCP credential boundary.** `torio mcp install`
  provisions a dedicated guest identity, private credential home, client group,
  and root-owned policy directory. `torio mcp status` verifies that boundary
  without repairing it.

## What Torio does not do

- **It holds no credentials.** It never stores, prompts for, or reads one, and
  never causes a credential prompt. A remote the guest cannot already read
  without prompting fails closed; granting that access is yours to do, on the
  guest, outside Torio.
- **It opens no tunnel.** The backend is loopback-only inside the VM. You open
  the SSH forward yourself, so network exposure is never a side effect of
  running a command.
- **It writes no history.** No commit, push, merge, tag, or release. The
  persistent backend has read access only. When you decide to push,
  `torio project shell` forwards your own SSH agent into a session that ends
  when you exit it.
- **It takes no data out.** Import brings a vault in; there is no export.
  Copying the Brain back to your Mac is a `limactl copy` you run yourself, which
  Torio neither performs nor verifies nor calls a backup.
- **It deletes nothing.** It never re-images or removes a VM, and forgetting a
  project leaves its checkout on disk.
- **It is not an agent platform.** No task queue, no dispatcher, no autonomous
  workers.
- **It does not broker MCP traffic yet.** The released CLI provisions custody
  only. It does not install or activate the dormant broker, run OAuth, or send
  requests to an upstream MCP service.
- **It does not stop an agent leaking what it has read.** The trust boundary is
  the edge of the VM, and the threat model covers prompt injection and a confused
  agent — not an adversarial one. See
  [`docs/03-architecture.md`](docs/03-architecture.md) and
  [`SECURITY.md`](SECURITY.md).

## Prerequisites

- A macOS host on Apple Silicon with `limactl` on your `PATH`.
- A Go toolchain, to build the CLI.
- For each repository you attach: read access that already works from the guest,
  without a prompt.

## Getting started

From a repository checkout, install a published release asset with a client
that already has access to the repository:

```bash
gh release download vX.Y.Z -D /tmp/torio-rel
scripts/install.sh --version X.Y.Z --base-url file:///tmp/torio-rel
```

The installer verifies `SHA256SUMS` before replacing the binary. Torio never
receives the GitHub credential used by `gh`.

To build from source instead, put the resulting binary on your `PATH` so every
documented command works as written:

```bash
go build -o torio ./cmd/torio
sudo install -m 755 torio /usr/local/bin/torio
```

Then follow the runbook, which has the exact commands in order:
[`docs/runbooks/first-run.md`](docs/runbooks/first-run.md).

Optionally, validate the documentation pack for internal consistency:

```bash
python3 scripts/validate_artifacts.py
```

## Documentation

The operational surface is this file plus
[`docs/runbooks/first-run.md`](docs/runbooks/first-run.md). Normative
engineering rules for contributors and agents live in [`AGENTS.md`](AGENTS.md).
Release-level changes are summarized in [`CHANGELOG.md`](CHANGELOG.md), with
detailed notes for the current candidate in
[`docs/releases/v0.2.0.md`](docs/releases/v0.2.0.md).

A static, Diátaxis-organised documentation site lives in [`site/`](site/) —
plain HTML and one CSS file, no runtime dependency. The complete first run is on
one page, `Get started`, in [`site/tutorials.html`](site/tutorials.html). It is
prepared for deployment on **Vercel**, but **deployment is pending**: no Vercel
project and no `torio.dev` domain are connected or configured by this repository
yet. The high-level human setup sequence is in
[`site/DEPLOYMENT.md`](site/DEPLOYMENT.md).

### One source, two outputs

The site pages **and** the runbook are generated by
[`scripts/build_docs.py`](scripts/build_docs.py) from Markdown sources in
[`docs/content/`](docs/content/). A section used in more than one place — pinning
the session token, say — is a single file in `docs/content/blocks/` included by
each, so the site and the runbook cannot disagree.

```bash
make docs         # regenerate site/*.html and docs/runbooks/*.md
make validate     # fail if any generated file drifted from its source
```

Generated files are committed, so serving the site still needs no build step.
Edit the sources, never the outputs — see [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Earlier exploration

A broader design exploration (worker/control-plane, registry, verifier, evidence
pipeline) came first, was never delivered, and is **superseded**. It no longer
ships in the working tree; it is kept under the annotated tag `archive/pre-v1`
and read with `git show archive/pre-v1:<path>`. See
[`docs/adr/0005-repository-and-documentation-governance.md`](docs/adr/0005-repository-and-documentation-governance.md).
Do not use it as an onboarding or task path.
