# Service and lifecycle contract

## Procesy

- `hermes serve` — backend Desktop/remote; custom user service Hermes Box.
- `hermes gateway` — messaging adapters i Kanban dispatcher; native gateway service.
- `hb` — początkowo CLI; daemon/socket tylko gdy wymaga tego admission/admin separation.
- Docker Engine — VM service.

## Bind

Demo A:

```text
backend bind: 127.0.0.1 inside VM
authorised path: SSH tunnel lub zweryfikowany Desktop SSH transport
```

Nie używać `0.0.0.0` bez osobnego ADR-u, auth i realnej potrzeby.

## Feature detection

`hb serve install` najpierw wykonuje:

```text
hermes serve --help
```

Jeśli brak `serve`, używa tylko udokumentowanego i spike-verified compatibility path. Nie zakłada, że `dashboard --no-open` zawsze istnieje.

## Readiness

Service jest ready dopiero gdy:

- process aktywny,
- expected loopback port listening,
- `/api/status` odpowiada,
- WebSocket/live Desktop path został zweryfikowany w acceptance test,
- gateway status jest osobno raportowany.

PID/service active bez endpointu nie oznacza ready.

## Restart

- Service restart nie usuwa sessions/state.
- VM reboot uruchamia potrzebne services dopiero po network/filesystem readiness.
- Gateway unit jest zarządzany natywnymi komendami Hermesa.
- `hb doctor` nie modyfikuje systemu bez explicit repair command.
