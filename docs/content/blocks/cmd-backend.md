## Command surface — `torio backend` {#backend}

Reports the agent backend this instance runs and, for a backend that holds a
credential of its own, opens the session where you grant it one. The parent
command takes no action itself; an absent or unknown subcommand is a usage
error.

| Command | What it does |
| --- | --- |
| `torio backend status` | Report the backend name and guest identity, the version installed on the guest, whether a credential is present, and which capabilities the backend declares — a project registry, a guest service, an interactive session. Reads the guest, changes nothing, and never reaches the network. |
| `torio backend login` | Open an interactive terminal on the guest as the backend identity and start the backend so its own login flow runs. Interactive, so `--json` is a usage error. |

Which backend an instance runs is fixed at `torio vm init --backend NAME` and
recorded in that instance's config. A second backend means a second instance,
never a second agent inside one VM.

You do not track which instance that is. `--backend NAME` is a global flag: it
names the agent an invocation is about and selects the box that runs it — the
default backend keeps `torio`, the rest are `torio-<backend>`. `TORIO_INSTANCE`
still names a box directly, for a test VM or a second box running the same
backend, and wins over the flag; given both, a disagreement between the flag and
what the instance declares is a usage error rather than a guest built for one
identity being driven as another.

`login` grants the *box* a credential, not you. It is issued to the guest
identity and can be revoked without touching your own, and Torio never copies a
credential in from the host: a shared identity would couple revocation to a
machine you also work on and make the box's activity indistinguishable from
yours. Torio sees none of the flow — it builds the transport and hands over the
terminal. No SSH agent is forwarded, so a login session cannot reach a Git
remote.

What the session then shows is the backend's own flow, and it differs. Claude
Code starts and prompts. Codex asks for a device code, which is the flow that
works on a box with no browser: it prints a code and a URL you open wherever you
already are, and nothing has to reach the guest's loopback. An operator who
would rather use an API key can run `codex login --with-api-key` in a
`torio vm shell` session as the backend identity and paste the key on standard
input; both routes leave the credential in the same place, and `backend status`
reports it the same way.

`status` verifies and never repairs. It runs the same guest checks as `vm
bootstrap`, with every repair turned off: a box missing something bootstrap
would install — the pinned binary, the command-path link, the managed settings —
fails the check and names `torio vm bootstrap` as the remedy, rather than being
quietly rebuilt by a command you asked a question.

`status` answers four states for the credential, and only two of them are
findings. **present** and **absent** are what a probe returned.
**not-applicable** is for a backend that declares no credential check at all,
and **unknown** is for one that declares a check whose result is missing from
the report.

The distinctions are the point. A backend Torio cannot ask has not been found to
be logged out, and a backend Torio could ask but did not hear back from has not
been found to be unaskable. Each of the last two is one command's worth of
difference for an operator, and collapsing them is how a box that holds a
credential comes to report that its state is unknowable.

For a backend that is an MCP client, `status` also reports its configured server
names. Where this comes from an agent-owned file it is a drift
detector. Claude Code's released route is the root-owned managed MCP document;
unmanaged native entries are excluded by the pinned managed setting. Codex keeps
its declarations in a file the agent owns, and a root-owned allowlist decides
which of them may run, matching each against the relay path and the single
service argument it is permitted to carry. In every case tool permission comes
from the separate root-owned broker policy, not from the backend report. See
[ADR-0013](https://github.com/wzslr321/torio/blob/main/docs/adr/0013-mcp-managed-client-config-and-activation.md).
