## Push, when you decide to {#project-push}

The persistent Hermes backend has read access to your checkouts and nothing
more. It cannot push, and no credential of yours is stored anywhere it could
reach.

When you want to write to a remote, open a session that carries your own
capability:

```bash
torio project shell my-service
```

This forwards your Mac's SSH agent into an interactive session in the checkout.
The capability lives exactly as long as the session does and leaves with you
when you exit. Inside it you are the `hermes` identity, in the project
directory, with your agent available to Git:

```bash
git status
git diff
git commit -am 'the change you decided to make'
git push
exit
```

Torio preflights the session — the project registered, the VM
bootstrap-verified, the checkout present with the registered origin and shared
permissions, your local agent actually holding an identity to forward. It
**never test-pushes** to prove any of that, because a test push is a write you
did not ask for.

Once you exit, Torio makes no claim about what happened. It does not know
whether you pushed, and it will not tell you that you did. Check the remote
yourself.

The session is not bounded by `--timeout`; you end it. It is interactive, so it
does not support `--json` — there is no document to emit, and asking for one is
a usage error rather than a silently ignored flag.

> Your agent must be loaded before you start: `ssh-add -l` should list an
> identity. An empty agent fails the preflight rather than opening a session
> that cannot push.
