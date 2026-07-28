# ADR-0013: Trusted config authority — polityka symlinków, typu i własności ścieżek

- Status: Accepted (rev. 3 — final policy acceptance; wdrożone w D3.0 enforcement slice)
- Date: 2026-07-25
- Dotyczy: `internal/config/`, kontrakt [`../contracts/config.md`](../contracts/config.md), plan
  `archive/pre-v1:docs/plans/02-demo-a.md` (entry gate przed D3)
- Powiązane: [`0003-lima-trust-boundary.md`](0003-lima-trust-boundary.md) (granica VM — inna
  warstwa; ten ADR jej nie zmienia ani nie supersede'uje)

> **Nota statusu (2026-07-28).** Polityka opisana niżej obowiązuje bez zmian dla `config.json`
> i katalogu `ConfigDir`; egzekwuje ją `internal/config/trust_darwinlinux.go`.
> Z macierzy poniżej wypadły dwa obiekty — nie dlatego, że reguła dla nich była zła, tylko
> dlatego, że sam obiekt przestał istnieć:
>
> - **version-lock manifest `version-lock.json`** nigdy nie został podpięty: żadna komenda go nie
>   czytała, a jedyny konsument pinu (`lima.Probe`) nie był wołany z CLI. `versionlock.go`,
>   `LoadVersionLock`, `WriteVersionLock`, `VersionLockPath` i `Probe` zostały usunięte —
>   [ADR-0017](0017-pre-v1-exploration-leaves-the-working-tree.md).
> - **`StateDir`** stracił wraz z nim swojego jedynego mieszkańca. Torio nie zapisuje na hoście
>   żadnego stanu, więc `Options.StateDir`, `Paths.StateDir`, gałąź `XDG_STATE_HOME` i flaga
>   `--state-dir` zostały usunięte — [ADR-0019](0019-state-directory-and-config-schema-v1-leave.md).
>
> Ta nota nie zmienia decyzji — odnotowuje, która jej część została dostarczona.

## Context

D2 wprowadziło typowaną konfigurację hosta (`config.json`) oraz version-lock manifest
(`version-lock.json`), które D3 (Lima adapter) i D4 (bootstrap) będą traktować jako **authority**
dla wersji i lifecycle zewnętrznych narzędzi. Kontrakt config.md nazywa `ConfigDir` „zaufanym
katalogiem", ale bieżąca implementacja **nie egzekwuje** tej zaufaności. Trzy zachowania zostały
odtworzone testowo na `origin/main` = `d1642489` (throwaway repro, RED evidence w raporcie taska):

1. **Symlink domyślnego `config.json` poza `ConfigDir`.** `Load` czyta plik przez `os.ReadFile`
   (podąża za symlinkiem) i sprawdza uprawnienia przez `os.Stat` (statuje *target*, nie link).
   Symlink → mode-private plik poza katalogiem został **zaakceptowany**; jego `default_timeout`
   (`99s`) stał się efektywną konfiguracją. Treść spoza zaufanego katalogu stała się authority.
2. **Symlink `version-lock.json` poza katalogiem.** `LoadVersionLock` analogicznie podąża za
   linkiem (`os.Stat` + `os.ReadFile`); pin `lima` z pliku spoza katalogu został zaakceptowany.
   To bezpośrednio zatruwa przyszły D3 feature/version probe.
3. **World-writable (`0777`) istniejący `ConfigDir`.** Ścieżka odczytu nigdy nie sprawdza modu ani
   typu/własności samego `ConfigDir` (waliduje tylko `StateDir` i sam plik). Katalog, w którym
   dowolny użytkownik może podmienić `config.json`/`version-lock.json`, jest traktowany jako zaufany.
   `os.MkdirAll(dir, 0700)` **nie** zaostrza modu już istniejącego katalogu, więc permissive katalog
   się utrzymuje.

Zanim config/version-lock zaczną sterować zewnętrznym lifecycle (D3/D4), granica zaufania katalogu
MUSI być realnie egzekwowana, inaczej cały łańcuch admission/lifecycle dziedziczy spoofowalną
authority. `Lstat` przed `ReadFile` **nie** jest wystarczający: obiekt może zostać podmieniony między
sprawdzeniem a użyciem (TOCTOU). Poprawny prymityw musi operować na tym samym otwartym obiekcie,
który jest odczytywany.

## Terminologia (precyzyjna — bez overclaimu)

Rozdzielamy dwie niezależne własności, których dotąd zbiorczo (i mylnie) nazywano „owner-only":

- **mode-private** — bity uprawnień nie zawierają dostępu grupy/innych: `mode.Perm()&0o077 == 0`.
  Dowodzi *prywatnego trybu*, nie tożsamości właściciela.
- **owned-by-EUID** — właściciel obiektu to efektywny użytkownik procesu: `st_uid == os.Geteuid()`.
  Dowodzi *tożsamości właściciela*, nie trybu.

Zaufanie ścieżki wymaga **obu** własności jednocześnie (plus właściwy typ obiektu). Nigdzie w
kontrakcie/ADR nie wolno pisać „owner-only" w znaczeniu wyłącznie trybu.

## Empiryczne przesłanki (weryfikacja prymitywu, go1.26.x / darwin arm64, stdlib)

Zweryfikowane realnym probe (`syscall`, bez zewnętrznych zależności):

- `syscall.Open(path, O_RDONLY|O_NOFOLLOW, 0)` + `syscall.Fstat(fd, &st)` — otwiera zwykły plik;
  odrzuca symlink w ostatnim komponencie; `Fstat` na **tym samym fd** daje `st.Uid` (→ owner-match),
  tryb (→ mode-private) i typ (`S_IFREG` → regular). Odczyt z tego fd operuje na tym samym obiekcie →
  brak TOCTOU na ostatnim komponencie.
- `syscall.Open(dir, O_RDONLY|O_NOFOLLOW|O_DIRECTORY, 0)` + `Fstat` — otwiera katalog nie-symlink;
  symlink w tym komponencie jest odrzucony (na darwin `ENOTDIR`, na innych `ELOOP` — wykrywamy przez
  *błąd otwarcia*, nie przez konkretne errno). `Fstat` daje własność/tryb/typ (`S_IFDIR`).
- **`syscall.Openat` NIE istnieje na darwin** (jest tylko na linux / w `golang.org/x/sys/unix`).
  Pełna atomowość *openat-relative* (jeden dirfd dla całej resolucji) wymagałaby więc nowej
  zależności `golang.org/x/sys/unix`. **Odrzucone** — w zaakceptowanym scope (łańcuch rodziców
  ponad bezpośrednim katalogiem aplikacji jest zaufany i poza granicą, patrz decyzja 2) open pliku po
  **pełnej ścieżce** z własnym `O_NOFOLLOW` + niezależne `O_NOFOLLOW|O_DIRECTORY` na samym
  `ConfigDir` w pełni domykają odtworzone wektory bez openat i bez nowej zależności.

Wniosek: mechanizm jest realizowalny **czystym stdlib** (`syscall.Open`/`Fstat`/`Geteuid`),
`os.Root` nie jest potrzebny jako główny prymityw (nie waliduje własności/parenta katalogu roota).

## Decision

Wprowadzić egzekwowaną granicę zaufania dla ścieżek config/version-lock: **no-follow open na tym
samym otwartym obiekcie + weryfikacja typu + mode-private + owned-by-EUID**, dla bezpośredniego
`ConfigDir` (jego samego, nie tylko zawartości) oraz dla plików w nim.

### Polityka (macierz — rev. 2)

| Ścieżka | Symlink (ostatni komponent) | Typ | mode-private | owned-by-EUID | Naruszenie |
|---|---|---|---|---|---|
| Domyślny `ConfigDir` (`<xdg>/hermes-box`), jeśli istnieje | MUSI NIE być symlinkiem (`O_NOFOLLOW\|O_DIRECTORY`) | katalog (`S_IFDIR`) | tak | tak | fail-closed reject |
| Domyślny `config.json` | MUSI NIE być symlinkiem (`O_NOFOLLOW`) | plik zwykły (`S_IFREG`) | tak (`0600`) | tak | reject |
| `version-lock.json` | jw. | plik zwykły | tak (`0600`) | tak | reject |
| explicit `--config PATH` (plik) | **TAK — egzekwowane** (`O_NOFOLLOW`) | plik zwykły | tak (`0600`) | tak | reject pliku |
| `StateDir` (domyślny lub explicit), jeśli istnieje | MUSI NIE być symlinkiem (`O_NOFOLLOW\|O_DIRECTORY`) | katalog | tak | tak | reject |

Zasady przekrojowe:
- Odczyt, typ, tryb i własność MUSZĄ pochodzić z tego samego otwartego deskryptora (`Fstat` na fd),
  bez powtórnej resolucji ścieżki. `os.Stat`/`os.ReadFile` po ścieżce są zastąpione przez
  open→fstat→read.
- Kanonikalizacja explicit override pozostaje bez zmian (`Abs`+`Clean`, bez rozwijania symlinków w
  stringu) — no-follow działa na poziomie *otwarcia obiektu*, nie na czyszczeniu stringa.
- Błędy nie ujawniają materiału o kształcie sekretu. Ścieżka FS bywa **caller-controlled** (explicit
  `--config`, ścieżka/katalog version-lock) i sama może mieć kształt sekretu, więc `redactErr` jest
  stosowany na **granicy pakietu** dla każdego zwracanego błędu (`Load`, `LoadVersionLock`,
  `WriteVersionLock`) — nie tylko na ścieżkach parse. `redactErr` zostawia błąd nie-sekretny bez zmian
  (zachowuje wrapping/diagnostykę) i spłaszcza tylko komunikat, który wyciekłby kształt sekretu; nie
  osłabia to diagnostyki `verifyTrusted`.

### Zachowanie jako root (jawne)

`owned-by-EUID` to **ścisła równość** `st_uid == os.Geteuid()`. Gdy proces działa jako root
(`EUID==0`), oczekiwanym właścicielem jest `0` — config/dir MUSZĄ być root-owned. Konsekwencja
świadoma: `sudo hb` przeciwko config w home zwykłego użytkownika (`st_uid != 0`) jest **fail-closed
odrzucony**; to celowe — rozjazd między EUID a właścicielem authority jest dokładnie tą
niejednoznacznością, którą odrzucamy. Nie akceptujemy też „bardziej uprzywilejowanego" root-owned
config przy EUID zwykłego użytkownika (odrzucone przez ścisłą równość).

### Granica build / platforma (precyzyjna)

- Nowy prymityw trusted-open żyje pod tagiem **`//go:build darwin || linux`** (obsługiwana platforma
  bezpieczeństwa = Linux i Darwin arm64). **Nie** deklarujemy generycznej dostępności `unix`, mimo że
  `syscall.O_NOFOLLOW` bywa zdefiniowany szerzej — polityki nie twierdzimy poza darwin/linux.
- Companion **`//go:build !darwin && !linux`** dostarcza jawny, udokumentowany no-op:
  polityka trusted-authority NIE jest tam egzekwowana ani deklarowana (spójnie z istniejącym
  `perm_other.go`). (Alternatywa fail-closed poza darwin/linux — do rozważenia; Demo A hosty to
  wyłącznie darwin/linux, więc no-op nie osłabia realnego celu.)
- Sprawdzenie trybu/własności/typu jest **czyste i przenośne** (`verifyTrusted` w `trust.go`, bez
  tagów) i przyjmuje wartości z jednego `Fstat`. Dawny split `perm_unix.go`/`perm_other.go`
  (wyłącznie trybowy `enforcePrivateMode`) został **usunięty** — jego rola weszła do `verifyTrusted`,
  a właściwy prymityw bezpieczeństwa (no-follow open + `Fstat`) żyje w `trust_darwinlinux.go`
  (`darwin || linux`) z jawnym no-op `trust_other.go` (`!darwin && !linux`). Generyczny tag `unix`
  nie jest już używany dla tej polityki.
- **Bez nowej zależności**: `golang.org/x/sys/unix` odrzucony (patrz „Empiryczne przesłanki").

### Strategia testów (zaimplementowana — RED→GREEN)

> Historia (superseded): rev. 1–2 planowały te testy jako „jeszcze nie napisane" i zakładały
> **wstrzykiwalny resolver własności** w `Options` do deterministycznego testu owner-mismatch bez
> roota. Oba założenia zostały **odrzucone w implementacji**. Finalna strategia (poniżej) nie dodaje
> żadnego runtime-configurable bypassu bezpieczeństwa (constraint 2 final acceptance): własność w
> produkcji pochodzi wyłącznie z `Fstat` + `os.Geteuid`.

Testy są napisane i przechodzą (`internal/config`, TDD RED→GREEN):

- **Pure unit** (`trust_unit_test.go`, przenośne): `verifyTrusted` jest czystą funkcją przyjmującą
  `typ/perm/uid/euid` bezpośrednio, więc owner-mismatch (`uid != euid`), mode-permissive, zły typ i
  zachowanie root (`euid==0` wymaga `uid==0`) są **deterministyczne bez roota i bez chown** — to
  zastępuje odrzucony wstrzykiwalny resolver.
- **Integration przez public API** (`trust_test.go`, `//go:build darwin || linux`): symlink
  domyślnego `config.json`, symlink `ConfigDir`, world-writable `ConfigDir`, explicit `--config`
  symlink / non-private / non-regular, symlink `version-lock.json` i jego katalogu, `WriteVersionLock`
  do symlinkowanego / world-writable katalogu, symlink / non-directory `StateDir`, happy path
  (EUID-owned mode-private katalog + regular `0600`), oraz brak wycieku caller-controlled
  secret-shaped ścieżki w błędzie trust.
- **Owner-mismatch integracyjnie**: `config.json` i `StateDir` owned-by inny UID są testowane pod
  rootem (`chown 12345`), a poza rootem `t.Skip` — deterministyczne pokrycie tej reguły daje pure
  unit test.
- **Poza darwin/linux**: enforcement assercje są gated (build tag `darwin || linux` lub runtime
  skip); zwykłe testy funkcjonalne pozostają aktywne. Polityka nie jest tam deklarowana (no-op).

## Decyzje orchestratora (rozstrzygnięte — rev. 2)

1. **explicit `--config`: TAK.** Egzekwujemy no-follow, typ regular, mode-private i owned-by-EUID
   także dla jawnej ścieżki. „Zaufany operator input" nie usprawiedliwia symlinkowanego ani
   nie-prywatnego pliku authority. **Nie** wymagamy `0700` parenta dla explicit config w D3.0.
2. **Głębokość łańcucha rodziców: tylko bezpośredni `ConfigDir`.** Brak pełnego ancestor-walk w tym
   slice. Bezpośredni katalog aplikacji MUSI być nie-symlinkiem, owned-by-EUID i mode-private jak
   wyżej; łańcuch przodków ponad nim (XDG base / `$HOME`) pozostaje zaufany i **poza** granicą tego
   slice.

## Consequences

- Wszystkie trzy odtworzone wektory + symlinkowany `ConfigDir` + spoofing własności zamknięte
  fail-closed przed jakimkolwiek konsumowaniem config/version-lock przez D3/D4.
- Przy akceptacji (krok enforcement, po sign-off): dodać wiersz **TM-16** do
  `archive/pre-v1:docs/04-threat-model.md` („Config/version-lock authority spoofowana przez
  symlink, world-writable lub obcy-UID trusted dir/plik" → control: no-follow open + non-symlink dir
  + mode-private + owned-by-EUID → fail-closed test) oraz wpis w
  `archive/pre-v1:docs/13-requirements-traceability.md`; zaktualizować
  [`../contracts/config.md`](../contracts/config.md) o politykę no-follow, typ, mode-private i
  owned-by-EUID, z rozdzieloną terminologią (mode-private vs owned-by-EUID; koniec z „owner-only").
- Platforma: enforcement darwin/linux; poza tym jawny udokumentowany no-op.
- Istniejące poprawne przypadki (EUID-owned prywatny katalog, zwykły `0600` plik, absent default =
  first-run) pozostają bez zmian.

## Rejected / poza zakresem

- **`os.Root` jako główny mechanizm** — odrzucony: nie waliduje własności/typu/modu samego `ConfigDir`
  ani jego parenta; nasza polityka wymaga jawnego `Fstat` własności, którego `os.Root` nie zapewnia.
- **`golang.org/x/sys/unix` / openat-relative** — odrzucone: nowa zależność, a w zaakceptowanym
  scope (parent trusted) niepotrzebna; open po pełnej ścieżce z `O_NOFOLLOW` wystarcza.
- **`Lstat`-only** — odrzucony: TOCTOU.
- **Pełny ancestor-chain walk** do `$HOME`/XDG base — poza zakresem D3.0 (osobny slice).
- **Naprawianie/zaostrzanie userowego środowiska** poza egzekwowaniem zaufanych ścieżek (żadnego
  `chmod`/`chown` cudzych obiektów) — poza zakresem.
- **Lima adapter, `hb vm ...`, provisioning, doctor, bootstrap, nowe feature flagi** — poza zakresem
  D3.0 (właściwe D3, po akceptacji tego ADR).

## Status wdrożenia

Ten ADR jest **Accepted (rev. 3)** i wdrożony w D3.0 enforcement slice (TDD RED→GREEN). Pliki:

- `internal/config/trust.go` — pure `verifyTrusted` (typ + mode-private + owned-by-EUID) i
  `statTrustedDirIfExists`.
- `internal/config/trust_darwinlinux.go` (`//go:build darwin || linux`) — `openTrustedFile`,
  `statTrustedDir`, `fstatObj` (no-follow open + `Fstat`).
- `internal/config/trust_other.go` (`//go:build !darwin && !linux`) — udokumentowany no-op.
- Przepięte ścieżki: `file.go` (`Load`), `versionlock.go` (`LoadVersionLock`, `WriteVersionLock`).
  Usunięte: `perm_unix.go`, `perm_other.go`, `statPrivate`, `statDirIfExists`.

Wraz z enforcementem zaktualizowano [`../contracts/config.md`](../contracts/config.md), threat model
(TM-16, `archive/pre-v1:docs/04-threat-model.md`) i traceability
(`archive/pre-v1:docs/13-requirements-traceability.md`).
