## Install the CLI {#build-cli}

From a checkout of the `torio` repository, build the binary and put it on your
`PATH`. Every command in these docs is written as `torio`, so do this once and
the rest of the page just works:

```bash
go build -o torio ./cmd/torio
sudo install -m 755 torio /usr/local/bin/torio
```

Confirm it resolves and runs, using a read-only command:

```bash
which torio
torio vm status
```

You should get one line naming the VM and its state, for example
`torio: Stopped`.

> Prefer not to install system-wide? Any directory already on your `PATH` works
> — for example `install -m 755 torio ~/.local/bin/torio`. If you skip this
> step entirely, `torio` will not resolve at all: a freshly built binary is not
> on your `PATH` and your shell does not search the current directory, so you
> would have to prefix every later command with `./` and run it from the
> repository root.
