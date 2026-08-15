## Command surface — `torio ui` {#ui}

| Command | What it does |
| --- | --- |
| `torio ui` | Open the interactive hub: setup, box status, projects, the Second Brain, and the guest service on one screen. |

Running `torio` with no command on a terminal opens the same hub. `torio ui`
names it, so a wrapper script or a keybinding can ask for it explicitly.

Setup derives the next step from the current box state and skips capabilities
the selected backend does not declare. Actions use the same operation as their
CLI counterpart. One operation runs at a time; long operations show elapsed
time. Interactive sessions receive the terminal directly.

Keys are shown at the bottom of every screen. `1` to `5` and `tab` switch
screens, `enter` runs the highlighted thing, `r` re-reads state, `w` jumps to
the setup step you are on, `b` rebinds the hub to another backend, and `q`
quits.

- The hub emits no JSON. `--json` is a usage error (exit 2).
- Without a terminal on both standard input and standard output, `torio ui`
  exits **3** (`NOT_A_TERMINAL`). Bare `torio` in the same situation keeps its
  usage error (exit 2), unchanged, so scripts and CI jobs read what they always
  have.
- `--verbose` has no effect while the hub owns the screen. Run the equivalent
  command for diagnostics.
- It opens on the instance and backend this invocation resolved. `b` rebinds
  it to another backend without quitting: the hub re-resolves the instance the
  same way `--backend` does, discards everything on screen, and re-reads the
  new box from nothing. The project registry is shared, so the same projects
  are listed on either side. The checkouts are not shared, so opening a project
  the new box has never held materializes its checkout first, from the remote on
  record, and then opens the session.
