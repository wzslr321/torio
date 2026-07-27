## Connect Hermes Desktop {#desktop-connect}

Desktop talks to the same loopback endpoint you forwarded, so bring the tunnel up
first and confirm both ends are ready: `torio serve status` exits `0`, and the
`curl` to `http://127.0.0.1:19119/api/status` returns `200`.

In Hermes Desktop → Settings → **Gateway Connection → Remote gateway**, set:

| Field | Value |
| --- | --- |
| Remote URL | `http://127.0.0.1:19119` — the Mac end of the SSH forward to the guest backend on `127.0.0.1:9119` |
| Session token | the value you pinned in the drop-in |

After **Save and reconnect**, the status bar shows the remote endpoint plus
matching client and backend versions.
