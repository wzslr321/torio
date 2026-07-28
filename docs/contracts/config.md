# Config contract (D2)

Ten dokument opisuje typowaną konfigurację hosta wprowadzoną w D2
(patrz `archive/pre-v1:docs/plans/02-demo-a.md` § D2). Implementacja: `internal/config/`.
Konfiguracja jest **non-secret** (AGENTS §6): materiał o kształcie sekretu jest odrzucany.

> **Version-lock manifest (`version-lock.json`) nie istnieje.** Był zaprojektowany w D2, ale
> nigdy nie został podpięty: żadna komenda go nie czytała, a jego jedyny konsument
> (`lima.Probe`) nie był wołany. Kod i ten opis zostały usunięte — patrz
> [ADR-0017](../adr/0017-pre-v1-exploration-leaves-the-working-tree.md). Opisana niżej granica
> zaufania ścieżek obowiązuje bez zmian dla `config.json`.

> **Katalog state nie istnieje.** `XDG_STATE_HOME`, `Paths.StateDir` i flaga `--state-dir` służyły
> wyłącznie manifestowi version-lock i zniknęły razem z nim — patrz
> [ADR-0019](../adr/0019-state-directory-and-config-schema-v1-leave.md). Torio nie zapisuje na
> hoście żadnego trwałego stanu poza `config.json`.

## Lokalizacje (XDG)

Ścieżki resolują się deterministycznie z zmiennych XDG, z udokumentowanymi fallbackami:

| Rola | Baza (env) | Fallback | Katalog aplikacji |
|---|---|---|---|
| Config | `XDG_CONFIG_HOME` | `$HOME/.config` | `<base>/torio/` |

Reguły:

- Ustawiony, ale **non-absolutny** `XDG_CONFIG_HOME` jest odrzucany fail-closed
  (nie jest po cichu ignorowany ani "naprawiany").
- Gdy XDG base jest nieustawiony i nie można ustalić `$HOME`, resolucja kończy się błędem
  (fail closed) zamiast zgadywać lokalizację.
- Fallback `$HOME` musi być **absolutny**. Non-absolutny `$HOME` (przy nieustawionym XDG) jest
  odrzucany fail-closed — nie jest kanonikalizowany względem CWD — z tego samego powodu co
  non-absolutny XDG base: katalog roboczy nie może wyznaczać domyślnej zaufanej lokalizacji
  configu.
- Flaga `--config PATH` nadpisuje wartość i jest kanonikalizowana
  (`filepath.Abs` + `Clean`, bez rozwijania symlinków — explicit override to zaufany input).
- **Precedence:** explicit `--config` całkowicie **omija** XDG base — malformed `XDG_CONFIG_HOME`
  ani `$HOME` nie mogą zablokować w pełni jawnej inwokacji. XDG jest konsultowany wyłącznie wtedy,
  gdy override jest nieobecny, i wtedy nadal ściśle walidowany (patrz reguła absolutności powyżej).
- Przy explicit `--config` zaufanym katalogiem config jest katalog nadrzędny wskazanego pliku.
- Pliki lokalizowane **wewnątrz** zaufanego katalogu (`config.json`) używają
  contained-join: nazwa musi być czystą nazwą pliku, a wynik nie może opuścić katalogu bazowego.
  Traversal jest odrzucany strukturalnie, nie przez czyszczenie stringów.

## Granica zaufania ścieżek (ADR-0013)

Zanim `config.json` stanie się authority, ścieżki są egzekwowane fail-closed.
Terminologia jest rozdzielona (koniec z „owner-only"):

- **mode-private** — brak dostępu grupy/innych: `perm & 0o077 == 0`.
- **owned-by-EUID** — właściciel obiektu to efektywny użytkownik procesu: `st_uid == geteuid()`
  (ścisła równość; jako root oczekiwany jest obiekt root-owned).

Reguły (egzekwowane na **darwin/linux**; poza nimi jawny, udokumentowany no-op — hosty Demo A to
macOS/Linux arm64):

- Zaufane pliki (`config.json`, także explicit `--config`) są otwierane
  **no-follow** (`O_NOFOLLOW`): symlink w ostatnim komponencie jest odrzucany. Typ, tryb i własność są
  weryfikowane przez `Fstat` **na tym samym deskryptorze**, z którego następuje odczyt — brak TOCTOU
  na ostatnim komponencie (`Lstat`+`ReadFile` jest niedozwolone). Plik musi być zwykły, mode-private
  (`0600`) i owned-by-EUID.
- Bezpośredni zaufany katalog (`ConfigDir`), jeśli istnieje, musi być **nie-symlinkiem**,
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
- **Zapis:** `WriteFile` waliduje zaufany katalog **przed** utworzeniem plików — atomowy rename
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
| `schema_version` | string | tak | `"2"`. Inna wartość → odrzucone (bez migracji). |
| `default_timeout` | string (Go duration) | nie | Domyślny timeout operacji; walidowany > 0 i ≤ policy max. Zasila timeout policy, gdy `--timeout` nie podano jawnie (flaga wygrywa). |
| `projects` | array | nie | Aktywny project registry — patrz niżej. Pominięty normalizuje się do pustego registry. |

Przykład (poprawny dokument):

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

## Wersja schematu

- `"2"` jest **jedyną** wspieraną wersją, przy odczycie i przy zapisie. `File` deklarujący inną
  wersję jest przy zapisie odrzucany, nie podnoszony po cichu.
- Poprzednik `"1"` (settings-only, sprzed registry) **nie jest czytany**. Torio nie miało wydania,
  które by taki dokument zapisało, więc żaden nie istnieje —
  [ADR-0019](../adr/0019-state-directory-and-config-schema-v1-leave.md). Ręcznie napisany dokument
  z `"1"` jest odrzucany jawnie (exit 2), nie czytany jako settings-only.
- Starsza binarka **jawnie odrzuca** ten dokument — i przez własny version gate (`"2"` nie jest dla
  niej wspierane), i przez `DisallowUnknownFields` na polu `projects`. Nie może przeczytać go jako
  settings-only i po cichu zgubić registry. Ta gwarancja leży po jej stronie i obowiązuje
  niezależnie od tego, co czyta binarka bieżąca.

## Zapis configu

- Zapis jest crash-safe: private temp → `fsync` → atomic rename, `0600`, katalog `0700`.
- Dokument jest walidowany **przed** utworzeniem pliku, a zaufany katalog **przed** zapisem — atomowy
  rename nie legalizuje symlinkowanego/permissive/obcego katalogu jako authority.
- Projekty są sortowane po `id`, więc ten sam registry daje ten sam plik niezależnie od kolejności
  dodawania.
- Po rename plik jest **odczytany ponownie** tą samą zaufaną ścieżką co loader, sparsowany,
  zwalidowany i porównany z dokumentem, który miał zostać zapisany. Rozjazd jest raportowany, nie
  naprawiany po cichu — rename już się wydarzył, więc decyzję podejmuje operator.

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
