## Edit a project with your own editor {#editor}

Torio adds no editor integration and mounts no macOS directory into the VM. A
project is an ordinary Git checkout at `/home/hermes/projects/<id>`, owned by
the `hermes` guest identity, on a minimal Ubuntu image. Whatever tool you use is
your own, reaching that checkout; it does not move the boundary — read access
stays your prerequisite, and commit and push stay decisions you make after
reading `git diff` and `git status`.

The simplest entrypoint is the one Torio gives you, because it also forwards
your SSH agent for the length of the session:

```bash
torio project shell my-service
```

If you want a shell without that capability, `limactl` logs you in as the Lima
user, not `hermes`, so become the checkout's owner explicitly:

```bash
limactl shell torio                  # interactive shell in the VM (Lima user)
sudo -iu hermes                      # become the hermes service identity
cd /home/hermes/projects/my-service
```

The guest is deliberately minimal — Python is present, most other tools are not
— so anything you want to run *inside* the VM you install in the VM yourself.
That install is your setup, not part of the control plane, and it stays inside
the VM: it must never add a Git remote, configure a credential helper, or grant
push access.

| Tool | How it reaches the checkout, and what to watch |
| --- | --- |
| Neovim, or any terminal editor | Run it inside the session above so edits are owned by `hermes`. If it is not in the guest, install it there once (for example `sudo apt-get install neovim`) — your setup, inside the VM. Your plugins, LSP servers, and config are the guest's, not your Mac's; keep a dotfiles checkout on the guest if you want them. |
| VS Code / Cursor, over Remote-SSH | Add `Include ~/.lima/torio/ssh.config` to your `~/.ssh/config` so the `lima-torio` host resolves, then *Connect to Host → lima-torio*. Include the file rather than copying a port: Lima reassigns the SSH port across VM restarts. One caveat worth stating plainly: Remote-SSH connects as the Lima user, not `hermes`, and installs a server component into that user's guest home — so saving files in the `hermes`-owned tree needs the remote window's integrated terminal running `sudo -iu hermes` for Git and for checks. For a single-identity session, the entrypoint above is cleaner. |
| Claude Code, or another terminal AI agent | Run the agent inside the VM as `hermes`, pointed at the checkout, so its edits land as `hermes`. Keep it to editing, inspection, and read-only checks; leave commit, push, remote changes, and any credential setup to you. Installing the agent and its runtime in the VM is your setup, outside the control plane and its credential neutrality. |
