# Contributing

## Przed zmianą

1. Przeczytaj `AGENTS.md`.
2. Ustal etap z `docs/plans/00-roadmap.md`.
3. Przeczytaj odpowiedni ADR i kontrakt.
4. Sprawdź, czy wymagany spike gate jest zamknięty.

## Branch i commit

- Jedna zmiana zachowania na branch/task.
- Format commitów: `type: krótki opis`.
- Typy: `feat`, `fix`, `test`, `docs`, `refactor`, `chore`, `spike`.
- Nie łącz refaktoru z nową funkcjonalnością.

## Testy

```bash
python3 scripts/validate_artifacts.py
go test ./...             # gdy kod Go już istnieje
go test -race ./...       # przed review zmian współbieżnych/state
```

Każdy nowy behavior wymaga RED → GREEN → REFACTOR. Spike jest wyjątkiem tylko w katalogu `spikes/` i musi być usunięty albo jawnie zatwierdzony do przepisania test-first.

## Review checklist

- Czy zmiana pozostaje w aktualnym scope?
- Czy nie duplikuje Kanbana/dispatchera Hermesa?
- Czy fail closed jest zachowany?
- Czy worker nie dostał nowej capability?
- Czy kandydacki kod nie jest wykonywany na hoście?
- Czy output JSON i exit codes są zgodne z kontraktem?
- Czy test dowodzi enforcementu, a nie tylko konfiguracji?
- Czy nie zapisano credentials, tokens, raw private logs lub danych Braina?

## Zmiany architektury

Dodaj nowy ADR. Nie edytuj historii zaakceptowanego ADR-u tak, aby ukryć poprzednią decyzję.
