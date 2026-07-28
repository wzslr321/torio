<!--
AI-Provenance:
  model: Claude Opus 5
  harness: Claude Code
-->

# ADR-0018: `torio brain export` wychodzi z zakresu V1

- Status: Accepted
- Date: 2026-07-28
- Supersedes: [ADR-0015](0015-torio-v1-onboarding-projects-and-operator-push.md)
  **wyłącznie** w części dotyczącej `export` — wiersz „`torio brain import/export`"
  w mapowaniu komend oraz wzmianka o `export` w sekcji Second Brain. Cała reszta
  ADR-0015, w tym kontrakt `import`, obowiązuje bez zmian.

## Context

`torio brain export` kopiuje working tree Braina przez prywatny staging na
gościu, weryfikuje go manifestem SHA-256 i atomowo tworzy nowy katalog na
hoście. Kod działa i ma testy. Kosztuje około 1 260 linii Go: `Export` plus
pięć funkcji pomocniczych, sześć plików `rename_noreplace_*` implementujących
exclusive rename na trzech platformach, `transfer.Verify`,
`transfer.ParseGuestManifest`, `Manifest.WriteJSON`, `lima.CopyFromGuest`,
komenda CLI, testy i dwie fazy harnessu E2E.

Za te 1 260 linii dostajemy **niepełny backup**. Export V1 jest working-tree-only:
`.git` jest jawnie wykluczane, `brain.bundle` nie powstaje, historia nie wychodzi.
Kontrakt Taska 12 przewidywał bundle „jeśli realny spike dowiedzie bezpiecznego
transportu"; spike tego nie objął, więc został wariant zapasowy — udokumentowany
working-tree-only i „jawny follow-up", który do dziś nie ma taska ani ADR-u.

Komenda o nazwie `export` sugeruje, że dane wyszły. Wyszła ich część, bez historii
zmian, którą Brain prowadzi lokalnym Gitem od `brain init`. To gorsze niż brak
komendy: brak jest widoczny, niepełny backup jest widoczny dopiero przy odtwarzaniu.

Realnym zastosowaniem exportu w V1 była **jednorazowa migracja** vaulta sprzed V1
(`/home/hermes/kb` na starej instancji) do nowej VM `torio` — plan §1.4. Transport,
którego export używa pod spodem, to promowany w Gate 0 `limactl copy`. Operator ma
więc do tej migracji tę samą drogę bez pośrednika.

## Decision

`torio brain export` znika z V1. `torio brain init`, `status` i `import` zostają.

Wyjście danych z VM staje się jawną operacją operatora, udokumentowaną
w runbooku:

```bash
limactl copy <instance>:/home/hermes/brain/ <host-dest>/
```

To ta sama komenda i ten sam kształt argumentów, których używał `CopyFromGuest`.
Migracja vaulta sprzed V1 to ta komenda ze starą instancją jako źródłem,
a następnie `torio brain import <host-dest>`.

Torio nie deklaruje, że ta operacja jest backupem, ani nie weryfikuje jej
manifestem. Deklaracja jest po stronie operatora, tak jak sama operacja.

## Consequences

- **Task 12 nie jest już w pełni dostarczony.** Tracker w `.hermes/plans/`
  zmienia go na import-only. To regres względem zaliczonego taska i tak jest
  zapisany, a nie przemilczany.
- **Harness E2E traci bramkę `import_export_roundtrip_manifests_match`.** Była
  najmocniejszym dowodem poprawności importu w repozytorium: import → export →
  ponowne policzenie manifestu → porównanie digestów. Po zmianie import
  weryfikuje payload checksumem po stronie gościa (`sha256sum -c` na
  promowanym drzewie), ale nikt nie sprawdza round-tripu. To realna utrata
  pokrycia i największy koszt tej decyzji.
- Znikają: exclusive rename na trzech platformach
  (`renameat2 RENAME_NOREPLACE` / `renamex_np RENAME_EXCL`), host-side
  `transfer.Verify`, parser manifestu gościa i `lima.CopyFromGuest`.
  `RENAME_EXCHANGE`, którego używa promocja importu, **zostaje** — to inny
  prymityw, w `internal/brain/transfer.go`.
- `torio brain export <cokolwiek>` kończy się exit 2 jako nieznana subkomenda.
  Nie zostawiamy stubu z komunikatem: komenda, która istnieje tylko po to, żeby
  odmówić, jest gorszym API niż jej brak.
- Get Started (Task 21) musi opisać ścieżkę „mam już vault" przez `limactl copy`
  + `brain import`, a nie przez `brain export`.

## Rejected

- **Zostawić export i dopisać `brain.bundle`.** Zamknęłoby lukę historii, ale
  wymaga własnego spike'u transportu bundle'a na realnym Macu i powiększa
  powierzchnię, którą ta zmiana ma zmniejszyć. Jeśli pełny backup Braina
  kiedykolwiek stanie się wymaganiem produktowym, należy mu się własny ADR
  i spike — nie doklejenie do komendy, która i tak jest niepełna.
- **Zostawić export i tylko przemianować go na `brain copy-out`.** Naprawia
  mylącą nazwę, nie usuwa 1 260 linii utrzymywanego kodu za operację, którą
  `limactl copy` wykonuje jednym poleceniem.
- **Zostawić stub `brain export`, który odsyła do `limactl copy`.** Trwały koszt
  w help, testach kontraktowych i mapowaniu exit code'ów za komunikat, który
  należy do dokumentacji.
- **Zachować bramkę round-tripu, eksportując tylko w E2E.** Wymagałoby trzymania
  całej ścieżki eksportu w produkcyjnej binarce po to, żeby testować import.
  Test, który utrzymuje przy życiu produkcyjną funkcję, przestaje być testem.
