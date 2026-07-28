<!--
AI-Provenance:
  model: Claude Opus 5
  harness: Claude Code
-->

# ADR-0020: Dokumentacja produktowa opisuje dostarczoną binarkę

- Status: Accepted
- Date: 2026-07-28
- Dotyczy: `README.md`, `docs/content/`, `docs/runbooks/`, `site/`, `AGENTS.md` §Status produktu
- Powiązane: [ADR-0015](0015-torio-v1-onboarding-projects-and-operator-push.md)
  (zakres, który dokumentacja ma opisywać),
  [ADR-0016](0016-normative-documents-are-corrected-not-archived.md) (reguła,
  którą ten ADR stosuje do runbooków),
  [ADR-0017](0017-pre-v1-exploration-leaves-the-working-tree.md),
  [ADR-0018](0018-brain-export-leaves-v1.md)

## Context

Trzy rundy porządkowe (#43–#48) czyściły kod i kontrakty. Dokumentacja
produktowa — README, strona, runbooki — nie została w nich dotknięta ani razu
i rozjechała się z binarką dalej niż cokolwiek, co tamte rundy wycięły.

`README.md` twierdzi, że Torio „never creates, re-images, or destroys" VM
i obsługuje „exactly one hardcoded workspace". Binarka tworzy VM z pinowanego
template'u (`torio vm init`) i prowadzi rejestr projektów
(`torio project add|list|show|use|remove|shell`). O Second Brainie README nie
wspomina wcale, mimo że `torio brain --help` nazywa go obowiązkowym.

Strona przeczy sama sobie w jednym buildzie: `pages/index.md` mówi „Torio
creates the Linux VM", `pages/explanation.md` — „operates on an **existing**
Lima VM and never creates". Strona główna opisuje też „Messaging gateway" jako
„a service Torio installs and watches"; nie ma komendy `torio gateway`
i nie ma niczego, co instalowałoby albo obserwowało gateway.

Kroki 6–8 tutoriala uczą ręcznego credential preflightu, ręcznego `git clone`
przez `torio vm ssh` i ręcznego `hermes project create` na jednym, wpisanym na
sztywno prywatnym repozytorium. `torio project add <name> <remote>` robi
wszystkie trzy rzeczy — z wyprowadzaną ścieżką workspace, fail-closed exit 7,
gdy gość nie umie czytać remote'u, i rejestracją w Hermesie przed zapisem do
configu. Instrukcja dla człowieka daje słabsze gwarancje niż komenda, która ją
zastąpiła.

Ten stan nie jest zaniedbaniem. `AGENTS.md` §Status produktu zapisał go
świadomie: „`README.md` **NIE** jest przepisywany na V1 przed finalnym release
taskiem", a rozjazd README ↔ V1 nazwał **oczekiwanym** w trakcie implementacji.
Odroczenie miało termin. Ten ADR jest tym terminem.

## Decision

Powierzchnia produktowa opisuje dostarczoną binarkę. Obejmuje to `README.md`,
`docs/content/` wraz z generowanymi `site/` i `docs/runbooks/`, oraz każdy
string widoczny dla operatora w CLI.

**Runbook Code V0 jest poprawiany, nie archiwizowany** ([ADR-0016](0016-normative-documents-are-corrected-not-archived.md)).
Jego treść — preflight, klon, rejestracja — zwija się do jednej komendy, więc
poprawka nie zostawia dokumentu do utrzymania. Materiał wtapia się w jeden
runbook pierwszego uruchomienia `docs/runbooks/first-run.md`:
vm → serve → tunel → token → brain → project → Desktop. Nie jest to
archiwizacja: nic nie trafia pod tag, bo nic nie jest zapisem przebiegu.

**Etykiety wersji schodzą z powierzchni użytkownika.** „V0", „V1" i pochodne
znikają z README, strony, runbooków i stringów CLI. Operator dowiaduje się,
co uruchamia, z `torio version` i tylko stamtąd.

**ADR-y, `docs/contracts/`, `AGENTS.md` §1–9 i `docs/03-architecture.md`
zachowują etykiety.** Tam wersja jest przedmiotem zapisu, a nie ozdobą:
ADR-0015 nazywa się „Torio V1 onboarding…", a zdanie „tego nie ma w V1" niesie
inną informację niż „tego nie ma".

**Konkretne prywatne repozytorium przestaje być wpisane w dokumentację.**
`torio project add` przyjmuje remote jako argument, więc żaden konkretny remote
nie jest już częścią produktu. Publiczna strona nie ma powodu nazywać czyjegoś
prywatnego repozytorium.

**`AGENTS.md` §Status produktu traci klauzulę odraczającą.** Reguła, która
dopuszczała rozjazd, znika razem z rozjazdem — inaczej zostaje w mocy przeciw
nowemu README.

## Consequences

- **Zakres V1 nie zmienia się.** Rozstrzyga go ADR-0015; ten ADR nie dodaje ani
  nie odbiera żadnej funkcji. Zmienia się wyłącznie to, co dokumentacja o nich
  mówi.
- **Znika jeden runbook.** Powierzchnia operacyjna to `README.md` plus
  `docs/runbooks/first-run.md`. Link do usuniętego runbooka zawiedzie —
  `validate_artifacts.py` sprawdza linki względne, więc każdy pozostały
  odsyłacz jest błędem bramki, nie cichą regresją.
- **Bloki opisujące ręczną procedurę znikają** (`workspace-preflight`,
  `workspace-clone`, `workspace-project`, `workspace-facts`). Ich inwarianty —
  credential-neutral control plane, brak host-copy seed, non-destructive gate,
  human-only writes — **nie** znikają: należą teraz do `project add`, które je
  wymusza, a nie do instrukcji, która o nie prosiła.
- **Bramka walidacji rośnie o dwa checki**, żeby to nie wróciło: ścieżki
  `docs/**` cytowane z Go muszą istnieć, a powierzchnia użytkownika nie może
  zawierać etykiet wersji. Pierwszy złapałby sześć martwych referencji do
  `docs/contracts/service-lifecycle.md`, które przeżyły ADR-0017 o trzy PR-y.
- **Historia pozostaje dostępna.** Runbook Code V0 i poprzednie README są
  w historii Gita; pre-V1 eksploracja pod tagiem `archive/pre-v1`.

## Rejected

- **Zostawić runbook Code V0 ze statusem „superseded".** ADR-0016 nazywa
  rozjazd dokumentu normatywnego z zachowaniem defektem do naprawy, nie
  zapisem do zachowania. Nagłówek „superseded" nie przeszkadza czytelnikowi
  wykonać kroków, które pod nim stoją — a te kroki opisują słabszą ścieżkę niż
  komenda, którą produkt dostarcza.
- **Przepisać runbook Code V0 na generyczny runbook projektów.** Zostawia dwa
  dokumenty tam, gdzie treść drugiego to jedna komenda i jej inwarianty. Te
  mieszczą się w runbooku pierwszego uruchomienia bez rozcieńczania go.
- **Wyciąć etykiety wersji także z ADR-ów i kontraktów.** Zniszczyłoby zapisy,
  których przedmiotem jest zakres wersji, i wymagało superseding ADR-ów dla
  zmiany czysto kosmetycznej. `AGENTS.md` §9 zakazuje cichego przepisywania
  decyzji.
- **Zostawić prywatny remote jako realny przykład.** Nazwa repozytorium nie
  jest sekretem, ale publikowanie jej w dokumentacji przeznaczonej na
  `torio.dev` ujawnia bez potrzeby, nad czym pracuje operator, i trafia do
  indeksów wyszukiwarek. Przykładowy remote niesie tę samą wartość dydaktyczną.
- **Odłożyć przepisanie do „finalnego release taska".** To jest ten task.
  Odroczenie bez terminu przestało być odroczeniem — dokumentacja publiczna
  uczyła w tym czasie procedury, której produkt nie realizuje.
