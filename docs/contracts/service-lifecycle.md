# Service and lifecycle contract

## Procesy

- `hermes serve` — backend Desktop/remote; custom user service Hermes Box.
- `hermes gateway` — messaging adapters i Kanban dispatcher; native gateway service.
- `torio` — początkowo CLI; daemon/socket tylko gdy wymaga tego admission/admin separation.
- Docker Engine — VM service.

## Bind

Demo A:

```text
backend bind: 127.0.0.1 inside VM
authorised path: SSH tunnel lub zweryfikowany Desktop SSH transport
```

Nie używać `0.0.0.0` bez osobnego ADR-u, auth i realnej potrzeby.

## Feature detection

`torio serve install` najpierw wykonuje:

```text
hermes serve --help
```

Jeśli brak `serve`, używa tylko udokumentowanego i spike-verified compatibility path. Nie zakłada, że `dashboard --no-open` zawsze istnieje.

### D5 (V1) — zweryfikowany surface backendu

Live discovery (`hermes serve --help` na zainstalowanym Hermes v0.19.0) ustaliło:

- `hermes serve` domyślnie binduje `--host 127.0.0.1 --port 9119` (loopback) i jest headless;
- `--skip-build` serwuje backend bez kroku build web UI (npm) — używane w non-interactive unit;
- endpoint gotowości to `GET /api/status` → 200 (JSON z `version`), nieautoryzowany; `/api/health|info|version`
  zwracają 401;
- `hermes serve --stop/--status` używają naiwnego dopasowania procesów (liczą własny proces zapytania) i są
  **niewiarygodne** — dlatego backend jest zarządzany przez custom user systemd unit, a readiness jest
  dowodzony przez stan systemd **oraz** probe HTTP, nigdy przez `serve --stop/--status`.

Custom unit (`hermes-serve.service`, user `hermes`): loopback bind pinowany w unicie, `HERMES_HOME=/home/hermes/.hermes`,
`Restart=always`, `WantedBy=default.target` + `linger` (start po reboot bez sesji). Walidowany
`systemd-analyze --user verify` przed aktywacją; zapis atomowy (staging → verify → rename).

## Readiness

Service jest ready dopiero gdy:

- process aktywny (`systemctl --user is-active == active`),
- expected loopback port listening,
- `/api/status` odpowiada 200 przez loopback,
- WebSocket/live Desktop path został zweryfikowany w acceptance test (human confirmation, poza D5),
- gateway status jest osobno raportowany (D5 backend nie zależy od gateway; `overall:degraded` przy
  zatrzymanym gateway jest oczekiwane i nie unieważnia readiness backendu).

PID/service active bez endpointu nie oznacza ready — `torio serve status` traktuje taki stan jako porażkę
weryfikacji (exit 6).

## Restart

- Service restart nie usuwa sessions/state.
- VM reboot uruchamia potrzebne services dopiero po network/filesystem readiness.
- Gateway unit jest zarządzany natywnymi komendami Hermesa.
- `torio doctor` nie modyfikuje systemu bez explicit repair command.
