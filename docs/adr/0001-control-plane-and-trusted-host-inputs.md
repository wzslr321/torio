# ADR-0001: The control plane is one Go binary with verified host inputs

- Status: Accepted
- Date: 2026-08-05
- Consolidates: the Go/CLI choice, the Cobra dependency, the trusted config
  authority policy, the removal of the host state directory, and operator
  selection of the managed instance. The superseded originals are recoverable at
  `git show archive/pre-oss:docs/adr/…` (`0001`, `0012`, `0013`, `0014`, `0019`,
  `0021`).
- Applies to: `cmd/torio`, `internal/cli`, `internal/config`

## Context

Torio runs on a supported host (ADR-0002), drives `limactl`, Git and systemd inside a guest, and
must be runnable by a person who has installed one binary. It should not require
a system Python, modify the Hermes environment, or import Hermes' private
modules.

Two host-side inputs decide what the rest of the program does: the config
document and the name of the Lima instance it talks to. Both were reproduced as
spoofable before they were fixed.

**The config path was authority without enforcement.** `Load` read the file with
`os.ReadFile`, which follows symlinks, and checked permissions with `os.Stat`,
which stats the target rather than the link. A symlink from the default config
path to a mode-private file elsewhere was accepted, and its `default_timeout`
became the effective configuration. The same held for a world-writable config
directory: nothing checked the mode, type or ownership of the directory itself,
and `os.MkdirAll(dir, 0700)` does not tighten a directory that already exists.
`Lstat` before `ReadFile` is not a fix — the object can be swapped between the
check and the read.

**One hardcoded instance name collided with daily use.** `lima.InstanceName` was
the constant `"torio"`. Once the same machine was used both for real work and for
destructive testing, the two uses shared one Brain: the e2e harness imports a
synthetic vault into the single vault at a fixed path, with no way to tell a test
environment from a production one. "Do not run the harness on your own machine"
is a rule that lives in someone's head rather than in the product.

## Decision

**One Go binary. Every host input that carries authority is proven, not assumed.**

1. **Go 1.26.x**, toolchain pinned by `go.mod` and `.tool-versions`; module
   `github.com/wzslr321/torio`; binary and command `torio`. Standard library
   first, `log/slog`, `context`, `os/exec.CommandContext`, no `sh -c`, and typed
   interface adapters for Lima, Git and the filesystem.

2. **Cobra owns the command tree and nothing else.** The pinned dependency
   (`github.com/spf13/cobra`) parses commands and flags. The stable JSON
   envelope, the 0–9 exit code mapping, the stdout/stderr split, redaction and
   timeout validation stay in `internal/cli`, so replacing or dropping the
   framework does not touch the CLI contract.

3. **Trusted paths are opened no-follow and verified on the open descriptor.**
   Type, permission bits and ownership all come from a single `Fstat` on the same
   fd that is read — never from a second path resolution. Two properties are
   required together and must never be conflated in prose:

   - **mode-private** — `mode.Perm()&0o077 == 0`.
   - **owned-by-EUID** — `st_uid == os.Geteuid()`, strict equality.

   | Path | Symlink in the last component | Type | mode-private | owned-by-EUID |
   |---|---|---|---|---|
   | `ConfigDir`, if it exists | rejected (`O_NOFOLLOW\|O_DIRECTORY`) | directory | yes | yes |
   | default `config.json` | rejected (`O_NOFOLLOW`) | regular file | yes (`0600`) | yes |
   | explicit `--config PATH` | rejected | regular file | yes (`0600`) | yes |

   Strict equality is deliberate under root: `sudo torio` against a config owned
   by an ordinary user fails closed, because a mismatch between EUID and the
   owner of the authority is exactly the ambiguity being rejected. Enforcement is
   built for `darwin || linux`; elsewhere it is an explicit, documented no-op
   rather than a silent claim. `golang.org/x/sys/unix` and openat-relative
   resolution were rejected — `syscall.Openat` does not exist on darwin, and with
   the parent chain above `ConfigDir` treated as trusted, a full-path open with
   `O_NOFOLLOW` closes the reproduced vectors without a new dependency.

4. **Torio keeps no state on the host.** There is no state directory and no
   `--state-dir`. The only planned occupant was a version-lock manifest that was
   never wired to a consumer; the directory outlived its purpose, and its
   leftover permission check could fail `torio version` over a directory Torio
   neither creates nor reads. `config.json` has exactly one supported schema
   version; an unsupported one is a usage error, not a migration.

5. **The operator selects the instance, and the instance selects the config.**
   `TORIO_INSTANCE` names the managed Lima instance; unset means `torio`,
   byte-for-byte the previous behaviour. The project registry is host state, so it
   follows the instance automatically: the default instance keeps
   `$XDG_CONFIG_HOME/torio/config.json`, a named one gets
   `$XDG_CONFIG_HOME/torio/instances/<name>/config.json`, and `--config` always
   wins as an explicit trusted input. The name must match the project-id rule
   (1–64 chars, lowercase alphanumerics and hyphens, alphanumeric at both ends)
   and is resolved in `internal/config` before anything touches disk or the VM.
   An invalid name is exit 2 and **never** a silent fall back to the default —
   falling back would aim a command meant for the test VM at the daily one, which
   is the failure this mechanism exists to prevent.

Guest paths stay hardcoded. A separate instance is a separate guest, so
`/home/hermes/brain` in the test VM is already a different directory.

## Consequences

- A single binary distributes and recovers easily, and Hermes is reached only
  through its public CLI, never its Python API.
- Cobra is a build-time dependency inside the trusted computing base. It is
  pinned, narrow, and not a runtime security component.
- Config authority cannot be spoofed by a symlink, a permissive directory or a
  foreign-UID file, and the failure is loud.
- Errors crossing the `internal/config` boundary are redacted, because a
  caller-controlled path can itself be secret-shaped.
- A typo in `TORIO_INSTANCE` is loud on write and quiet on read:
  `TORIO_INSTANCE=torio-tset vm status` answers `not_found`, because that is a
  correct answer about a VM that does not exist. Accepted — the alternative is a
  registry of known instances, which is host state this ADR removes.
- Every new reader of the instance name must take the resolved value; a test
  guards against a hardcoded literal outside the default.

## Rejected

- **Bash or Python as the control plane.** Weaker typing and quoting; harder
  packaging and runtime isolation. Rust: stronger safety, slower to build here.
- **A hand-rolled stdlib dispatcher.** Avoids a dependency by reimplementing help,
  nested subcommands and suggestions, which Cobra already has tested.
- **`Lstat` before read.** TOCTOU.
- **`os.Root` as the primary primitive.** It validates neither ownership nor the
  mode of the directory itself.
- **Keeping the state directory for future use.** The same promise the
  version-lock manifest made. An empty directory does not accelerate a future
  feature; it specifies it prematurely.
- **`--instance` as a flag instead of an environment variable.** The instance is a
  property of a working session, not of one invocation. A flag would have to be
  repeated, and omitting it once would aim the command at the wrong machine.
- **A shared config across instances.** The project registry is host state; if it
  were shared, `project list` would show daily projects while talking to the test
  VM.
