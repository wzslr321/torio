# Config contract (D2)

Ten dokument opisuje typowaną konfigurację hosta oraz version-lock manifest wprowadzone w D2
(patrz `archive/pre-v1:docs/plans/02-demo-a.md` § D2). Implementacja: `internal/config/`.
Konfiguracja jest **non-secret** (AGENTS §6): materiał o kształcie sekretu jest odrzucany.

## Lokalizacje (XDG)

Ścieżki resolują się deterministycznie z zmiennych XDG, z udokumentowanymi fallbackami:

| Rola | Baza (env) | Fallback | Katalog aplikacji |
|---|---|---|---|
| Config | `XDG_CONFIG_HOME` | `$HOME/.config` | `<base>/hermes-box/` |
| State  | `XDG_STATE_HOME`  | `$HOME/.local/state` | `<base>/hermes-box/` |

Reguły:

- Ustawiony, ale **non-absolutny** `XDG_CONFIG_HOME`/`XDG_STATE_HOME` jest odrzucany fail-closed
  (nie jest po cichu ignorowany ani "naprawiany").
- Gdy XDG base jest nieustawiony i nie można ustalić `$HOME`, resolucja kończy się błędem
  (fail closed) zamiast zgadywać lokalizację.
- Fallback `$HOME` musi być **absolutny**. Non-absolutny `$HOME` (przy nieustawionym XDG) jest
  odrzucany fail-closed — nie jest kanonikalizowany względem CWD — z tego samego powodu co
  non-absolutny XDG base: katalog roboczy nie może wyznaczać domyślnej zaufanej lokalizacji
  config/state.
- Flagi `--config PATH` i `--state-dir PATH` nadpisują wartości i są kanonikalizowane
  (`filepath.Abs` + `Clean`, bez rozwijania symlinków — explicit override to zaufany input).
- **Lazy resolution / precedence:** każda lokalizacja jest resolowana niezależnie. Explicit
  override (`--config`/`--state-dir`) całkowicie **omija** odpowiedni XDG base — nieużywana (nawet
  malformed) zmienna XDG ani `$HOME` nie mogą zablokować w pełni jawnej inwokacji. Konsultowany jest
  wyłącznie base faktycznie potrzebny (bo jego override jest nieobecny), i każda faktycznie użyta
  wartość jest nadal ściśle walidowana (patrz reguła absolutności powyżej).
- Przy explicit `--config`, zaufanym katalogiem config (dla `version-lock.json`) jest katalog
  nadrzędny wskazanego pliku — manifest leży obok jawnego configu.
- Pliki lokalizowane **wewnątrz** zaufanego katalogu (`config.json`, `version-lock.json`) używają
  contained-join: nazwa musi być czystą nazwą pliku, a wynik nie może opuścić katalogu bazowego.
  Traversal jest odrzucany strukturalnie, nie przez czyszczenie stringów.

## Granica zaufania ścieżek (ADR-0013)

Zanim `config.json`/`version-lock.json` staną się authority, ścieżki są egzekwowane fail-closed.
Terminologia jest rozdzielona (koniec z „owner-only"):

- **mode-private** — brak dostępu grupy/innych: `perm & 0o077 == 0`.
- **owned-by-EUID** — właściciel obiektu to efektywny użytkownik procesu: `st_uid == geteuid()`
  (ścisła równość; jako root oczekiwany jest obiekt root-owned).

Reguły (egzekwowane na **darwin/linux**; poza nimi jawny, udokumentowany no-op — hosty Demo A to
macOS/Linux arm64):

- Zaufane pliki (`config.json`, `version-lock.json`, także explicit `--config`) są otwierane
  **no-follow** (`O_NOFOLLOW`): symlink w ostatnim komponencie jest odrzucany. Typ, tryb i własność są
  weryfikowane przez `Fstat` **na tym samym deskryptorze**, z którego następuje odczyt — brak TOCTOU
  na ostatnim komponencie (`Lstat`+`ReadFile` jest niedozwolone). Plik musi być zwykły, mode-private
  (`0600`) i owned-by-EUID.
- Bezpośredni zaufany katalog (`ConfigDir`, `StateDir`), jeśli istnieje, musi być **nie-symlinkiem**,
  katalogiem, mode-private i owned-by-EUID. Katalog nieistniejący = poprawny first-run. Walidacja
  otwiera katalog `O_RDONLY|O_DIRECTORY`, więc zaufany katalog musi być **używalny jako katalog
  aplikacji** — w praktyce `0700` (`mode-private` sam w sobie dopuszczałby np. `0100` tylko-exec, co
  jest fail-closed odrzucane przy otwarciu; utwardzenie tego rozróżnienia to ewentualny późniejszy
  slice, nie ta granica).
- **Zakres:** walidowany jest wyłącznie bezpośredni katalog aplikacji; łańcuch przodków ponad nim
  (XDG base / `$HOME`) pozostaje zaufany i poza granicą tego slice (brak pełnego ancestor-walk).
- **explicit `--config`:** otrzymuje pełne egzekwowanie *pliku* (no-follow, typ, mode-private,
  owned-by-EUID); tryb jego katalogu nadrzędnego **nie** jest wymagany (operator może wskazać wspólną
  lokalizację).
- **Zapis:** `WriteVersionLock` waliduje zaufany katalog **przed** utworzeniem plików — atomowy rename
  nie „legalizuje" symlinkowanego/permissive/obcego katalogu jako authority.
- Błędy uprawnień/typu/ścieżki nie ujawniają materiału o kształcie sekretu (redakcja na granicy).

## Config document — `config.json`

- Lokalizacja domyślna: `<config-dir>/config.json` (lub explicit `--config PATH`).
- Format: JSON (standardowa biblioteka Go). Dokładnie jeden dokument; trailing data jest błędem.
- Nieznane pola są odrzucane (`DisallowUnknownFields`) — schemat fail-closed.
- Własność/uprawnienia/typ: na darwin/linux wymagany zwykły plik, mode-private (`0600`) i
  owned-by-EUID, otwierany no-follow (patrz „Granica zaufania ścieżek"); szersze bity, symlink, obcy
  właściciel lub nie-zwykły typ są odrzucane. Poza darwin/linux egzekwowanie nie jest deklarowane
  (hosty Demo A: macOS/Linux arm64).
- Brak domyślnego configu to **poprawny first-run** (defaulty). Explicit `--config` wskazujący na
  nieistniejący plik to błąd (exit 2).

Pola:

| Pole | Typ | Wymagane | Semantyka |
|---|---|---|---|
| `schema_version` | string | tak | `"1"` albo `"2"`. Inna wartość → odrzucone (bez migracji). |
| `default_timeout` | string (Go duration) | nie | Domyślny timeout operacji; walidowany > 0 i ≤ policy max. Zasila timeout policy, gdy `--timeout` nie podano jawnie (flaga wygrywa). |
| `projects` | array | nie (tylko V2) | Aktywny project registry — patrz niżej. Pod `"1"` jest **nieznanym polem** i jest odrzucane. |

Przykład (poprawny V2):

```json
{
  "schema_version": "2",
  "default_timeout": "45s",
  "projects": [
    {
      "id": "my-project",
      "display_name": "My Project",
      "remote": "git@github.com:owner/my-project.git"
    }
  ]
}
```

## Project registry — schema V2

Registry jest **niesekretnym** źródłem prawdy o podpiętych projektach
([ADR-0015](../adr/0015-torio-v1-onboarding-projects-and-operator-push.md)).
Legacy `archive/pre-v1:docs/contracts/project-config.md` i
`archive/pre-v1:schemas/project.schema.json` **nie** obowiązują i nie są już w drzewie
([ADR-0017](../adr/0017-pre-v1-exploration-leaves-the-working-tree.md)).

**Workspace path nie jest polem.** Jest wyprowadzany z `id` (`/home/hermes/projects/<id>`) przez
warstwę projektów, więc config nie może wskazać projektu na dowolną ścieżkę w guest. Obiekt projektu
z polem `path` jest odrzucany jak każde nieznane pole.

| Pole | Typ | Wymagane | Reguła |
|---|---|---|---|
| `id` | string | tak | Lowercase slug ASCII: litery, cyfry i wewnętrzne `-`; ≤ 64 bajty; unikalny w dokumencie. Ten sam charset wyprowadza workspace path, więc nic w nim nie może traversować ani zmienić katalogu. |
| `display_name` | string | tak | Niepusty, ≤ 64 bajty, poprawny UTF-8, bez control characters i bez leading/trailing whitespace. |
| `remote` | string | tak | Wspierana forma transportu (niżej); ≤ 512 bajtów. |

Wspierane formy `remote`:

- `https://host[:port]/path` — **bez userinfo w ogóle**, bo to jedyne miejsce, w którym siedzi token
  albo hasło HTTPS;
- `ssh://[user@]host[:port]/path` — username jest niesekretnym elementem transportu i jest
  dozwolony, hasło nigdy;
- `[user@]host:path` — forma scp-like ze **względną** ścieżką (absolutna sprawiłaby, że lokalne
  `C:/repo` wygląda jak remote).

Odrzucane fail-closed: query i fragment (mogą nieść token), percent-encoding (ukrywa powyższe),
control characters i whitespace, wiodący `-` (Git przeczytałby remote jako flagę), lokalna ścieżka
i `file://`, `http://`, `git://`, oraz materiał o kształcie sekretu w dowolnym polu.

Reguły całego registry:

- **Unikalne `id`** — egzekwowane zawsze, także przy odczycie.
- **Duplikat `remote`** — odrzucany domyślnie **przy dodawaniu** (`AddOptions.AllowDuplicateRemote`
  to jawna decyzja operatora). Walidacja dokumentu go nie odrzuca: raz podjęta jawna decyzja nie może
  uczynić configu nieczytelnym.
- **Bounded** — maksymalnie 64 projekty; config jest czytany przy każdej inwokacji.
- Registry jest walidowany **przy odczycie i przy zapisie**, więc ręcznie wyedytowany dokument nie
  przemyci wpisu, którego write path by nie przyjął.

## Kompatybilność V1 ↔ V2

- Czytane są oba dokumenty. V1 normalizuje się do **pustego** registry, nie do błędu.
- Każdy zapis emituje V2: pierwsza mutacja (dodanie/usunięcie projektu) podnosi wersję dokumentu.
  `File` deklarujący inną wersję jest przy zapisie odrzucany, nie podnoszony po cichu.
- Starsza binarka **jawnie odrzuca** V2 — i przez version gate (`"2"` nie jest wspierane), i przez
  `DisallowUnknownFields` na polu `projects`. Nie może przeczytać V2 jako settings-only i po cichu
  zgubić registry.

## Zapis configu

- Zapis idzie tą samą crash-safe ścieżką co version-lock: private temp → `fsync` → atomic rename,
  `0600`, katalog `0700`.
- Dokument jest walidowany **przed** utworzeniem pliku, a zaufany katalog **przed** zapisem — atomowy
  rename nie legalizuje symlinkowanego/permissive/obcego katalogu jako authority.
- Projekty są sortowane po `id`, więc ten sam registry daje ten sam plik niezależnie od kolejności
  dodawania.
- Po rename plik jest **odczytany ponownie** tą samą zaufaną ścieżką co loader, sparsowany,
  zwalidowany i porównany z dokumentem, który miał zostać zapisany. Rozjazd jest raportowany, nie
  naprawiany po cichu — rename już się wydarzył, więc decyzję podejmuje operator.

## Version-lock manifest — `version-lock.json`

- Lokalizacja: `<config-dir>/version-lock.json` (kanoniczna, contained w katalogu config).
- Własność: operator-authored, zaufany metadata pin. Non-secret; na darwin/linux zwykły plik,
  mode-private (`0600`) i owned-by-EUID, otwierany no-follow (patrz „Granica zaufania ścieżek").
- Format: JSON, dokładnie jeden dokument, nieznane pola odrzucane, `schema_version` = `"1"`.
- Zapis jest crash-safe (temp → fsync → atomic rename); niepoprawny manifest jest odrzucany
  **przed** utworzeniem pliku, a zaufany katalog jest walidowany **przed** zapisem (atomowy rename nie
  legalizuje niezaufanego katalogu jako authority).

Pola:

| Pole | Typ | Wymagane | Semantyka |
|---|---|---|---|
| `schema_version` | string | tak | Musi być `"1"`. |
| `lima` | string | nie | Przypięta wersja Lima. Pusty = nieprzypięte. |
| `docker` | string | nie | Przypięta wersja Dockera. |
| `hermes` | string | nie | Przypięta wersja Hermes CLI. |

Każda ustawiona wartość musi pasować do wzorca `^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$` i nie może mieć
kształtu sekretu.

**Zakres D2:** manifest ma wyłącznie lifecycle parse/validate/write. D2 **nie** wykonuje runtime
probingu, nie instaluje niczego i nie zawiera adapterów Lima/Docker/Hermes.

**Konsumenci (przyszłe slice'y):**

- **D3 — Lima adapter:** feature/version probe używa przypiętej wersji `lima`.
- **D4 — Deterministic bootstrap:** instalacja przypiętych zależności (`docker`, `hermes`, `lima`).

## Exit / błędy

Błędy resolucji i walidacji konfiguracji mapują się na usage/schema error (exit `2`) zgodnie z
[`cli.md`](cli.md). Komunikaty błędów nie ujawniają materiału o kształcie sekretu — gwarancja jest
egzekwowana na granicy pakietu `internal/config` (redakcja każdego zwracanego błędu), więc obowiązuje
także dla bezpośrednich wywołań API, nie tylko przez finalny renderer CLI. Skan surowych bajtów
odrzuca sekrety wcześnie, ale sam nie wystarcza: wartość o kształcie sekretu zapisana w formie
JSON-escaped (np. z literą `h` w prefiksie zapisaną jako escape `\uXXXX`) nie ma w surowych bajtach
dosłownego prefiksu, więc dekoder mógłby ją odtworzyć i wstawić do błędu — przez interpolację `%q`
zdekodowanej wartości albo przez nazwę nieznanego pola zwróconą z `DisallowUnknownFields`. Redakcja
na granicy zamyka tę ścieżkę; finalny renderer redaguje znane kształty dodatkowo, jako defense in
depth.
