<!--
AI-Provenance:
  model: Claude Opus 5 (1M context)
  harness: Claude Code
-->

# Handoff — MCP broker, credential custody, egress

Session notes, not canonical. Everything normative lives in `docs/adr/` and
`docs/contracts/`. This file exists so the next session does not re-derive what
this one established, and does not re-discover the open items the hard way.

Date: 2026-07-29. Branch: `feat/mcp-broker-daemon` (PR #78). PR #77 merged.

---

## 1. One decision is blocking, and it is not mine to take

`AGENTS.md` §4 lists, under "Torio NIE MOŻE implementować":

> secret managera klasy Vault ani **domenowego network allowlistu**

ADR-0024 proposes exactly a domain/destination allowlist. `AGENTS.md:3` says a
contradiction between it and anything else means **stop work and report the
conflict** — it outranks every ADR. ADR-0024 cites `docs/03-architecture.md`'s
matching "out of scope" entry and reverses it, but never mentions §4.

**Nothing further should be built on ADR-0024 until this is resolved.** Either
§4 is amended (the operator's call) or ADR-0024 is withdrawn. ADR-0023 declares
itself incomplete without ADR-0024, so this blocks the inference-credential work
too.

A second, smaller governance item: **ADR-0022 is still `Status: Proposed`**, yet
`AGENTS.md` §5 invariant 8/8a and the normative `docs/contracts/cli.md` were
rewritten on its authority. `AGENTS.md:63` ranks only *accepted* ADRs as a
source of truth. Accept it or mark the dependents provisional.

And a third: **the write window is a new architectural decision with no ADR.**
It contradicts ADR-0022's "wstrzyknięta instrukcja może użyć każdego przyznanego
narzędzia". `cli.md` was narrowed; ADR-0022 was not. Needs ADR-0025 superseding
that passage.

---

## 2. What shipped, and what state the guest is actually in

Merged (PR #77): ADR-0022, guest-side boundary verification, `torio mcp status`,
`torio mcp install`, live-run evidence, the relay, the policy engine + audit.

On the branch (PR #78): the daemon, write windows, the dead-socket drift check,
ADR-0023, ADR-0024.

**The daemon cannot start on the live guest.** Four independent reasons, all
verified:

1. `torio-mcp` is not in `torio-mcp-clients`, so it cannot `chgrp` its own
   socket — EPERM, exit 7. One `usermod -aG` in `InstallMCPBroker` fixes it.
2. Nothing provisions `/run/torio-mcp` — exit 3. Wants `RuntimeDirectory=` in a
   unit.
3. **No systemd unit exists anywhere in the repo**, and nothing installs either
   guest binary. `Makefile` `package-release` builds only `./cmd/torio`.
   ADR-0022 §6 requires the unit be validated with `systemd-analyze verify`
   before activation; there is no unit to validate.
4. `pendingUpstream.roundTrip` always errors — the real HTTP transport is the
   deliberate next slice.

Three error strings currently send an operator to `torio mcp install` *on the
guest*; `torio` is a host binary. Fix the wording with the unit slice.

The live instance still has **3 Atlassian credential files under
`$HERMES_HOME/mcp-tokens/`** from the pre-broker `hermes mcp add`, so
`torio mcp status` exits 6 until that is migrated and the tokens revoked
upstream. That is the documented state, not a defect.

---

## 3. Defects found by audit, still open

Two read-only audits ran over the whole body of work. The critical one — the
write window was never enforced — is fixed in `eade5f5`. These are not:

**Fail-open checks (highest priority; they are security controls that report OK
when they cannot tell):**

- `internal/lima/mcpsocket.go:63` parses the socket directory's **group**,
  prints it in the failure message, and never compares it. `torio-mcp:torio-mcp
  0750` passes every check while the agent has no `+x` and can reach no socket.
  `status` then prints "Broker boundary holds" on a guest where MCP cannot work.
  Compounding it, `cmd/torio-mcp-connect` maps the resulting EACCES to a
  confident, wrong diagnosis about group membership.
- `internal/lima/mcpbroker.go:255` and `internal/lima/mcpsocket.go:43` treat
  **any** non-zero exit from `sudo -n stat` as "absent, all good". That covers
  sudo requiring a password, sudo missing, stat missing. One sudoers change and
  both drift checks go green on a guest with live tokens and a dead broker. The
  comment's premise ("as root") is exactly what the probe has not established.
  Contrast `internal/lima/status.go:117`, which refuses to treat an unusable
  probe as ground truth.

**Missing verification ADR-0022 §6 requires:**

- No check that `mcp_servers` in `$HERMES_HOME/config.yaml` points only at the
  relay. **This is the one entry the agent can rewrite to bypass the broker
  entirely** — `config.yaml` is agent-writable, which is the ADR's own premise.
- No check on policy document ownership or mode, though ADR §4 and `cli.md`
  both assert `root:root 0644`. An agent-writable policy document voids the
  decision with every existing check green.
- Nothing renders the granted scope, so nothing verifies it matches the files.
  `Grant`/`ServiceGrant`/`ToolGrant`/`WriteTools` are exported for exactly this
  and have no consumer. `AGENTS.md` §5.8a's "jawny, wyliczalny i weryfikowany"
  is not delivered.
- Nothing checks `torio-mcp` stayed out of `torio-projects`. The installer is
  asserted not to do it; `status` never looks. The symmetric check for `hermes`
  exists.

**Correctness:**

- **Duplicate JSON keys.** `toolName` uses `json.Unmarshal` (last key wins);
  `forward` sends the original bytes verbatim and `json.Compact` preserves both
  keys. `{"name":"deleteJiraIssue","name":"getJiraIssue"}` is audited as
  `getJiraIssue` and a first-wins upstream executes `deleteJiraIssue`. Latent
  only because no transport carries traffic yet; it goes live with the HTTP
  slice. The fix must be duplicate-key rejection at parse time, not a re-encode.
- `filterToolsList` returns early on any `error` member, so a reply carrying
  both `error` and `result` is passed through **unfiltered** and without even
  being confirmed as JSON-RPC 2.0. Every other branch there fails closed.
- The parse-error path echoes a caller-authored `id`: Go populates decoded
  fields before a type error, so `isScalarID` runs too late. Output goes back to
  the same client only, but the invariant the comment asserts does not hold.
- `boundToolName` duplicates `maxAuditFieldLen` and drops the rune-boundary
  guard, so the two truncations of the same value differ. Single-source it.
- No cap on concurrent connections; anything running as `hermes` can exhaust
  the broker's descriptors.

**Contract drift:**

- `cli.md:75` justifies the unused exit code 4 with "V1 nie ma silnika policy" —
  V1 now ships one whose whole job is denial. The conclusion (4 stays unused) is
  still right; the reason is false. Duplicated in `internal/cli/exit.go:26`.
- `cli.md:330` says every state-changing command is idempotent. `mcp
  allow-write` is not — it moves the deadline every run. Neither `mcp install`
  nor `mcp allow-write` is in the list below it.
- Exit code 1 is used by three binaries and defined in no table; the two guest
  binaries claim to follow `cli.md` and do not appear in it.
- Error envelopes carry the parent command (`"mcp"`) rather than the full name
  (`"mcp.allow-write"`), which `cli.md:52` forbids. Pre-existing and broad —
  `project add`, `brain import`, `vm ssh` all do it; `project shell` is the one
  that does it right.
- A mistyped service name in `allow-write` exits 6 (drift) instead of 2 (usage).
  `--for` is classified correctly, so one command classifies its two arguments
  inconsistently.
- `docs/03-architecture.md` is untouched by any of this work, though
  `docs/adr/README.md` requires security-boundary changes to update it. The
  trust boundary now has a third guest identity, a policy directory and a socket.
- `docs/v1-evidence/FINDINGS-mcp-broker-boundary.md` says "ADR-0022 stands as
  written" (it was amended after) and "the broker daemon does not exist yet"
  (it does; nothing deploys it). Evidence must not be silently falsified by
  later work — correct it or add a dated successor.
- ADR-0024's Context says `hermes` has "no primitive to read, change or bypass"
  the ruleset; its own Precondition section describes exactly such a primitive.
  The Precondition is right.
- ADR-0023 rejects `hermes proxy` partly for authenticating nobody over loopback
  TCP — which its own chosen design also does. The valid distinguishing reason
  (no `openai-codex` adapter) stands alone; the authentication clause should go.
- `AGENTS.md:109`'s `8a.` is not a valid ordered-list marker; items 9–12 fold
  into item 8 on GitHub.

---

## 4. The shim: a live one-step path from the agent to root

Independent of everything above, and cheap to fix:

```
lrwxrwxrwx root   root   /usr/local/bin/hermes -> /home/hermes/hermes-agent/venv/bin/hermes
-rwxrwxr-x hermes hermes /home/hermes/hermes-agent/venv/bin/hermes
Defaults secure_path="/usr/local/sbin:/usr/local/bin:..."
```

Torio never runs the shim as root. But `/usr/local/bin` is first on sudo's
`secure_path` and the operator has passwordless sudo, so **one `sudo hermes …`
executes agent-authored code as root** — and flushes any ruleset in one command.
A kernel-enforced boundary an operator can be talked into removing is not a
boundary.

Fix: make the shim root-owned (a copy, or a wrapper that `exec`s under `setpriv
--reuid hermes`), and have `bootstrap` prove the shim's ultimate target is not
writable by `hermes`. Recorded as a precondition in ADR-0024.

---

## 5. Threat model — decided this session, not yet written into the docs

These decisions are scoped to **prompt injection and a confused agent**:
realistic, frequent, the daily risk. They are **not** scoped to a deliberately
adversarial agent.

The reason is structural. The agent's job is to read Jira, Confluence and code
and produce text a human reads; every channel that lets it work is a channel
data leaves through. The operator's terminal is an egress channel. "The box
prevents exfiltration" was never achievable and must not be claimed. What is
achievable: the agent cannot acquire durable capability, every capability it has
is enumerable and verified, and actions leave a trail.

Consequences already taken: no DNS filter, no SNI proxy. Both cost real
usability and pay off only against the threat we are not claiming to stop. DNS
remains an uncontrolled covert channel (every query leaves as uid 991,
`systemd-resolved`, never as the agent) and that is accepted, not overlooked.

**This belongs in `docs/03-architecture.md` and is not there yet.**

---

## 6. Facts worth not re-deriving

Verified in Hermes source or on the live guest this session:

- `agent/file_safety.py` states its own read denylist "is **NOT** a security
  boundary" — the terminal tool runs as the same uid and can `cat auth.json`.
- `config.yaml` is not on the write denylist and `HERMES_WRITE_SAFE_ROOT` is
  unset, so `mcp_servers.<n>.tools.include` — Hermes' only least-privilege
  mechanism for MCP — is agent-writable. A default, not a control.
- **Hermes hooks are the same trap**: declared in `config.yaml`, consent
  allowlist at `~/.hermes/shell-hooks-allowlist.json`, both agent-writable. Do
  not build a control there.
- **Codex refresh tokens rotate and are single-use** (`refresh_token_reused` is
  a first-class error) and the refresh fires reactively on a 401 mid-turn and
  writes back unconditionally. Read-only custody is impossible for that
  credential; there can be exactly one holder; migration is a move, not a copy.
- For a custom provider on a **loopback** base URL, Hermes resolves the key to
  the literal `"no-key-required"` and excludes loopback hosts from vendor-key
  derivation. Hermes can run holding no inference credential, by configuration.
- `base_url` goes to the OpenAI SDK as http(s). There is **no unix-socket
  surface for inference**, so ADR-0022's kernel-established peer identity does
  not transfer.
- `meta skuid` works on this kernel (6.8.0-134); `nft --check` is a kernel
  round-trip, not a parse. `hermes` has no sudo, no capability, no usable
  userns (AppArmor), and owns no file on a privileged execution path.
- **There is no host-side enforcement point.** `192.168.5.0/24` is on no macOS
  interface; the guest's TCP/IP terminates inside `limactl hostagent` running as
  the operator's uid, so a pf `user 501` rule would match all of the operator's
  own traffic.
- A validated git remote always yields a host token, but it may be an **SSH
  config alias**, not a DNS name. A mechanically derived allowlist needs an
  explicit answer for that.
- The inference endpoint is written down **nowhere machine-readable** —
  `model.base_url` is empty; the host is only discoverable by grepping Hermes.

Unverified, and recorded as such: `meta skuid` behaviour on orphaned sockets on
this host; whether pf is enabled on the Mac; whether Lima 2.2.0 exposes any
usernet egress knob; PyPI/npm/uv-python hostnames.

---

## 7. Suggested order for tomorrow

1. **Resolve the `AGENTS.md` §4 conflict.** Blocks ADR-0024 and therefore
   ADR-0023. Operator decision.
2. **ADR-0025 for the write window**, superseding the relevant ADR-0022 passage.
   Accept ADR-0022 while there.
3. **The three fail-open checks** (§3) — small, tested, and they are the
   difference between a control and a claim.
4. **`mcp_servers` verification** — the one bypass an agent can perform alone.
5. **The unit slice**: `usermod -aG`, `/run/torio-mcp`, a systemd unit validated
   before activation, packaging for the guest binaries. This is what makes the
   daemon run at all.
6. The shim fix (§4) — independent, cheap, and it gates any later egress work.
7. Contract drift (§3, last block) as one sweep.

The upstream HTTP transport, OAuth, and the Atlassian migration come after the
unit slice, because none of them is observable until the daemon starts.
