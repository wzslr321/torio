## Edit a project with your own editor {#editor}

Torio mounts no host directory into the VM. A project is an ordinary Git
checkout at `/home/claude/projects/<id>`, owned by the `claude` guest identity.
Editors reach that checkout inside the VM or over SSH. The optional Neovim panel
under `integrations/neovim` uses the same routes.

The normal entrypoint opens the checkout without forwarding your SSH agent:

```bash
torio project enter my-service
```

Only open the push-capable operator session when you intentionally need it:

```bash
torio project shell my-service
```

The guest is deliberately minimal — Python is present, most other tools are not
— so anything you want to run *inside* the VM you install in the VM yourself.
That install is your setup, not part of the control plane, and it stays inside
the VM: it must never add a Git remote, configure a credential helper, or grant
push access.

| Tool | How it reaches the checkout, and what to watch |
| --- | --- |
| Neovim, or any terminal editor | Run it inside `project enter` for ordinary work. If it is not in the guest, install it there once (for example `sudo apt-get install neovim`). Your plugins, LSP servers, and config are the guest's, not your host's. The host-side `:Torio` panel lists projects, opens routine or push-capable terminals, reports health, and shows open sessions. |
| VS Code / Cursor, over Remote-SSH | Add `Include ~/.lima/torio/ssh.config` to your `~/.ssh/config` so the `lima-torio` host resolves, then *Connect to Host → lima-torio*. Include the file rather than copying a port: Lima reassigns the SSH port across VM restarts. See the caveat below the table. |
| Claude Code, or another terminal AI agent | Run the agent inside the VM as `claude`, pointed at the checkout, so its edits and commits land as `claude`. Leave pushes, remote changes, and credential setup to an operator session. Installing the agent and its runtime in the VM is outside the Torio control plane. |

Caveat for Remote-SSH: it connects as the Lima user, not `claude`, and installs
a server component into that user's guest home — so saving files in the
`claude`-owned tree needs the remote window's integrated terminal running
`sudo -iu claude` for Git and for checks. For a single-identity session,
`project enter` is cleaner.
