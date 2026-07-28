## Pin a session token {#session-token}

`hermes serve` gates every non-public `/api/*` route behind an
`X-Hermes-Session-Token` header. That token is normally injected into the
dashboard SPA's `index.html`, but `serve` is headless and never renders that
page — so a remote Desktop client has nothing to read it from, and unauthorized
calls fail:

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:19119/api/sessions   # -> 401
```

`/api/status` is public and answers `200` either way, so a green readiness check
does **not** prove the token is usable.

Pin a token you can also paste into Desktop by setting
`HERMES_DASHBOARD_SESSION_TOKEN` in a systemd **drop-in**, kept separate from the
base unit so `torio serve install` re-rendering the unit does not wipe it.

### Generate the value

Any long random string works. Torio does not generate one for you — it handles
no secrets. For example:

```bash
python3 -c 'import secrets; print(secrets.token_urlsafe(32))'
```

This is the value you will paste into Desktop, so keep it somewhere you can copy
from once, such as your password manager.

### Create the drop-in on the guest

Do this in an **interactive shell on the guest**, not through `torio vm ssh`. Two
reasons, one of which fails silently:

- `torio vm ssh` forwards no stdin, so piping a file in produces an **empty file while still exiting `0`** — `echo … | torio vm ssh -- sudo -u hermes -- tee …` looks like it worked and did nothing.
- Passing the token as a command argument would put the secret in the control plane's logs and in `/proc` on the guest. Typing it into a shell you opened yourself keeps it out of both, which is what the credential-neutral boundary expects.

```bash
limactl shell torio                  # interactive shell in the VM (Lima user)
sudo -iu hermes                      # become the hermes service identity

install -d -m 700 ~/.config/systemd/user/hermes-serve.service.d
umask 077
nano ~/.config/systemd/user/hermes-serve.service.d/override.conf
```

Type these two lines, then paste your token immediately after the `=`:

```ini
[Service]
Environment=HERMES_DASHBOARD_SESSION_TOKEN=
```

Save with `Ctrl+O`, `Enter`, leave with `Ctrl+X`, then `exit` twice.

The token is typed, never pasted as part of a ready-made block, and the line
above deliberately stops at `=`. Copying a block that already contains a stand-in
value pins **that** value: the backend starts, Desktop connects, every check
passes, and the deployment is guarded by a token an attacker can read in the
documentation. Leaving the value empty fails visibly instead, which is the
failure you want.

The file stores the token in plain text, so it must not be group- or
world-readable: `700` on the directory, `600` on the file. `nano` writes through
a new file, so check the result rather than assuming:

```bash
torio vm ssh -- sudo -u hermes -- \
    stat -c '%a %U:%G %n' /home/hermes/.config/systemd/user/hermes-serve.service.d/override.conf
```

Confirm you pinned something, without printing it — this catches an empty value
and a value you meant to replace:

```bash
torio vm ssh -- sudo -u hermes -- \
    awk -F= '/SESSION_TOKEN/ {print "token_chars=" length($NF)}' \
    /home/hermes/.config/systemd/user/hermes-serve.service.d/override.conf
```

### Apply it

Reload and restart. Plain `sudo -u hermes` does not set `XDG_RUNTIME_DIR`, so
`systemctl --user` fails with `Failed to connect to bus: No medium found` unless
it is passed explicitly (the `hermes` uid is `1000`):

```bash
torio vm ssh -- sudo -u hermes -- \
    env XDG_RUNTIME_DIR=/run/user/1000 systemctl --user daemon-reload
torio serve restart --timeout 2m
```

Verify the gate now opens — `401` without the header, `200` with it:

```bash
curl -s -o /dev/null -w '%{http_code}\n' \
    -H 'X-Hermes-Session-Token: [REDACTED]' http://127.0.0.1:19119/api/sessions
```

Because the value is pinned in the drop-in it is stable across restarts.

### Rotate it

Rotate whenever the value has been anywhere it should not have been — a
screenshot, a paste buffer you shared, a terminal someone else watched. Generate
a new value, edit the drop-in with `nano` exactly as above, then repeat the
reload and restart. The old token stops working the moment the process comes
back, so **update Desktop in the same sitting**: a stale token there produces a
`401` that looks like a broken tunnel rather than a rejected credential.

> The token is a **secret**: generate your own, keep it out of the repository,
> its evidence, and any pull request or comment, and rotate it if it leaks.
