## Reach the backend over your own SSH tunnel {#tunnel}

The backend binds `127.0.0.1:9119` *inside* the VM, so you forward a host
loopback port to it over SSH. Torio deliberately adds no tunnel feature — you
control the forward, which means network exposure is never an accident of
running a command.

Derive the forward from the supported live Lima SSH config and open it:

```bash
ssh -F ~/.lima/torio/ssh.config -L 19119:127.0.0.1:9119 -N -f \
    -o ExitOnForwardFailure=yes lima-torio
```

Verify it from the host — you should get `200`:

```bash
curl -s -m 5 -o /dev/null -w '%{http_code}\n' http://127.0.0.1:19119/api/status
```

Tear the tunnel down when you are done by killing the `ssh` process holding the
forward.

> `overall:degraded` in `/api/status` is **expected** when the messaging gateway
> is stopped — the serve backend/dashboard component is still `ok`.
