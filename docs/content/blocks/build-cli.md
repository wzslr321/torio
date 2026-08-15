## Install the CLI {#build-cli}

Every command in these docs is written as `torio`, so put the binary on your
`PATH` once and the rest of the page just works.

Where a release exists, install its verified asset. From a checkout of the
`torio` repository:

```bash
scripts/install.sh                       # latest stable release
scripts/install.sh --version X.Y.Z       # or a specific one, without the leading v
```

The installer resolves the release, verifies `SHA256SUMS` before the binary is
copied anywhere — which is the whole reason to use it rather than untarring by
hand — and installs into `~/.local/bin` by default. It needs a published
release to exist; before one does, build from source below.

`install.sh` authenticates to nothing and never will. Torio stores and
transports no credentials, and an installer carrying a forwarded token would be
the one exception that makes the claim untrue. So a repository it cannot read
anonymously answers `404` from `api.github.com`, and `gh` — which does hold your
credentials — closes that gap without Torio touching them:

```bash
gh release download vX.Y.Z -D /tmp/torio-rel
scripts/install.sh --version X.Y.Z --base-url file:///tmp/torio-rel
```

Either route verifies the same checksums. Set `TORIO_REPO=owner/name` if the
assets live somewhere other than the default.

### Installing a dev build {#install-dev}

To try what is on `main` before it is released:

```bash
scripts/install.sh --channel dev
```

This installs the build of the last commit that reached `main`, into
`~/.local/share/torio-dev/bin`, and links it into `~/.local/bin` under the name
`torio-dev`. A stable install keeps its own directory and its own name, so both
are available at once and neither overwrites the other's guest payloads. Rerun
the same command to move to a newer build. `--link-dir DIR` links it somewhere
else, `--no-link` skips the link.

A dev build is not a release. It carries whatever reached `main` and has passed
the pull-request gate; the release gates that boot a guest and install the macOS
archive on a Mac have not run against it. Checksums are verified exactly as they
are for a release, and `torio-dev version` reports the commit it was built from.

The two installs are separate binaries, not separate states: `torio-dev` reads
the same configuration and talks to the same boxes as `torio`. Where that
matters, point it at a box of its own:

```bash
TORIO_INSTANCE=torio-devbox torio-dev vm status
```

### Building from source instead {#install-source}

From a checkout, one command builds the working tree and installs it the same
way, under a third name:

```bash
make local
```

It builds for this host only, installs into `~/.local/share/torio-local/bin`,
and links `~/.local/bin/torio-local`. Nothing is published and no tag is
touched. `torio-local version` reports the branch, the commit and whether the
tree was dirty when it was built, so the binary names what you are testing. Run
it again after a change to replace it, and delete the two paths above to be rid
of it.

To place a binary yourself instead, with a Go toolchain:

```bash
go build -o torio ./cmd/torio
sudo install -m 755 torio /usr/local/bin/torio
```

> Prefer not to install system-wide? Any directory already on your `PATH` works
> — for example `install -m 755 torio ~/.local/bin/torio`. If you skip this
> step entirely, `torio` will not resolve at all: a freshly built binary is not
> on your `PATH` and your shell does not search the current directory, so you
> would have to prefix every later command with `./` and run it from the
> repository root.

Confirm it resolves and runs:

```bash
which torio
torio version
```

`version` is the only place the operator reads which build they have:

```text
torio dev (commit …, built …)
go1.26.5 darwin/arm64
```

A binary built straight from a checkout calls itself `dev`; the commit is the
one you built.

`torio vm status` also works from here and answers `torio: not_found` until the
next step creates the VM. That is the expected answer on a host that has never
run Torio, not a failure — it exits `0`.
