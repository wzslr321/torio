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

## D3 — Lima adapter — V1 adapter (status/start/ssh) pending review; Init/Stop deferred

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
  mountów, `ssh.loadDotSSHPubKeys == false`, dokładny image) oraz `stop` — usunięte z V1, wrócą jako
  osobny slice; sanityzowane pola `mounts`/`ssh` w evidence są zachowane pod ten przyszły slice,
- ~~**CLI wiring**~~ — **DONE, pending review** (2026-07-25): `hb vm status`, `hb vm start`,
  `hb vm ssh -- COMMAND...` wpięte w `internal/cli` przez unexported seam (`app.newLima`), z
  mapowaniem `lima.ErrorKind` → exit code (not_found/ambiguous/postcondition→3; binary/command/
  malformed/version/timeout/cancel→8; remote non-zero → 8) i JSON envelope dla stanu VM.

Poza zakresem (świadomie, dalej): D4 deterministic bootstrap, instalacja Docker/Hermes/Git, cloud-init
bootstrap scripts, `hb doctor`, gateway, serve lifecycle, Hermes adapter, Docker adapter, host mounts,
credentials/provider config.

Następny slice: przywrócenie `init`/`stop` (adapter + CLI), a dopiero po nich D4 — Deterministic
bootstrap. CLI wiring `status`/`start`/`ssh` jest już zrobione (pending review).

## D4 — Deterministic bootstrap

Bootstrap instaluje przypięte dependencies i tworzy directories/services, ale nie przyjmuje sekretów. Musi być re-runnable i wykazywać drift.

Acceptance na świeżej VM:

- arm64 verified,
- Docker/Hermes/Git probes pass,
- repos/state na Linux filesystemie,
- transfer mount narrow,
- żaden token w image/template/logu.

## D5 — Serve lifecycle

Na podstawie spike'a:

- generate/install custom user unit,
- loopback bind,
- endpoint readiness,
- logs/status/restart,
- feature-detected compatibility.

Tests: render golden file, wrong CLI surface, port occupied, process active but endpoint dead.

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
