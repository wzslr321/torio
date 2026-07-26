# Handoff — Demo A / D1 (CLI skeleton)

> **ARCHIVAL NOTICE (superseded — historical only).** This handoff belongs to the
> pre-V0 Demo A exploration and is **not** a live next-task instruction. The
> current product is **Torio V0**; its only active documentation is
> [`README.md`](README.md) and the two runbooks under
> [`docs/runbooks/`](docs/runbooks/). Do not act on the "Exact next task" or
> gate-status sections below — see [`docs/legacy-architecture.md`](docs/legacy-architecture.md).
> The historical content below is preserved unchanged as evidence.

- Data: 2026-07-24
- Sesja: pierwszy pionowy slice Demo A — **D1 (CLI skeleton)**. Bez D2–D8, bez Demo B.
- Wykonawca: Claude Code (Opus 4.8) w imieniu wzslr321
- Adresat: LLM-głowa projektu (orchestrator). `AGENTS.md` pozostaje nadrzędny.

> **Historyczny nakaz S2 jest nieaktualny.** Poprzedni handoff kazał „dokończyć korektę
> drivera/evidence S2 w tym samym PR (#2) i przejść re-review”. To zostało zrobione: PR #2 jest
> zmergowany do `main` (`e0c0baa`), a S2 jest **PASS (rev-9, pełna macierz 22/22)**. Nie wracaj do
> driverów/harnessu S2 — jest zamknięte dla user-service scope.

## Gate status (bieżący)

```text
S0-HOST:               PASS
S0-TARGET-VM:          PASS
S2:                    PASS   (native gateway systemd user-service lifecycle; rev-9 22/22)
Etap 0:                INCOMPLETE  (S1, S3–S8 nadal UNKNOWN dla swoich scope)
Demo A — MVP/manual:   GO TO EXECUTE supervised smoke  (prerequisite spełniony)
  └─ D1 CLI skeleton:  DONE
Demo B native Docker:  NO-GO
```

Demo A jako całość **nie jest** PASS — wykonany jest wyłącznie slice D1. Reszta (D2–D8) i acceptance
end-to-end pozostają otwarte.

## Co dostarczono w tej sesji (D1)

Produkcyjny binary `hb` (`go build -o ./bin/hb ./cmd/hb`), TDD (RED→GREEN dla każdego zachowania):

- `hb version` (human na stdout) i `hb version --json` — dokładnie jeden JSON envelope zgodny z
  [`docs/contracts/cli.md`](docs/contracts/cli.md) (`schema_version:"1"`, `ok`, `command`, `data`,
  `warnings:[]`, `error:null`).
- Stabilne mapowanie exit codes (tabela 0–9 z kontraktu, zablokowane testem; `1` zarezerwowane dla
  uncategorized internal, poza tabelą kontraktu).
- Ścisłe rozdzielenie stdout (machine/human) i stderr (diagnostyka `log/slog`); stdout w trybie
  `--json` pozostaje czystym JSON-em nawet z `--verbose`.
- `--timeout` walidowany względem policy max (`internal/config`); realne timeout/cancellation w typed
  runnerze `internal/execx` (argument arrays, `os/exec.CommandContext`, bez `sh -c`, redacted
  diagnostics).
- Central redaction utility (`internal/redact`, TM-12) — znane kształty sekretów + zarejestrowane
  literały → `[REDACTED]`.

Layout: `cmd/hb/`, `internal/cli/`, `internal/config/`, `internal/execx/`, `internal/redact/`.
Żadnych empty stubów dla `doctor`/`vm`/`serve`/`gateway`/D2–D8/Demo B.

Dispatch przez **Cobra** (`github.com/spf13/cobra v1.10.2`, przypięte) — nowy
[ADR-0012](docs/adr/0012-cobra-cli-framework.md) doprecyzowuje preferencję stdlib z ADR-0001 dla
warstwy CLI. `internal/cli` nadal jest jedynym miejscem egzekwującym envelope/exit-codes/redakcję.

## Weryfikacja (rzeczywiste komendy, exit 0 chyba że zaznaczono)

```text
gofmt -l <pliki>              → czysto
go test ./...                → ok (cli, config, execx, redact); cmd/hb bez testów
go test -race -count=1 ./...  → ok
go vet ./...                 → ok
go build -o ./bin/hb ./cmd/hb → ok  (bin/ jest gitignored)
./bin/hb version             → exit 0
./bin/hb version --json      → exit 0, jeden JSON document
./bin/hb frobnicate          → exit 2  (usage; --json → error envelope)
./bin/hb version --timeout 24h → exit 2 (przekroczony policy max)
python3 scripts/validate_artifacts.py → exit 0
git diff --check             → exit 0
```

## Security impact

- Brak nowej powierzchni sieciowej ani żadnej egzekucji zewnętrznej komendy w D1 (`version` niczego
  nie uruchamia).
- Boundary do przyszłych external commands (`internal/execx`) wymusza argument arrays + context +
  timeout + redakcję; nie ma `sh -c` ani `--force`/policy-bypass.
- Redakcja i JSON-envelope invariant są pokryte testami (sekrety nigdy nie trafiają do output/logów).

## Exact next task

**D2 — Host/VM config.** Nic więcej w następnej sesji. Zakres D2 z
[`docs/plans/02-demo-a.md`](docs/plans/02-demo-a.md): typed config + default XDG paths, schema/semantic
validation, brak sekretów w configu, canonical paths, version lock manifest; testy dla
invalid/unknown fields, path traversal, insecure permissions i redakcji. **Nie** implementować D2 w tej
sesji — to handoff, nie start D2.

## Files to read first (następna sesja)

1. `AGENTS.md` (nadrzędny kontrakt)
2. `docs/plans/02-demo-a.md` (D1 DONE; zakres D2)
3. `docs/contracts/cli.md` (envelope, exit codes, global flags)
4. `docs/spike-results/99-decision.md` (bramki; Demo A GO)
5. `docs/adr/0001-go-control-plane.md` (Go stdlib-first, typed adapters)
