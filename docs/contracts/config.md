# Config contract

This document describes the typed host configuration. Implementation:
`internal/config/`. The configuration is **non-secret** (`AGENTS.md` §6):
secret-shaped material is rejected.

> **There is no version-lock manifest.** `version-lock.json` was designed but
> never wired up: no command read it, and its only consumer was never called. The
> code and its description were removed —
> [ADR-0001](../adr/0001-control-plane-and-trusted-host-inputs.md). The path trust
> boundary below applies unchanged to `config.json`.

> **There is no state directory.** `XDG_STATE_HOME`, `Paths.StateDir` and the
> `--state-dir` flag existed only for the version-lock manifest and went with it —
> [ADR-0001](../adr/0001-control-plane-and-trusted-host-inputs.md). Torio writes no
> persistent host state other than `config.json`.

## Locations (XDG)

Paths resolve deterministically from XDG variables, with documented fallbacks:

| Role | Base (env) | Fallback | Application directory |
|---|---|---|---|
| Config | `XDG_CONFIG_HOME` | `$HOME/.config` | `<base>/torio/` |

Rules:

- A set but **non-absolute** `XDG_CONFIG_HOME` is rejected fail-closed — never
  silently ignored or "fixed".
- When the XDG base is unset and `$HOME` cannot be determined, resolution fails
  closed rather than guessing a location.
- The `$HOME` fallback must be **absolute**. A non-absolute `$HOME` (with XDG
  unset) is rejected fail-closed and is not canonicalized against the working
  directory, for the same reason as a non-absolute XDG base: the working directory
  must not decide where the trusted config lives.
- `--config PATH` overrides the value and is canonicalized (`filepath.Abs` +
  `Clean`, without resolving symlinks — an explicit override is a trusted input).
- **Precedence:** an explicit `--config` **bypasses** the XDG base entirely, so a
  malformed `XDG_CONFIG_HOME` or `$HOME` cannot block a fully explicit
  invocation. XDG is consulted only when there is no override, and is still
  strictly validated then.
- With an explicit `--config`, the trusted config directory is the parent of the
  named file.
- A file located **inside** the trusted directory (`config.json`) uses a contained
  join: the name must be a plain file name and the result must not leave the base
  directory. Traversal is rejected structurally, not by string cleaning.

## Path trust boundary ([ADR-0001](../adr/0001-control-plane-and-trusted-host-inputs.md))

Before `config.json` becomes authority, its paths are enforced fail-closed. The
terminology is deliberately split — "owner-only" is not a thing:

- **mode-private** — no group or other access: `perm & 0o077 == 0`.
- **owned-by-EUID** — the object's owner is the process's effective user:
  `st_uid == geteuid()`, strict equality. As root, a root-owned object is what is
  expected.

Rules, enforced on **darwin/linux**; elsewhere an explicit, documented no-op:

- Trusted files (`config.json`, including an explicit `--config`) are opened
  **no-follow** (`O_NOFOLLOW`): a symlink in the last component is rejected. Type,
  mode and ownership are verified with `Fstat` **on the same descriptor** the read
  comes from — no TOCTOU on the last component, and `Lstat` + `ReadFile` is not
  permitted. The file must be regular, mode-private (`0600`) and owned-by-EUID.
- The immediate trusted directory (`ConfigDir`), if it exists, must be a
  non-symlink directory, mode-private and owned-by-EUID. A missing directory is a
  valid first run. Validation opens it `O_RDONLY|O_DIRECTORY`, so a trusted
  directory must be **usable as an application directory** — in practice `0700`
  (mode-private alone would admit something like `0100`, which fails closed at
  open).
- **Scope:** only the immediate application directory is validated. The ancestor
  chain above it (XDG base, `$HOME`) is treated as trusted and is outside this
  boundary; there is no full ancestor walk.
- **Explicit `--config`:** the *file* gets full enforcement (no-follow, type,
  mode-private, owned-by-EUID); the mode of its parent directory is **not**
  required, so an operator may point at a shared location.
- **Writes:** `WriteFile` validates the trusted directory **before** creating any
  file — an atomic rename must not "legalize" a symlinked, permissive or
  foreign-owned directory as authority.
- Permission, type and path errors do not reveal secret-shaped material;
  redaction happens at the package boundary.

## The config document — `config.json`

- Default location: `<config-dir>/config.json`, or an explicit `--config PATH`.
- Format: JSON, standard library. Exactly one document; trailing data is an error.
- Unknown fields are rejected (`DisallowUnknownFields`) — a fail-closed schema.
- Ownership, permissions and type: on darwin/linux a regular file, mode-private
  (`0600`) and owned-by-EUID, opened no-follow. Wider bits, a symlink, a foreign
  owner or a non-regular type are rejected.
- A missing default config is a **valid first run** and yields defaults. An
  explicit `--config` naming a file that does not exist is an error (exit 2).

Fields:

| Field | Type | Required | Meaning |
|---|---|---|---|
| `schema_version` | string | yes | `"2"`. Any other value is rejected, with no migration. |
| `default_timeout` | string (Go duration) | no | Default operation timeout; validated > 0 and ≤ the policy maximum. Feeds the timeout policy when `--timeout` is not given explicitly (the flag wins). |
| `projects` | array | no | The project registry — see below. Omitted normalizes to an empty registry. |

A valid document:

```json
{
  "schema_version": "2",
  "default_timeout": "45s",
  "projects": [
    {
      "id": "my-project",
      "display_name": "My Project",
      "remote": "git@github.com:owner/my-project.git"
    }
  ]
}
```

## Project registry

The registry is the **non-secret** source of truth about attached projects
([ADR-0003](../adr/0003-ownership-split-and-operator-carried-write.md)).

**A workspace path is not a field.** It is derived from `id` as
`/home/hermes/projects/<id>` by the projects layer, so the config cannot point a
project at an arbitrary guest path. A project object carrying a `path` field is
rejected like any unknown field.

| Field | Type | Required | Rule |
|---|---|---|---|
| `id` | string | yes | Lowercase ASCII slug: letters, digits and internal `-`; ≤ 64 bytes; unique within the document. The same charset derives the workspace path, so nothing in it can traverse or change directory. |
| `display_name` | string | yes | Non-empty, ≤ 64 bytes, valid UTF-8, no control characters, no leading or trailing whitespace. |
| `remote` | string | yes | A supported transport form (below); ≤ 512 bytes. |

Supported `remote` forms:

- `https://host[:port]/path` — **no userinfo at all**, because that is the one
  place an HTTPS token or password sits;
- `ssh://[user@]host[:port]/path` — a username is a non-secret transport element
  and is allowed; a password never is;
- `[user@]host:path` — the scp-like form with a **relative** path (an absolute one
  would make a local `C:/repo` look like a remote).

Rejected fail-closed: query and fragment (either can carry a token),
percent-encoding (it hides the former), control characters and whitespace, a
leading `-` (Git would read the remote as a flag), a local path, `file://`,
`http://`, `git://`, and secret-shaped material in any field.

Registry-wide rules:

- **Unique `id`** — enforced always, including on read.
- **Duplicate `remote`** — rejected by default **when adding**
  (`AddOptions.AllowDuplicateRemote` is an explicit operator decision). Document
  validation does not reject it: a decision once taken deliberately must not make
  the config unreadable.
- **Bounded** — at most 64 projects; the config is read on every invocation.
- The registry is validated **on read and on write**, so a hand-edited document
  cannot smuggle in an entry the write path would refuse.

## Schema version

- `"2"` is the **only** supported version, on read and on write. A `File`
  declaring another version is rejected on write rather than quietly upgraded.
- The predecessor `"1"` (settings-only, before the registry) is **not read**.
  Torio never had a release that wrote such a document, so none exists —
  [ADR-0001](../adr/0001-control-plane-and-trusted-host-inputs.md). A hand-written
  `"1"` document is rejected explicitly (exit 2), not read as settings-only.
- An older binary **explicitly rejects** this document, both through its own
  version gate and through `DisallowUnknownFields` on `projects`. It cannot read
  it as settings-only and silently drop the registry. That guarantee lives on its
  side and holds regardless of what the current binary reads.

## Writing the config

- The write is crash-safe: private temp → `fsync` → atomic rename, `0600`, in a
  `0700` directory.
- The document is validated **before** the file is created, and the trusted
  directory **before** the write — an atomic rename does not legalize a symlinked,
  permissive or foreign-owned directory as authority.
- Projects are sorted by `id`, so the same registry produces the same file
  regardless of the order things were added.
- After the rename the file is **read back** through the same trusted path the
  loader uses, parsed, validated and compared with the document that was meant to
  be written. A mismatch is reported, not silently repaired — the rename already
  happened, so the decision is the operator's.

## Exit codes and errors

Configuration resolution and validation failures map to a usage/schema error
(exit `2`), per [`cli.md`](cli.md). Error messages do not reveal secret-shaped
material; the guarantee is enforced at the `internal/config` package boundary by
redacting every returned error, so it holds for direct API calls too and not only
through the CLI renderer.

Scanning raw bytes rejects secrets early but is not sufficient on its own: a
secret-shaped value written JSON-escaped — for example with a letter of its prefix
encoded as `\uXXXX` — has no literal prefix in the raw bytes, so the decoder could
reconstruct it and place it into an error, either by interpolating the decoded
value with `%q` or through an unknown field name returned by
`DisallowUnknownFields`. Boundary redaction closes that path; the final renderer
redacts known shapes as defence in depth.
