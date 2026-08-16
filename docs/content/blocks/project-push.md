## Push, when you decide to {#project-push}

The persistent backend receives no operator write credential. A private
repository's guest deploy key is read-only only if you authorized it that way;
Torio cannot verify the forge setting.

When you want to write to a remote, open a session that carries your own
capability:

```bash
torio project shell my-service
```

This forwards your host's SSH agent into an interactive session in the checkout.
The capability lives exactly as long as the session does and leaves with you
when you exit. Inside it you are the agent identity, in the project
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

### Pin one key, and approve each signature {#operator-key}

By default the session forwards your agent whole: every identity it holds is
usable inside. To narrow that, set `operator_key` in the config document to a
fingerprint or key comment naming the one identity a session may use. With the
pin set, `project shell` forwards a mediated agent instead: it lists the pinned
key alone, asks you on the host before every signature (a dialog naming the
project, the remote, the branch and how far ahead it is; Deny is the default
and the cancel), and records each decision to `agent-audit.jsonl` beside the
config before acting on it. The dialog reports what the checkout held when the
session opened; Torio still makes no claim about what a signature was used for.

### Let an agent session ask to push {#push-grant}

An agent session normally receives no operator write route. With a pinned
`operator_key`,

```bash
torio project agent my-service --push-grant
```

opens one that may **ask**: the mediated agent is reachable inside the session,
every signature waits for your confirmation on the host, and an unanswered
dialog denies. The grant lasts one invocation; no config field turns it on.
Without a pin the flag is refused outright, and a preflight refuses an origin
the grant could not serve (an HTTPS push URL never consults an SSH agent, and a
host key missing from the agent identity's `known_hosts` stops a push before it
reaches the key), each with its remedy.

The session is not bounded by `--timeout`; you end it. It is interactive, so it
does not support `--json` — there is no document to emit, and asking for one is
a usage error rather than a silently ignored flag.

> Your agent must be loaded before you start: `ssh-add -l` should list an
> identity. An empty agent fails the preflight rather than opening a session
> that cannot push.
