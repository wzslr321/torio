<!--
AI-Provenance:
  model: Claude Opus 5 (1M context)
  harness: Claude Code
-->

# Handoff — MCP broker, credential custody, egress

Session notes, not canonical. Everything normative lives in `docs/adr/` and
`docs/contracts/`. This file exists so the next session does not re-derive what
earlier ones established, and does not re-discover the open items the hard way.

Updated 2026-07-29 (second session). Branch: `feat/mcp-broker-daemon` (PR #78).
PR #77 merged.

---

## 1. Governance: settled

**The `AGENTS.md` §4 conflict is resolved.** ADR-0024 proposed a domain
allowlist, which §4 forbids. The operator resolved it as a **split**, not an
amendment:

- ADR-0024 keeps only the uid-keyed part. An allowlist of identities is not an
  allowlist of domains, so §4 does not reach it — and it is the whole of what
  ADR-0023 needs to close its loopback broker (ADR-0024 §5).
- The destination allowlist is now **ADR-0026**, `Proposed` and explicitly
  **blocked** until the operator decides on §4. Nothing may be built on it.

The narrowing costs real coverage: `hermes` keeps an unrestricted set of
destinations, so ADR-0024 does not close data exfiltration. That is stated in
its Context and in a "what this buys and what it does not" section, not in a
footnote.

**ADR-0022 is Accepted.** `AGENTS.md` §5 invariant 8/8a and `cli.md` were
rewritten on its authority, and §3 ranks only accepted ADRs as a source of truth.

**ADR-0025 covers the write window**, superseding ADR-0022's "wstrzyknięta
instrukcja może użyć każdego przyznanego narzędzia". ADR-0022 carries the
pointer at the superseded passage. `cli.md` now records that `mcp allow-write`
is the one non-idempotent mutating command, with the reason.

---

## 2. What state the guest is actually in

Merged (PR #77): ADR-0022, guest-side boundary verification, `torio mcp status`,
`torio mcp install`, live-run evidence, the relay, the policy engine + audit.

On the branch (PR #78): the daemon, write windows, the dead-socket drift check,
ADR-0023, ADR-0024, ADR-0025, ADR-0026, the verification work below.

**The daemon still cannot start on the live guest.** Four independent reasons,
all verified and none addressed yet:

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

Note for the unit: `verifyBrokerSockets` now requires `/run/torio-mcp` to be
`torio-mcp:torio-mcp-clients`, so the unit needs `RuntimeDirectoryMode=0750`
and the right group, not systemd's default.

Three error strings still send an operator to `torio mcp install` *on the
guest*; `torio` is a host binary. Fix the wording with the unit slice.

The live instance still has **3 Atlassian credential files under
`$HERMES_HOME/mcp-tokens/`** from the pre-broker `hermes mcp add`, so
`torio mcp status` exits 6 until that is migrated and the tokens revoked
upstream. That is the documented state, not a defect.

---

## 3. Defects: what is closed

Closed this session (`30d03c9`, `424841f`), each with a failing test first:

- The socket directory's **group is now compared**, not just printed.
  `torio-mcp:torio-mcp 0750` used to pass owner and mode while the agent could
  not traverse to the socket at all.
- Both `sudo -n stat` **fail-open probes are gone**. Every privileged stat now
  names a control path that must exist, so one round trip separates "the probe
  never ran" from "the path is absent" — no line, one line, two lines. The
  unprovable case is drift, not "not installed".
- `torio-mcp-connect` no longer asserts one cause for EACCES; it names both the
  client group and the directory above the socket.
- **Policy documents are verified**: directory `root:root` and unwritable by
  anyone else, every entry a regular file (never a symlink) with the `.json`
  suffix the loader requires, each `root:root 0644`.
- **`mcp_servers` is verified** against the relay path, with a reader that
  models one shape of block YAML and reports everything else as drift rather
  than guessing.

---

## 4. Defects: still open

**Correctness in the broker (all in `cmd/torio-mcp-broker`):**

- **Duplicate JSON keys.** `toolName` uses `json.Unmarshal` (last key wins);
  `forward` sends the original bytes verbatim and `json.Compact` preserves both
  keys. `{"name":"deleteJiraIssue","name":"getJiraIssue"}` is audited as
  `getJiraIssue` and a first-wins upstream executes `deleteJiraIssue`. Latent
  only because no transport carries traffic yet; it goes live with the HTTP
  slice. The fix must be duplicate-key rejection at parse time, not a re-encode.
- `filterToolsList` returns early on any `error` member (`server.go:388`), so a
  reply carrying both `error` and `result` is passed through **unfiltered** and
  without even being confirmed as JSON-RPC 2.0. Every other branch there fails
  closed.
- The parse-error path echoes a caller-authored `id`: Go populates decoded
  fields before a type error, so `isScalarID` runs too late. Output goes back to
  the same client only, but the invariant the comment asserts does not hold.
- `boundToolName` duplicates `maxAuditFieldLen` and drops the rune-boundary
  guard, so the two truncations of the same value differ. Single-source it.
- No cap on concurrent connections; anything running as `hermes` can exhaust
  the broker's descriptors.

**Verification still missing:**

- Nothing renders the granted scope, so nothing verifies it matches the files.
  `Grant`/`ServiceGrant`/`ToolGrant`/`WriteTools` are exported for exactly this
  and still have no consumer. `AGENTS.md` §5.8a's "jawny, wyliczalny i
  weryfikowany" is not delivered.

  **This item is bigger than it reads, and research (2026-07-29) said why.** The
  upstream tool set is not fixed: it may change during a session, and MCP has a
  list-changed notification precisely because of that. So a renderer that reads
  only `policy.d/` renders our half of a comparison and calls it the scope.
  Three requirements the earlier framing did not carry:

  1. it must fetch `tools/list` **at render time** — a tool can appear upstream
     after the policy was last audited;
  2. without subscribing to list-changed, the drift between upstream and
     `policy.d/` is **silent**, so rendering on demand is not a convenience but
     the only signal there is;
  3. if the visible set depends on the credential the broker presents, the
     renderer must ask over the same credential, or it shows a scope that is not
     the reachable one. Any cache has to be bypassed or its age shown.

  Requirement 3 and the exact notification mechanics rest on the 2026-07-28 spec
  revision, which **we have not verified ourselves** (see below). Requirements 1
  and 2 hold regardless.
- Nothing checks `torio-mcp` stayed out of `torio-projects`. The installer is
  asserted not to do it; `status` never looks. The symmetric check for `hermes`
  exists.
- `internal/lima/mcpinstall.go` still reads a failed `sudo -n stat` as "the path
  is missing" in `ensureBrokerHome` and `ensurePolicyDir`. Both fail closed, so
  this is a wrong diagnosis rather than a hole — but it is the same shape the
  verification path just shed, and `statPath` is right there.

**Contract drift:**

- `cli.md:75` justifies the unused exit code 4 with "V1 nie ma silnika policy" —
  V1 now ships one whose whole job is denial. The conclusion (4 stays unused) is
  still right; the reason is false. Duplicated in `internal/cli/exit.go:26`.
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
  Its "domenowy egress allowlist" out-of-scope entry is **still correct** after
  the split — do not remove it while ADR-0026 is blocked.
- `docs/v1-evidence/FINDINGS-mcp-broker-boundary.md` says "ADR-0022 stands as
  written" (it was amended after) and "the broker daemon does not exist yet"
  (it does; nothing deploys it). Evidence must not be silently falsified by
  later work — correct it or add a dated successor.
- ADR-0024's Context no longer claims `hermes` has "no primitive to read, change
  or bypass" the ruleset in a way its own Precondition contradicts, but check
  the wording again when the ruleset actually ships.
- ADR-0023 rejects `hermes proxy` partly for authenticating nobody over loopback
  TCP — which its own chosen design also does. The valid distinguishing reason
  (no `openai-codex` adapter) stands alone; the authentication clause should go.
- `AGENTS.md:109`'s `8a.` is not a valid ordered-list marker; items 9–12 fold
  into item 8 on GitHub.

---

## 5. The shim: a live one-step path from the agent to root

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

## 6. Threat model — decided, still not in the architecture doc

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

The new `mcp_servers` check is deliberately built to this model and says so in
its own comment: the file belongs to the identity the check constrains, so it is
a drift detector, not a boundary.

**This belongs in `docs/03-architecture.md` and is still not there.**

---

## 7. Facts worth not re-deriving

Verified in Hermes source or on the live guest:

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
- **What that uid actually is**, read in `net/netfilter/nft_meta.c` at tag
  `v6.8`: `nft_meta_get_eval_skugid()` compares `sock->file->f_cred->fsuid` —
  the fsuid of whoever *created the socket*, not the sender and not euid.
  `f_cred` is set once in `init_file()` and never recomputed. Orphaned sockets,
  TIME_WAIT, request sockets and kernel sockets have no `struct file`
  (`sock_orphan()` clears `sk_set_socket`), so the rule does not match them —
  which makes fail-closed a property of `policy drop` plus a positive pass, not
  of the mechanism. A negated match (`! --uid-owner N`) *does* match an orphaned
  socket, so the inverse construction leaks exactly what it exists to catch.
  Reference `nft_meta_get_eval_skugid()`, not `xt_owner` and not the LKML
  thread: the semantics carry over from iptables, the proof for nftables is in
  that one function.
- **`SCM_RIGHTS` is the one way around the uid rule from the agent's uid.** The
  uid belongs to the open file description, and passing an fd shares it
  (`fd_install(new_fd, get_file(f))`), so a socket the broker created and handed
  over still carries `skuid torio-mcp`. Netfilter cannot close this; only the
  broker's design can. Recorded as ADR-0024 Decision 6.
- **`socket uid` / `socket gid` in nftables does not exist and is not coming.**
  The 2022 patchset sits at `changes-requested` and was declined on purpose in
  favour of making `meta skuid` usable everywhere. Do not design around a
  future selector.
- **There is no host-side enforcement point.** `192.168.5.0/24` is on no macOS
  interface; the guest's TCP/IP terminates inside `limactl hostagent` running as
  the operator's uid, so a pf `user 501` rule would match all of the operator's
  own traffic.
- A validated git remote always yields a host token, but it may be an **SSH
  config alias**, not a DNS name. A mechanically derived allowlist needs an
  explicit answer for that (now recorded in ADR-0026).
- The inference endpoint is written down **nowhere machine-readable** —
  `model.base_url` is empty; the host is only discoverable by grepping Hermes.

Unverified, and recorded as such: whether pf is enabled on the Mac; whether Lima
2.2.0 exposes any usernet egress knob; PyPI/npm/uv-python hostnames; how much
teardown traffic actually dies once `policy drop` is in force on this guest.

**The largest unverified item is not on that list, because it may not be a
detail.** Research (2026-07-29) reports an MCP specification revision dated
2026-07-28 that it calls the biggest protocol change since launch — a stateless
core, `Mcp-Session-Id` removed, `server/discover` and `subscriptions/listen`
added, Roots/Sampling/Logging deprecated, JSON Schema 2020-12. Only the
annotation claims (ADR-0025) were checked against primary sources; the rest of
the client surface was not. **If that revision is real, the broker's protocol
surface is designed against an older shape** — `ungovernedMethod` currently
refuses every method except `tools/list` and `tools/call`, which would mean
refusing calls a current client makes as a matter of course. Verify the revision
before writing anything else about the MCP surface, and before treating that
refusal as correct.

Also unresearched, and for a bad reason: **the upstream direction of Hermes
Agent**. It was dropped as "read the local sources instead", but the sources in
the guest say what Hermes does today, not where it is going. Hermes Agent is
public — repo, issues, pull requests and changelog are exactly the material to
read — and this is the single question that can invalidate ADR-0023 outright,
since that whole decision rests on "Hermes accepts nothing but http(s)", a
sentence resting only on our own reading. Highest-leverage next research
question; higher than systemd unit hardening, which we can settle from
`systemd.exec(5)` without help.

---

## 8. Suggested order for next session

1. **The unit slice**: `usermod -aG`, `/run/torio-mcp` with the client group and
   `RuntimeDirectoryMode=0750`, a systemd unit validated before activation,
   packaging for the guest binaries. This is what makes the daemon run at all,
   and nothing downstream is observable until it does.
2. **The broker correctness defects** (§4, first block) — duplicate JSON keys
   first, because it goes live with the HTTP transport and silently executes a
   different tool than the one audited.
3. **The remaining verification gaps** (§4, second block) — the granted-scope
   renderer is the one `AGENTS.md` §5.8a asks for by name.
4. The shim fix (§5) — independent, cheap, and it gates any later egress work.
5. Contract drift (§4, last block) as one sweep, including
   `docs/03-architecture.md` and the threat model from §6.
6. ADR-0026 stays blocked. Do not start it; ask the operator about §4 first.

Two of these are gated on a check, not on effort. **Verify the 2026-07-28 MCP
revision before item 2** — a duplicate-key fix written against the wrong
protocol shape is work done twice. And **ADR-0023 should not be implemented
before the upstream-Hermes question is answered**, because a transport other
than http(s) collapses that decision back into ADR-0022's shape and its custody
half is already known not to be the obstacle.

The upstream HTTP transport, OAuth, and the Atlassian migration come after the
unit slice, because none of them is observable until the daemon starts.
