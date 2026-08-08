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

`status` answers three states for the credential, and the third is not a
softened second: **present**, **absent**, and **not-applicable** for a backend
Torio has no offline way to ask. A backend it cannot ask has not been found to
be logged out.

For a backend that is a native MCP client, `status` also lists the MCP servers
the guest is configured with, by name. Read that as what is configured, never as
what is permitted: the list comes from a file the agent owns and can rewrite,
and the servers carry credentials of yours that live under the agent's own
identity. Revocation is at the provider. This is a hole the project names rather
than hides — see
[ADR-0009](https://github.com/wzslr321/torio/blob/main/docs/adr/0009-backend-contract-and-claude-code.md)
and the MCP broker work it points at.
