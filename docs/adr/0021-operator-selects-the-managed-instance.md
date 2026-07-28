<!--
AI-Provenance:
  model: Claude Opus 5 (1M context)
  harness: Claude Code
-->

# ADR-0021: Operator wybiera zarządzaną instancję

- Status: Accepted
- Date: 2026-07-28
- Dotyczy: `internal/lima`, `internal/config`, `internal/cli`
- Powiązane: [ADR-0003](0003-lima-trust-boundary.md) (granica zaufania, która
  ustaliła jedną VM), [ADR-0013](0013-trusted-config-authority.md) (granica
  zaufania ścieżek config), [ADR-0015](0015-torio-v1-onboarding-projects-and-operator-push.md)

## Context

`lima.InstanceName` jest stałą `"torio"`. ADR-0003 zapisał to świadomie: jedna
VM aarch64 jest granicą zaufania Demo A, a Torio nie zarządza wieloma nazwanymi
instancjami. Ścieżki gościa są zaszyte tak samo — `/home/hermes/brain`,
`/home/hermes/.hermes`, `/home/hermes/projects`.

Ta decyzja przestała wystarczać w momencie, w którym operator zaczął używać
Torio jako środowiska codziennej pracy. Powstały dwa zastosowania jednej
maszyny, o sprzecznych wymaganiach:

- **codzienna praca** — vault z realnymi notatkami, projekty z realnym kodem,
  wymaganie: nic tego nie rusza;
- **testowanie produktu** — dogfood, spike'i, przebiegi harnessu, wymaganie:
  wolno niszczyć i stawiać od zera.

Kolizja nie jest hipotetyczna i nie dotyczy projektów. Projekty są rozdzielone z
natury: każdy siedzi pod `/home/hermes/projects/<id>`, a `project remove`
archiwizuje wpis i nigdy nie kasuje checkoutu. Kolizja dotyczy **Braina**.
`spikes/v1-e2e/run.sh` wykonuje `brain import` (syntetyczny vault, plik z
faktem, test kolizji) do jedynego vaulta pod stałą ścieżką. Harness nie ma
żadnego sposobu, żeby odróżnić środowisko testowe od produkcyjnego, bo z jego
punktu widzenia istnieje dokładnie jedno.

Obejścia bez zmiany kodu są gorsze niż problem. „Nie uruchamiaj harnessu na
swojej maszynie" jest regułą żyjącą w cudzej głowie, a nie w produkcie —
dokładnie ten kształt, którego AGENTS.md §9 zabrania w kwestiach bezpieczeństwa
i który tak samo źle znosi zmęczenie w kwestiach danych. `limactl delete torio`
przed każdym testem czyni testowanie na tyle drogim, że przestaje się odbywać.

## Decision

**Nazwa instancji przestaje być stałą i staje się wejściem operatora.**

1. `TORIO_INSTANCE` wybiera zarządzaną instancję Limy. Nieustawiona daje
   `torio` — dokładnie dotychczasowe zachowanie, bajt w bajt.

2. **Nazwa instancji wybiera też config.** To jest sedno decyzji, nie dodatek.
   Rejestr projektów jest stanem hosta; gdyby został wspólny, `project list`
   pokazywałby projekty codzienne przy rozmowie z VM-ką testową, a `project add`
   dopisywałby testowy projekt do rejestru produkcyjnego. Rozwiązanie:

   - instancja domyślna → `$XDG_CONFIG_HOME/torio/config.json` (bez zmian),
   - instancja nazwana → `$XDG_CONFIG_HOME/torio/instances/<nazwa>/config.json`,
   - `--config` wygrywa zawsze, bo jest jawnym zaufanym wejściem (ADR-0013).

   Rozdzielenie jest więc **automatyczne**. Operator nie musi pamiętać o drugiej
   fladze, a wariant „ustawiłem instancję, zapomniałem configu" nie istnieje.

3. **Walidacja fail-closed przed jakąkolwiek pracą.** Nazwa musi spełniać tę
   samą regułę co identyfikator projektu: od 1 do 64 znaków, wyłącznie małe
   litery, cyfry i myślniki, zaczyna się i kończy znakiem alfanumerycznym.
   Wzorzec jest w `internal/config` obok tego dla projektów. Nazwa niepoprawna
   kończy się błędem użycia (exit 2), **nigdy** cichym powrotem do domyślnej —
   powrót skierowałby komendę przeznaczoną dla VM-ki testowej na codzienną, czyli
   spowodowałby dokładnie tę awarię, której ten mechanizm ma zapobiegać.
   Wartość trafia do argv `limactl` i do segmentu ścieżki configu; jedno i drugie
   musi być rozstrzygnięte, zanim cokolwiek dotknie dysku albo VM-ki. Komunikat
   błędu podaje regułę i nigdy nie odbija wartości.

4. **Rozstrzygnięcie żyje w `internal/config`**, bo tam jest wstrzykiwany
   `Getenv` i tam już rozwiązywane są zaufane ścieżki. `internal/lima` dostaje
   gotową, zwalidowaną nazwę raz, przy starcie. Dzięki temu żaden test nie musi
   mutować środowiska procesu, żeby sprawdzić to zachowanie.

Zaszyte ścieżki **gościa** się nie zmieniają. Osobna instancja to osobny gość,
więc `/home/hermes/brain` w VM-ce testowej jest z definicji innym katalogiem niż
w produkcyjnej. Uczynienie ich konfigurowalnymi nie dałoby nic poza kolejnym
wymiarem, w którym dwa środowiska mogą się rozjechać.

## Consequences

- Codzienne używanie i testowanie produktu przestają dzielić vault. To jedyny
  powód powstania tego ADR-u i jedyne kryterium, po którym należy go oceniać.
- `TORIO_INSTANCE` **nie jest credentialem** i nie zmienia granicy zaufania z
  ADR-0003. Wybiera, z którą VM-ką rozmawiamy — jest wejściem tej samej klasy co
  flaga CLI, dostępnym dla kogoś, kto i tak może uruchomić `torio` z dowolnymi
  argumentami. Granica zaufania ścieżek configu z ADR-0013 obowiązuje bez zmian,
  bo katalog instancji leży **wewnątrz** już zaufanego katalogu config.
- Literówka w nazwie nie jest cicha przy zapisie, ale jest cicha przy odczycie:
  `TORIO_INSTANCE=torio-tset vm status` odpowie `not_found` zamiast błędu, bo z
  punktu widzenia produktu to poprawne pytanie o nieistniejącą VM-kę. Dopiero
  `vm init` by ją utworzył. Uznajemy to za akceptowalne — alternatywą byłaby
  lista dozwolonych nazw, czyli stan, którego Torio na hoście nie prowadzi
  (ADR-0019).
- ADR-0003 **nie jest tym uchylony**. Nadal obowiązuje jedna VM na jedno
  wywołanie i jedna granica zaufania; zmienia się to, że operator wybiera którą,
  a nie to, że Torio zarządza wieloma naraz.
- Koszt utrzymania: każde nowe miejsce czytające nazwę instancji musi brać ją z
  rozstrzygniętej wartości, nie z literału `"torio"`. Test pilnuje, że w kodzie
  produkcyjnym nie ma zaszytego literału poza wartością domyślną.

## Rejected

- **Flaga `--instance` zamiast zmiennej środowiskowej.** Nazwa instancji dotyczy
  całej sesji pracy, nie pojedynczego wywołania. Flaga wymagałaby powtarzania jej
  przy każdej komendzie, a pominięcie jednej raz kierowałoby komendę do
  niewłaściwej maszyny — czyli dokładnie ta awaria, której ten ADR ma zapobiec.
  Zmienna eksportowana raz w powłoce testowej trzyma się kontekstu, w którym
  operator faktycznie myśli.
- **Wspólny config dla wszystkich instancji.** Odrzucone w punkcie 2: rejestr
  projektów jest stanem hosta i musi iść za instancją, inaczej dwa środowiska
  widzą swoje projekty nawzajem.
- **Konfigurowalne ścieżki gościa.** Nie rozwiązują niczego, czego nie rozwiązuje
  osobna instancja, a mnożą wymiary rozjazdu.
- **Reguła operacyjna zamiast zmiany w kodzie** („nie uruchamiaj harnessu na
  maszynie produkcyjnej"). Reguła bez egzekwowania jest instrukcją, nie granicą.
  Torio istnieje po to, żeby takich reguł nie trzeba było pamiętać.
- **Rejestr znanych instancji na hoście.** Wykryłby literówkę, ale wprowadziłby
  stan hosta, który ADR-0019 świadomie usunął. Cena wyższa niż zysk.
