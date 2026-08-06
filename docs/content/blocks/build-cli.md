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

### Building from source instead {#install-source}

With a Go toolchain, build the binary and put it on your `PATH`:

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
