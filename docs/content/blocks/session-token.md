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
limactl shell torio            # interactive shell in the VM (Lima user)
sudo -iu hermes                      # become the hermes service identity

install -d -m 700 ~/.config/systemd/user/hermes-serve.service.d
umask 077
cat > ~/.config/systemd/user/hermes-serve.service.d/override.conf <<'EOF'
[Service]
Environment=HERMES_DASHBOARD_SESSION_TOKEN=PASTE-YOUR-TOKEN-HERE
EOF
chmod 600 ~/.config/systemd/user/hermes-serve.service.d/override.conf
```

The file stores the token in plain text, so it must not be group- or
world-readable: `700` on the directory, `600` on the file. Confirm with:

```bash
torio vm ssh -- sudo -u hermes -- \
    stat -c '%a %U:%G %n' /home/hermes/.config/systemd/user/hermes-serve.service.d/override.conf
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

Because the value is pinned in the drop-in it is stable across restarts. Rotate
it by editing the drop-in and repeating the reload and restart above.

> The token is a **secret**: generate your own, keep it out of the repository,
> its evidence, and any pull request or comment, and rotate it if it leaks.
