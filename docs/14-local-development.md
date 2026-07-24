# Local development setup

## Host

Primary development host to macOS Apple Silicon. Dokumenty i unit tests Go mogą działać również na Linuxie; real Demo A wymaga macOS + Lima + Desktop.

## Prerequisites

```bash
brew install go lima git
```

Go toolchain jest przypięty przez `go.mod`/`.tool-versions`. Nie instaluj Docker Desktop jako wymagania projektu; Docker Engine działa wewnątrz Lima VM zgodnie ze zweryfikowanym bootstrapem.

Hermes Agent instaluj wyłącznie z aktualnej oficjalnej dokumentacji. Nie kopiuj komendy instalacyjnej z pamięci modelu.

## Pierwsze kroki

```bash
cd hermes-box
python3 scripts/validate_artifacts.py
go version
go test ./...
```

Kod produkcyjny wystartował po Demo A GO. Pierwszy slice (D1 — CLI skeleton) jest w `cmd/hb/` i `internal/{cli,config,execx,redact}/`; `go test ./...` uruchamia jego pakiety.

## LLM workflow

1. Uruchom coding agenta w root repo.
2. Użyj `prompts/00-implementer-system.md` jako project context.
3. Pierwsza sesja: `prompts/01-spike.md`.
4. Po review GO: jedna sesja na jeden slice z promptu Demo A/B.
5. Po każdym slice użyj `prompts/04-code-review.md`.
6. Dla boundary changes dodatkowo `prompts/05-security-review.md`.
7. Kończ sesję `prompts/06-handoff.md`.

## Secret hygiene

- Nie zapisuj provider tokens do repo ani template.
- W przykładach używaj `[REDACTED]` lub canary.
- Provisioning secrets jest interaktywną operacją po bootstrapie.
- Przed commit uruchom validator i lokalny secret scanner, jeśli jest dostępny.

## Shared folder

Transfer folder służy wyłącznie jawnemu handoffowi plików. Repozytoria, `.git`, `hb.db`, Hermes profile i credentials pozostają na Linux filesystemie VM.
