<!--
AI-Provenance:
  model: Claude Opus 5
  harness: Claude Code
-->

# ADR-0019: Katalog state i schemat configu V1 wychodzą z V1

- Status: Accepted
- Date: 2026-07-28
- Dotyczy: `internal/config/`, `internal/cli/`, kontrakty
  [`config.md`](../contracts/config.md) i [`cli.md`](../contracts/cli.md)
- Powiązane: [ADR-0013](0013-trusted-config-authority.md) (granica zaufania
  ścieżek — ten ADR jej nie zmienia, tylko odbiera jej jeden obiekt),
  [ADR-0017](0017-pre-v1-exploration-leaves-the-working-tree.md)

## Context

Dwa wejścia, które CLI dziś przyjmuje, nie mają konsumenta.

**Katalog state.** `Paths.StateDir` resolwuje się z `XDG_STATE_HOME` albo
z `--state-dir`, jest walidowany jako katalog zaufany i logowany na poziomie
debug. Na tym kończy się jego rola: żaden plik `.go` nic tam nie zapisuje ani
stamtąd nie czyta. Jedynym planowanym mieszkańcem był manifest
`version-lock.json`, którego nigdy nie podpięto i który zniknął wraz z całą
gałęzią version-lock (ADR-0017). Katalog przeżył swój cel o jeden PR.

Sama pozostałość nie jest neutralna. `config.Load` woła
`statTrustedDirIfExists(paths.StateDir)`, a `Load` wykonuje się
w `PersistentPreRunE` **każdej** komendy. Katalog `~/.local/state/torio`
z bitami grupy — utworzony przez cokolwiek, bo Torio go nie tworzy — kończy
`torio version` błędem exit 2:

```
torio: config: …/state/torio has insecure permissions 0755;
want mode-private (no group/other access)
```

To kontrola bezpieczeństwa katalogu, w którym nie ma nic do ochrony, zdolna
zatrzymać całe CLI. Koszt bez ekspozycji.

**Schemat configu V1.** `schema_version: "1"` to dokument settings-only sprzed
project registry. Czytamy go, normalizując do pustego registry; każdy zapis
i tak emituje V2. Torio nie zostało nigdy wydane, więc dokument V1 nie istnieje
poza fixture'ami testów. Argument zapisany w `config.md` — „starsza binarka
jawnie odrzuca V2" — dotyczy **starej** binarki i obowiązuje niezależnie od
tego, czy ta czyta V1: broni go version gate i `DisallowUnknownFields` po jej
stronie, nie po naszej.

## Decision

Oba wejścia znikają. `--state-dir` i `schema_version: "1"` kończą się exit 2
jako, odpowiednio, nieznana flaga i niewspierana wersja schematu.

Znika cała koncepcja katalogu state: `Options.StateDir`, `Paths.StateDir`,
gałąź `XDG_STATE_HOME` w `ResolvePaths` oraz walidacja `StateDir` w `Load`.
Torio przestaje mieć pojęcie „katalogu state", zamiast mieć puste.

`ConfigSchemaVersion` przestaje być aliasem na najnowszą z dwóch wersji i staje
się jedyną wspieraną.

## Consequences

- **ADR-0013 traci jeden obiekt, nie decyzję.** Polityka (no-follow open, typ,
  mode-private, owned-by-EUID) obowiązuje bez zmian dla `config.json`
  i `ConfigDir`. `StateDir` wypada z tabeli obiektów, bo nie ma już takiego
  obiektu. Nota statusu w ADR-0013 to odnotowuje; sama decyzja zostaje
  nietknięta (`AGENTS.md` §9).
- **Naprawiony defekt.** `torio version` przestaje zależeć od trybu katalogu,
  którego Torio nie tworzy i nie używa.
- **`XDG_STATE_HOME` przestaje mieć na Torio jakikolwiek wpływ.** Dla operatora
  jest to zmiana bez skutku: nic tam nigdy nie powstało.
- **Powrót jest tani.** Gdyby Torio kiedykolwiek potrzebowało trwałego state
  (cache, ledger), przywrócenie resolucji to około piętnastu linii w `paths.go`
  plus wpięcie w politykę ADR-0013 — mniej pracy niż utrzymywanie pustej
  koncepcji do tego czasu.
- **Ręcznie napisany `config.json` z `schema_version: "1"` przestaje działać.**
  Odrzucenie jest jawne (exit 2, komunikat o niewspieranej wersji), nie ciche.
  Naprawa to zmiana jednego znaku i dopisanie `"projects": []`.
- Kontrakty `config.md` i `cli.md` tracą, odpowiednio, sekcję kompatybilności
  V1 ↔ V2 z wierszem `State` w tabeli XDG, oraz `--state-dir` z bloku
  globalnych flag.

## Rejected

- **Zostawić `--state-dir`, usunąć samą walidację w `Load`.** Naprawia defekt
  i nie usuwa niczego więcej, ale zostawia w kontrakcie i w `--help` flagę,
  która nie robi nic — gorsze niż jej brak, bo obiecuje wpływ na zachowanie.
- **Zostawić katalog state jako miejsce na przyszłość.** To ta sama obietnica,
  którą złożył `version-lock.json`. Pusty katalog nie przyspiesza przyszłej
  funkcji; określa ją przedwcześnie.
- **Zostawić odczyt V1 „na wszelki wypadek".** Wypadek wymagałby dokumentu,
  który nigdy nie powstał: Torio nie miało wydania, a każdy zapis od czasu
  wprowadzenia registry emituje V2.
- **Zmigrować V1 → V2 przy odczycie.** Migracja ma sens, gdy istnieją dokumenty
  do zmigrowania. Tutaj dodałaby ścieżkę zapisu do komendy, która deklaruje
  tylko odczyt, żeby obsłużyć pusty zbiór wejść.
