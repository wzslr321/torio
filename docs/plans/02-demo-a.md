# Etap 1 — Demo A: Remote Hermes Box

## Prerequisite

`docs/spike-results/99-decision.md` musi zawierać `Demo A: GO` i przypięte versions/commands.

## Definition of done

Na czystym Apple Silicon Macu użytkownik może:

1. zainstalować prerequisites,
2. utworzyć/start/stop VM,
3. uruchomić persistent Hermes Brain,
4. połączyć Desktop przez SSH/loopback,
5. wejść IDE przez SSH,
6. zrestartować VM bez utraty Hermes state,
7. uruchomić `hb doctor` i otrzymać prawdziwe probes.

## D1 — CLI skeleton — DONE

Status: **DONE** (2026-07-24). Production `hb` binary buduje się i przechodzi
`go test ./...`, `go vet ./...`. Zrealizowane test-first:

- `hb version` (human) i `hb version --json` — envelope zgodny z [`../contracts/cli.md`](../contracts/cli.md),
- stabilne mapowanie exit codes (tabela z kontraktu, zablokowana testem),
- ścisłe rozdzielenie stdout (machine/human) i stderr (diagnostyka `log/slog`),
- context timeout: walidacja `--timeout` względem policy max + realne
  cancellation/timeout w typed runnerze `internal/execx` (bez `sh -c`),
  z cleanupem całego drzewa procesów (unix; granica platformy jawna),
- bounded + redacted retained child output per stream (deterministyczne flagi
  truncation),
- central redaction utility (`internal/redact`, TM-12) — poprawnie obsługuje
  nakładające się literały (longest-first); redakcja egzekwowana też w finalnym
  rendererze błędów/envelope.

Zaimplementowane globalne flagi w D1: `--json`, `--verbose`, `--timeout`.
`--config` i `--state-dir` są **D2-pending** i w D1 są odrzucane (usage, exit 2)
— patrz [`../contracts/cli.md`](../contracts/cli.md) „Dostępność per slice". `--help`
jest wąskim, udokumentowanym wyjątkiem od reguły `--json` (afordancja dla człowieka).

Zrealizowany layout:

```text
cmd/hb/
internal/cli/
internal/config/
internal/execx/
internal/redact/
```

Inne commandy (`doctor`, `vm`, `serve`, `gateway`, …) NIE są stubowane.

Dispatch komend przez Cobra (przypięte w `go.mod`) — patrz
[ADR-0012](../adr/0012-cobra-cli-framework.md). `internal/cli` pozostaje jedynym miejscem
egzekwującym envelope/exit-codes/redakcję, niezależnie od frameworka.

Następny slice: **D2 — Host/VM config** (nierozpoczęty).

## D2 — Host/VM config — DONE

Status: **DONE** (2026-07-24). Zrealizowane test-first w `internal/config/` i wpięte w
Cobra tree (`internal/cli`). Kontrakt formatu/lokalizacji: [`../contracts/config.md`](../contracts/config.md).

- typed config i default XDG paths — `XDG_CONFIG_HOME`/`XDG_STATE_HOME` z udokumentowanymi
  fallbackami `$HOME/.config` i `$HOME/.local/state`; non-absolutny XDG base odrzucany fail-closed,
- jeden format konfiguracji (`config.json`, JSON ze standardowej biblioteki) z polem
  `schema_version` (const `"1"`); brak nowej zależności,
- schema/semantic validation — nieznane pola odrzucane (`DisallowUnknownFields`), `default_timeout`
  walidowany względem policy max; malformed/unknown/invalid = fail closed,
- no secrets in config — materiał o kształcie sekretu odrzucany bez wycieku (ani human, ani JSON
  error go nie ujawnia); dodatkowo finalny renderer redaguje znane kształty,
- canonical paths + containment — explicit `--config`/`--state-dir` kanonikalizowane; pliki
  lokalizowane wewnątrz zaufanych katalogów przez contained-join (traversal odrzucany strukturalnie),
- egzekwowana granica zaufania ścieżek (no-follow open + `Fstat` z tego samego fd: typ, mode-private,
  owned-by-EUID) na hostach **darwin/linux** (build-tagged `darwin || linux`; jawny no-op poza nimi,
  patrz ADR-0013) oraz crash-safe zapis (temp → fsync → atomic rename, 0600/0700),
- version lock manifest — typowany, schema-versioned, non-secret, ze ścisłą walidacją i round-tripem
  zapis/odczyt; konsumowany przez D3/D4 (patrz kontrakt).

Wpięcie CLI: `--config PATH` i `--state-dir PATH` to realne persistent flagi (przed i po subkomendzie),
resolujące się do typowanej konfiguracji używanej przez wykonanie (`default_timeout` zasila timeout
policy, gdy `--timeout` nie podano jawnie). Zachowana dyscyplina D1: exit mapping, jeden JSON envelope,
rozdział stdout/stderr, redakcja, `--timeout` policy.

Tests: defaults + XDG overrides, `--config`/`--state-dir` przed/po `version`, absent vs explicit config,
malformed JSON/schema/unknown/semantic-invalid, canonical+traversal, trust boundary (darwin/linux:
symlink/type/mode/owner reject),
secret-shaped rejection bez wycieku, version-lock parse/validate + crash-safe round trip, niezmieniony
envelope D1 (drugi decode = `io.EOF`).

Następny slice: **D3 — Lima adapter** (nierozpoczęty). D3–D8/Demo B pozostają nierozpoczęte.

## D3 — Lima adapter — V1 adapter (status/start/ssh) pending review; Stop done (D3.1); Init deferred

Status: **D3-V1 `internal/lima` adapter (probe/status/start/ssh) — pending review** (2026-07-25).
**To NIE jest zamknięcie D3 milestone.** V1 to świadomie najmniejsza użyteczna ścieżka adaptera:
tworzenie instancji (`init`, z zaufanego template) oraz `stop` są **odłożone do późniejszego
slice'a** i nie ma ich w kodzie. Bez CLI wiring operator nie ma jeszcze żadnego sposobu wywołania
adaptera z `hb`. Zrealizowane test-first w `internal/lima/`, wyłącznie przez `execx.Runner`
(typed argv, brak `sh -c`). Discovery oparte o realny, zainstalowany `limactl 2.2.0`; sanityzowane,
read-only evidence w `docs/spike-results/evidence/etap-0d-lima-adapter/` (żadna komenda mutująca nie
została uruchomiona).

Zakres V1 (zaimplementowany):

- feature/version probe (`limactl --version`): parsuje `limactl version <semver>` — walidowana
  gramatyka semver (`MAJOR.MINOR.PATCH` + opcjonalny pre-release/build), nie dowolny `\S+`; sprawdza
  opcjonalny pin `VersionLock.Lima` (przekazywany przez wywołującego jako zwykły parametr — adapter
  nie importuje `internal/config`); rozdziela binary-missing / non-zero exit / malformed output /
  version mismatch / timeout / cancellation,
- status (`limactl list --json --tty=false`): streaming NDJSON decode (realny output to jeden obiekt
  JSON na linię, nie tablica), mapowanie `Running/Stopped/Broken/Unknown` na własny `State`; każdy
  nierozpoznany string statusu jest fail-closed (malformed output); **odmawia parsowania obciętego
  outputu** (`execx` `StdoutTruncated`) i **odrzuca rekordy z pustym `name`/`status`** zamiast
  interpretować je jako „brak VM",
- start (`limactl start <instance> --tty=false`): idempotentny sukces tylko gdy świeżo odpytany stan
  już jest `Running`; brak instancji lub stan niejednoznaczny (`Broken`/`Unknown`) to fail-closed błąd
  bez mutacji; po `start` z exit 0 adapter re-odpytuje status i wymaga dokładnie `Running`, inaczej
  fail-closed `postcondition_failed` — czysty exit code sam w sobie nie wystarcza,
- ssh (`limactl shell --tty=false <instance> -- COMMAND...`): każdy token komendy to osobny element
  argv z jawnym separatorem `--`, więc token wyglądający jak flaga nie może zostać
  zreinterpretowany; czysty non-zero exit zdalnej komendy nie jest błędem adaptera (ten sam kontrakt
  co `execx`),
- każda metoda przyjmuje `context.Context` wywołującego i przekazuje go bez zmian (bez wewnętrznego
  `context.Background()`); fake-runner testy pokrywają timeout/cancellation.

**Odłożone do kolejnych slice'ów (świadomie, poza V1):**

- `init` (create z zaufanego, embedded template + kontrola zgodności istniejącej instancji: brak host
  mountów, `ssh.loadDotSSHPubKeys == false`, dokładny image) — usunięte z V1, wróci jako osobny slice;
  sanityzowane pola `mounts`/`ssh` w evidence są zachowane pod ten przyszły slice,
- ~~`stop`~~ — **DONE, pending review** (2026-07-25): patrz D3.1 niżej,
- ~~**CLI wiring**~~ — **DONE, pending review** (2026-07-25): `hb vm status`, `hb vm start`,
  `hb vm ssh -- COMMAND...` wpięte w `internal/cli` przez unexported seam (`app.newLima`), z
  mapowaniem `lima.ErrorKind` → exit code (not_found/ambiguous/postcondition→3; binary/command/
  malformed/version/timeout/cancel→8; remote non-zero → 8) i JSON envelope dla stanu VM.

Poza zakresem D3-V1 (świadomie): pełny D4 deterministic bootstrap (instalacja przypiętych
Docker/Hermes/Git, cloud-init bootstrap scripts), `hb doctor`, gateway, serve lifecycle, Hermes adapter,
Docker adapter, host mounts, credentials/provider config.

## D3.1 — Lifecycle `stop` + `hb vm bootstrap` (existing target) — pending review

Status: **`internal/lima.Stop`, `internal/lima.Bootstrap` oraz `hb vm stop` / `hb vm bootstrap` —
pending review** (2026-07-25). Cel: **kontrolowany Remote Second Brain V1 path gotowy do użytku przez
operatora na istniejącej VM `hermes-box`; formalne Demo A pozostaje pending.** To nie jest zamknięcie
S1–S8 ani formalne Demo A.

- `Stop` (`limactl stop <instance> --tty=false`): mirror `Start` — idempotentny sukces gdy już
  `Stopped`; brak instancji → `not_found`; `Broken`/`Unknown` → `ambiguous_state` bez mutacji; po `stop`
  z exit 0 re-query wymaga `Stopped`, inaczej `postcondition_failed`. Nigdy `--force`, nigdy nie usuwa
  danych. Wpięte jako `hb vm stop` (exit 3 dla preconditions, 8 dla external).
- `Bootstrap` reconciliuje i weryfikuje **istniejący** target po zweryfikowanym `Running`, przez ten sam
  typed limactl/execx boundary (fixed argv, bez `sh -c`, bez sklejanych stringów, bounded/redacted
  output). Przewidziana tożsamość gościa to dedykowany non-root użytkownik `hermes` (posiada trwałe
  KB/profil pod `/home/hermes`, jest w grupie `docker` — evidence: `etap-0b/s0-target-vm.txt`). Ponieważ
  `limactl shell` loguje jako użytkownik Lima, stabilna ścieżka dociera do `hermes` jawnie przez
  `sudo -u hermes -- hermes …`; goła nazwa `hermes` rozwiązuje się przez stały symlink na secure_path.
  - Reconcile (idempotentny, wąski): membership `hermes` w grupie `docker` (additive `usermod -aG`);
    symlink `/usr/local/bin/hermes` → przypięty launcher, tworzony dopiero po potwierdzeniu, że launcher
    istnieje (brakujący launcher = drift, nie dangling shim).
  - Verify (read-only, fail-closed): `uname -m == aarch64`; `hermes --version` przez stabilną ścieżkę;
    osiągalność serwera Docker dla `hermes`; `git --version`; wymagane ścieżki KB/workspace
    (`/home/hermes/.hermes`, `/home/hermes/projects`) jako katalogi na natywnym ext4; brak host-share
    mountu. Drift/nieweryfikowalny stan → `verification_failed` (exit 6) z remediacją. Nowe
    `ErrorKind`: `not_running` (→3), `verification_failed` (→6).
  - `hb vm bootstrap` emituje jeden envelope z listą udowodnionych checków i trwałymi lokalizacjami
    (home/KB/workspace) jako handoff; dotarcie do Hermesa pozostaje operator-controlled. V1 działa
    unpinned (obserwowane wersje raportowane, by drift był widoczny); enforcement pinu w kolejnym slice.

Operator runbook (start → bootstrap/verify → connect): [../runbooks/remote-second-brain-v1.md](../runbooks/remote-second-brain-v1.md).

Następny slice: przywrócenie `init` (adapter + CLI), a dopiero po nim pełny D4 — Deterministic bootstrap
(instalacja przypiętych dependencies).

## D4 — Deterministic bootstrap

Bootstrap instaluje przypięte dependencies i tworzy directories/services, ale nie przyjmuje sekretów. Musi być re-runnable i wykazywać drift.

Acceptance na świeżej VM:

- arm64 verified,
- Docker/Hermes/Git probes pass,
- repos/state na Linux filesystemie,
- transfer mount narrow,
- żaden token w image/template/logu.

## D5 — Serve lifecycle — persistent loopback Desktop backend (V1) — pending review

Status: **`internal/serve` + `hb serve install|start|stop|restart|status|logs` — pending review**
(2026-07-26). Cel: **persistentny, loopback-only Hermes Desktop backend na istniejącej VM `hermes-box`,
osiągalny z Maca przez operator-controlled SSH tunnel.** To NIE jest formalne Demo A ani żaden claim o
model conversation, credential migration czy gateway.

Zrealizowane (test-first tam, gdzie behavior; live discovery przed renderowaniem unitu):

- **Feature-detected surface** — read-only live discovery `hermes serve --help` (Hermes v0.19.0): loopback
  defaults `--host 127.0.0.1 --port 9119`, `--skip-build`, endpoint `GET /api/status → 200`. Ustalono też,
  że `hermes serve --stop/--status` są niewiarygodne (naiwne dopasowanie procesów) → zarządzanie przez
  systemd, nie przez nie.
- **Generate/install custom user unit** — deterministyczny render (golden file) z pinowanym loopback bindem,
  `HERMES_HOME=/home/hermes/.hermes`, `Restart=always`; `internal/serve` przez typed guest boundary
  (fixed argv, bez `sh -c`, stdin-fed `tee` do zapisu unitu — nowe `execx.Command.Stdin`). Zapewnia
  `linger`. Walidacja `systemd-analyze --user verify` **przed aktywacją**; zapis atomowy
  (staging → verify → rename); idempotentne (`changed:false` przy re-run).
- **Loopback bind** — bind pinowany w unicie i we wszystkich probach; nigdy `0.0.0.0` (locked golden +
  invariant test).
- **Endpoint readiness** — `start`/`restart`/`status` dowodzą stanu systemd **oraz** `GET /api/status == 200`
  przez loopback; aktywny proces z martwym endpointem to fail-closed (exit 6). `start`/`restart` re-query
  postcondition; `stop` idempotentne z re-query.
- **logs/status/restart** — `logs` bounded/redagowane, unit-scoped (`journalctl --user -u`); `status`
  exit 0 tylko gdy ready.

Tests: render golden file; invalid unit rejected przed aktywacją (wrong CLI surface → `systemd-analyze`
fail, exit 6); process active but endpoint dead (fail-closed exit 6, jednostkowo i live przez SIGSTOP);
idempotencja install/start/stop; linger ensure; transport/timeout classification; CLI envelope + exit-code
mapping. Live V1 proof (real install→start→status, host SSH tunnel + host curl, negative case, stop/restart,
final Running): `docs/spike-results/evidence/d5-serve-liveproof-*`.

Operator runbook (start VM → bootstrap → serve install/start/status → tunnel → Desktop):
[../runbooks/remote-second-brain-v1.md](../runbooks/remote-second-brain-v1.md).

Świadomie poza D5 (human confirmation / późniejsze slice'y): faktyczny Desktop chat/provider credentials,
KB/second-brain migration, non-loopback bind, arbitrary bind host/port, `hb gateway`, doctor, vm init,
workers, formalne Demo A/S1–S8.

## D6 — Gateway wrapper

Deleguj do natywnych commands Hermesa. Nie generuj własnego gateway unitu.

Tests: exact argv, status mapping, timeout, redacted failure.

## D7 — Doctor

Probes:

- host arch/macOS prereqs,
- Lima version/VM state,
- VM arch/filesystem,
- Hermes command surface/version,
- Docker health,
- serve port/endpoint,
- gateway status,
- XDG permissions,
- transfer mount scope.

Każdy check ma stable ID, severity i remediation. `doctor` nie naprawia automatycznie.

## D8 — End-to-end acceptance

Uruchom na realnym Macu/VM:

- clean init,
- Desktop live chat,
- session persistence,
- VM reboot,
- service reconnect,
- IDE SSH,
- negative external/non-loopback access test,
- no broad host mount.

Zapisz evidence i dopiero wtedy `Demo A: PASS`.
