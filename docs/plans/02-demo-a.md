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

## D3 — Lima adapter

Typed adapter:

- feature/version probe,
- init/start/stop/status/ssh,
- argument arrays, no `sh -c`,
- idempotency,
- timeout/cancellation,
- fake process runner tests.

Provisioning template generowany wyłącznie z trusted embedded/template files.

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
