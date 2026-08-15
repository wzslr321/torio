## Command surface — `torio ui` {#ui}

| Command | What it does |
| --- | --- |
| `torio ui` | Open the interactive hub: setup, box status, projects, the Second Brain, the guest service, and the MCP boundary on one screen. |

Running `torio` with no command on a terminal opens the same hub. `torio ui`
names it, so a wrapper script or a keybinding can ask for it explicitly.

Setup derives the next step from the current box state and skips capabilities
the selected backend does not declare. Actions use the same operation as their
CLI counterpart. One operation runs at a time; long operations show elapsed
time. Interactive sessions receive the terminal directly.

Keys are shown at the bottom of every screen. `1` to `6` and `tab` switch
screens, `enter` runs the highlighted thing, `r` re-reads state, `w` jumps to
the setup step you are on, `b` rebinds the hub to another backend, and `q`
quits.

- The hub emits no JSON. `--json` is a usage error (exit 2).
- Without a terminal on both standard input and standard output, `torio ui`
  exits **3** (`NOT_A_TERMINAL`). Bare `torio` in the same situation keeps its
  usage error (exit 2), unchanged, so scripts and CI jobs read what they always
  have.
- The project screen opens a session on Enter, an operator shell on `s`, a
  detail panel on `v`, and the remote correction on `e`. Its add form takes an
  id, an optional remote and an optional bundle; an id with neither, on a
  project the registry does not know, asks before making one that has no remote. The dashboard stops the
  bound box on `x`, and asks first, and opens a shell inside it on `s`, which is
  `vm shell`. `t` on the dashboard shows the status-line recipe for a tmux bar
  or a zsh prompt — the text `status setup` prints, shown and never written.
  The Brain tab reconciles this box's replica with the host vault on `y`, and
  imports a host directory on `m` — always through the preflight, so what
  would move is on screen, in counts, before anything does.
- The MCP tab is `mcp status` rendered: the checks, the identity separation
  they establish, and the granted policy. `i` provisions the boundary and `l`
  signs one policy service in, picked from the grant; when that session ends
  cleanly the hub runs the same activation `mcp login` runs, so the broker
  starts once the last service is signed in. No credential appears on any of
  these paths.
- `--verbose` has no effect while the hub owns the screen. Run the equivalent
  command for diagnostics.
- It opens on the instance and backend this invocation resolved. `b` rebinds
  it to another backend without quitting: the hub re-resolves the instance the
  same way `--backend` does, discards everything on screen, and re-reads the
  new box from nothing. The project registry is shared, so the same projects
  are listed on either side. The checkouts are not shared, so opening a project
  the new box has never held materializes its checkout first, from the remote on
  record, and then opens the session.
- A rebind also reconciles the Second Brain on both sides of the move: the box
  being left is synced before the binding changes, the box arrived at right
  after, and the note under the header says what each side carried, in counts.
  Neither sync can fail the rebind — a box that cannot sync is reported in that
  note and the move lands anyway.
