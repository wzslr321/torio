## Command surface — `torio ui` {#ui}

| Command | What it does |
| --- | --- |
| `torio ui` | Open the interactive hub: setup, box status, projects, the Second Brain, and the guest service on one screen. |

Running `torio` with no command on a terminal opens the same hub. `torio ui`
names it, so a wrapper script or a keybinding can ask for it explicitly.

The hub is a second way to reach the operations, not a second implementation of
them. Every action it offers calls exactly what the equivalent command calls, so
`torio vm bootstrap` and the hub's bootstrap step do the same work in the same
way. What the hub adds is the part a command surface cannot: it holds the order
of a multi-step setup, so it can show where you are rather than only what to
type next.

The Setup screen derives the next step from the box itself, not from a list you
are assumed to have followed. A box that was never created starts at creation; a
bootstrapped box whose backend holds no credential goes to the login; a backend
that installs no service is never shown a service step, because there is nothing
there to install. The Dashboard shows every box on the host and carries the same
next step, so both screens answer the question the same way.

Long steps say so. Bootstrap can take up to ten minutes on a fresh box, and the
hub shows a spinner and the elapsed time for the whole wait rather than going
quiet. One operation runs at a time.

Interactive work is handed the real terminal. Backend logins, agent sessions and
shells run as themselves, with `Ctrl-C` reaching the session rather than the
hub, and the hub redraws when the session ends.

Keys are shown at the bottom of every screen. `1` to `5` and `tab` switch
screens, `enter` runs the highlighted thing, `r` re-reads state, `w` jumps to
the setup step you are on, and `q` quits.

- The hub is interactive and emits no JSON. `--json` is a usage error (exit 2):
  the machine-readable answers stay with the commands that produce them.
- Without a terminal on both standard input and standard output, `torio ui`
  exits **3** (`NOT_A_TERMINAL`). Bare `torio` in the same situation keeps its
  usage error (exit 2), unchanged, so scripts and CI jobs read what they always
  have.
- The hub silences the diagnostic logger while it draws, so `--verbose` has no
  effect on it. Run the equivalent command when you need diagnostics.
- It works on the instance and backend this invocation resolved. To open it
  against another backend, quit and run `torio --backend <name> ui`.
