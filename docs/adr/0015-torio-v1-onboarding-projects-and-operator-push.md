<!--
AI-Provenance:
  model: Cursor Grok 4.5
  harness: Cursor
  skills:
    - mark-ai-provenance
-->

# ADR-0015: Torio V1 — onboarding, Second Brain, multi-project i operator-only push

- Status: Accepted
- Date: 2026-07-27
- Supersedes: ograniczenie produktowe Torio V0 / konsekwencja [ADR-0014](0014-rename-to-torio.md)
  „istniejąca VM only” (Torio nigdy nie tworzy ani nie niszczy instancji `torio`); mylenie
  Knowledge Base / Second Brain z `$HERMES_HOME` (`/home/hermes/.hermes`). ADR-y 0001–0014
  pozostają nietknięte jako historia — ten ADR ich nie przepisuje.

## Context

Torio V0 jest wąskim, w pełni operatorskim produktem: Remote Second Brain na **już istniejącej**
VM Lima `torio` oraz dokładnie jeden hardcoded coding workspace. Operator buduje Torio ze źródeł,
ręcznie provisionuje VM i credentials, a push jest całkowicie poza CLI.

To wystarczyło na dogfood, ale nie na presentation-ready onboarding: nowy użytkownik Apple Silicon
Mac nie może wejść na `torio.dev`, zainstalować binarkę, utworzyć VM, założyć prywatny Markdown
Brain, podpiąć dowolne repozytoria i wypchnąć commit bez znajomości wewnętrznej architektury.

Równolegle w kodzie i docs nadal miesza się profil Hermesa (`/home/hermes/.hermes`) z vaultem
wiedzy — `HermesKBPath` wskazuje na katalog stanu aplikacji. Legacy `schemas/project.schema.json`
oraz platformowe plany (workerzy, admission, verifier) kuszą do reaktywacji superseded roadmapy.
Potrzebny jest jeden aktywny ADR zakresu V1, który rozstrzyga granicę produktu bez przywracania
tej platformy.

Plan implementacji: [`.hermes/plans/2026-07-27_131723-torio-v1-presentation-ready.md`](../../.hermes/plans/2026-07-27_131723-torio-v1-presentation-ready.md).

## Decision

### Host i instalowalność

- V1 wspiera **wyłącznie** macOS na Apple Silicon. Intel Mac, Linux i Windows jako host są poza
  zakresem.
- Torio **MAY** tworzyć nową VM Lima o nazwie `torio` (`torio vm init`) według pinowanego,
  zweryfikowanego template’u. Nie recreate/reset/delete niezgodnej istniejącej instancji; brak
  `--force` i destrukcyjnego recovery w V1.
- Użytkownik otrzymuje release asset macOS arm64 oraz zweryfikowany installer; budowa ze źródeł
  nie jest wymagana do Get Started.

### Second Brain

- Second Brain jest **wymaganym** elementem produktu V1, nie aliasem dla `$HERMES_HOME`.
- Canonical vault path: `/home/hermes/brain` na natywnym filesystemie gościa.
- `/home/hermes/.hermes` pozostaje wyłącznie profilem i aplikacyjnym stanem Hermesa
  (`HermesProfilePath`). Kod i docs MUSZĄ rozróżniać te ścieżki.
- Brain jest filesystem-first, Markdown-first i prywatny. V1 **NIE** wprowadza cloud sync,
  embeddings, vector DB ani automatycznego connector ingestion.
- Cross-project context działa przez globalny retrieval skill (`torio-brain`) i jawne
  file/search retrieval. **Zakazane:** bulk injection całego vaulta do promptu oraz dodawanie
  `/home/hermes/brain` jako folderu każdego Hermes Project.
- Second Brain pozostaje osobnym Hermes Project do bezpośredniej pracy (browse/edit).
- `torio brain init/status/import/export`: scaffold albo bezpieczny transfer istniejącego drzewa.
  Transport jest jednorazowy i ograniczony — **bez** broad host mountu. Treść payloadu **NIE**
  trafia do stdout, logów ani evidence (tylko bounded counts, bytes, manifest hash).

### Multi-project registry

- Aktywny project registry to `config.json` **schema V2** (niesekretne: `id`, `display_name`,
  `remote`). `schemas/project.schema.json` pozostaje **legacy** i nie jest używany przez V1.
- Workspace path jest zawsze wyprowadzany jako `/home/hermes/projects/<project-id>`. Użytkownik
  **NIE** podaje arbitralnej ścieżki.
- Git remote **NIE MOŻE** zawierać hasła, tokenu, query/fragment ani innego embedded credential.
  Niesekretny SSH username (np. `git@`) jest dozwolony.
- `torio project add` klonuje albo bezpiecznie adoptuje zgodny guest checkout i rejestruje projekt
  przez publiczne `hermes project` CLI.
- `torio project remove` zapomina/archiwizuje wpis (i Hermes project), ale **NIE** usuwa checkoutu.
  Brak `--delete` w V1.

### Operator-only push

- Persistentny guest (tożsamość serwisowa `hermes`) ma wyłącznie **read** access do origin.
- Write capability pochodzi wyłącznie z krótkotrwałej, interaktywnej sesji operatora:
  `torio project shell <id>` → zwykłe komendy Git → `exit`.
- Operator shell używa oddzielnej login identity oraz ephemeral SSH agent forwarding
  (`ssh -A` na sesję). Globalne `ssh.forwardAgent: true` w Lima template jest **zakazane**.
  Hermes service identity **NIE** dziedziczy `SSH_AUTH_SOCK`.
- Torio **NIE** przechowuje tokenów/kluczy Git write i **NIE** automatyzuje push/merge/release.

### Poza V1 (świadomie)

- Per-task workerzy, dispatcher, queue, fresh verifier platform i stary legacy project schema.
- Automatyczny agent push/merge/deploy/release.
- Import hostowego checkoutu albo szeroki mount katalogu macOS.
- Keep/Discard per agent turn w Hermes Desktop — timeboxed upstream enhancement; **nie** blokuje
  Torio V1. Osobny, mały upstream fix fałszywego sukcesu Revert MAY iść równolegle.

### Mapowanie komend V1 → decyzje

| Komenda | Decyzja |
| --- | --- |
| `torio vm init/start/bootstrap` | Fresh VM create + verify; native FS; profile ≠ brain |
| `torio brain init/status` | Canonical `/home/hermes/brain`; scaffold; bounded status |
| `torio brain import/export` | One-shot transport; no content in logs; all-or-nothing |
| `torio project add/list/show/use/remove` | Config V2; derived workspace path; non-destructive remove |
| `torio project shell` | Ephemeral operator write; no persistent agent credentials |

## Consequences

- Implementacja V1 postępuje według planu presentation-ready; README i wygenerowane runbooki
  opisują V0 **do** finalnego release tasku — podczas pracy status produktu pozostaje
  pre-release względem V1.
- `internal/lima` zyskuje typed `Init` i template bez broad mounts; bootstrap rozróżnia
  `HermesProfilePath` i `HermesBrainPath`.
- Hostowy CLI zyskuje `brain` i `project` surface oraz osobny interactive runner (nie
  captured `execx.Runner`).
- Spike’e Gate 0 (fresh onboarding, operator shell, brain transfer) MUSZĄ dostarczyć realne
  evidence na Apple Silicon Mac przed produkcyjnym kodem zależnym od pinów/transportu.
- Jeśli macierz izolacji agent forwarding nie da się spełnić bez kruchego hacku, wymagany jest
  superseding ADR dla host-side `torio project push` — nie „pozornie bezpieczny” shell.
- Legacy platforma (sekcje 4–5 `AGENTS.md`, stare plany/kontrakty) nadal **NIE** wraca do
  implementacji.

## Rejected

- Reaktywacja worker/admission/verifier roadmapy jako „V1”.
- Traktowanie `/home/hermes/.hermes` jako Second Brain.
- Bulk injection vaulta do system promptu każdego projektu.
- Persistent Lima `forwardAgent` albo współdzielony `SSH_AUTH_SOCK` z procesem `hermes`.
- Zapisywanie workspace path albo credentials w config.
- `project remove --delete` checkoutu w V1.
- Broad host mount jako transport Braina.
- Uznanie Keep/Discard za blocker release Torio V1.
