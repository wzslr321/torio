# ADR-0012: Cobra jako framework CLI dla `hb`

- Status: Accepted
- Date: 2026-07-24
- Amends: [ADR-0001](0001-go-control-plane.md) (utrzymany; ta decyzja doprecyzowuje jego preferencję „stdlib przed zależnościami” dla warstwy CLI)

## Context

ADR-0001 wybrał Go i preferencję standard library przed zależnościami, dopuszczając
zależności „bez realnej potrzeby, uzasadnienia i pinningu” — czyli za uzasadnieniem i z pinningiem.

Pierwsza implementacja dispatchu CLI (slice D1) ręcznie parsowała flagi i rozgałęział komendy przez
`switch` po surowych stringach, z dwukrotnym `flag.Parse`, aby obsłużyć flagi przed i po nazwie
komendy. To rozwiązanie:

- nie skaluje się do drzewa komend z [`../contracts/cli.md`](../contracts/cli.md)
  (`vm`, `serve`, `gateway`, `project`, `task`, `admin` z pod-komendami i per-command flagami),
- łamie się przy pozycyjnych argumentach pod-komend (np. `hb vm ssh -- COMMAND...`),
- powiela logikę, którą dojrzałe frameworki mają przetestowaną (help, sugestie, spójne błędy).

## Decision

Warstwa CLI `hb` używa **Cobra** (`github.com/spf13/cobra`) do budowy drzewa komend, parsowania
per-command flag i generowania help. Zależność jest **przypięta** w `go.mod`/`go.sum`:

```text
github.com/spf13/cobra v1.10.2
github.com/spf13/pflag v1.0.9            (tranzytywnie)
github.com/inconshreveable/mousetrap v1.1.0 (tranzytywnie)
```

Granice, które **pozostają własnością** pakietu `internal/cli` (nie Cobry), aby kontrakt był
niezależny od frameworka:

- stabilny JSON envelope (`docs/contracts/cli.md`),
- mapowanie exit codes 0–9 (Cobra ma `SilenceErrors`/`SilenceUsage`; błędy renderuje nasz kod),
- rozdzielenie stdout (machine/human) i stderr (diagnostyka `log/slog`),
- redakcja (`internal/redact`),
- walidacja timeoutu względem policy max (`internal/config`).

Reszta preferencji ADR-0001 obowiązuje bez zmian: stdlib-first dla logiki control-plane, typed
adapters dla Git/Hermes/Docker/Lima, `context`, `os/exec.CommandContext`, brak `sh -c`.

## Consequences

- Dispatch to deklaratywne drzewo komend zamiast `switch` po stringach; interleaving flag globalnych
  (persistent flags) działa natywnie.
- Nowa zewnętrzna zależność w TCB build-time — przypięta i o wąskim zakresie (parser CLI, nie runtime
  bezpieczeństwa). Aktualizacje wersji wymagają świadomego bumpa `go.mod` + `go.sum`.
- `internal/cli` nadal jest jedynym miejscem egzekwującym envelope/exit/redaction, więc zmiana lub
  usunięcie frameworka nie narusza kontraktu CLI.

## Rejected

- **Ręczny dispatch stdlib** (command registry z własnym `flag.FlagSet` per komenda): brak nowej
  zależności, ale reimplementacja tego, co Cobra ma sprawdzone (help, nested subcommands, sugestie);
  większy koszt utrzymania przy pełnym drzewie z `cli.md`.
- **urfave/cli v3**: równoważny funkcjonalnie, mniejsza baza użytkowników w narzędziach klasy
  kubectl/docker/gh; ten sam koszt „nowej zależności” bez przewagi nad Cobrą.
