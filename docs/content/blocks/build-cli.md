## Install the CLI {#build-cli}

From a checkout of the `torio` repository, build the binary and put it on your
`PATH`. Every command in these docs is written as `torio`, so do this once and
the rest of the page just works:

```bash
go build -o torio ./cmd/torio
sudo install -m 755 torio /usr/local/bin/torio
```

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
next step creates the VM. That is the expected answer on a Mac that has never
run Torio, not a failure — it exits `0`.

> Prefer not to install system-wide? Any directory already on your `PATH` works
> — for example `install -m 755 torio ~/.local/bin/torio`. If you skip this
> step entirely, `torio` will not resolve at all: a freshly built binary is not
> on your `PATH` and your shell does not search the current directory, so you
> would have to prefix every later command with `./` and run it from the
> repository root.

### Installing a release build instead {#install-release}

Where a release exists and you would rather install its verified asset than
build, fetch the asset with a tool that already holds your credentials, then
point the installer at what you fetched:

```bash
gh release download vX.Y.Z -D /tmp/torio-rel
scripts/install.sh --version X.Y.Z --base-url file:///tmp/torio-rel
```

`install.sh` authenticates to nothing and never will. Torio stores and
transports no credentials, and an installer carrying a forwarded token would be
the one exception that makes the claim untrue. So a repository it cannot read
anonymously answers `404` from `api.github.com`, and `gh` — which does hold your
credentials — closes that gap without Torio touching them.

Either route verifies `SHA256SUMS` before the binary is copied anywhere, which
is the whole reason to use the installer rather than untarring by hand. Set
`TORIO_REPO=owner/name` if the assets live somewhere other than the default.
