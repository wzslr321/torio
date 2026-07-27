## Point Desktop at the Code V0 workspace {#desktop-workspace}

Registering the Hermes project does **not** by itself change what Desktop shows.
Settings → **Workspace → Working Directory** defaults to `.`, which resolves
against the serve unit's `WorkingDirectory=/home/hermes/hermes-agent` — the
Hermes source checkout — so the file tree shows the wrong repository. Set it
explicitly:

```text
/home/hermes/projects/REDACTED-PROJECT
```

Optionally set **Repository Discovery Roots** to `/home/hermes/projects` so
discovery stays inside the workspace root.

Two dead ends worth skipping: the folder chip in the status bar opens a context
menu, not a project switcher; and Desktop's **Terminal** tab is a shell on your
**Mac**, not on the guest.
