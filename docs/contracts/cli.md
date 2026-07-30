# CLI contract

> Ten dokument jest **normatywny**: opisuje command surface dostarczonej binarki. Rozjazd
> z jej zachowaniem jest defektem do naprawy, a nie zapisem do zachowania — patrz
> [ADR-0016](../adr/0016-normative-documents-are-corrected-not-archived.md).
>
> Kontrakt powstał dla platformy **pre-V0** i przez jakiś czas opisywał komendy, których
> binarka nigdy nie miała (`doctor`, `status`, `reconcile`, `vm logs`, `gateway`, `task`,
> `admin`), a nie opisywał `brain` ani dostarczonego kształtu `project`. Ta treść została
> usunięta, a nie zarchiwizowana: kontrakt, który wymienia nieistniejące komendy, myli
> czytelnika bez względu na to, jak jest opatrzony. Zakres wyznacza
> [ADR-0015](../adr/0015-torio-v1-onboarding-projects-and-operator-push.md); pre-V1
> eksploracja, z której pochodziła usunięta treść, jest pod tagiem `archive/pre-v1`
> ([ADR-0017](../adr/0017-pre-v1-exploration-leaves-the-working-tree.md)).

## Binary i output

Binary nazywa się `torio` (zmiana nazwy: [ADR-0014](../adr/0014-rename-to-torio.md)). Domyślnie wypisuje czytelny output na stdout i diagnostykę na stderr. `--json` zwraca dokładnie jeden JSON document na stdout i nie miesza z nim logów.

### JSON envelope

```json
{
  "schema_version": "1",
  "ok": true,
  "command": "vm.status",
  "data": {},
  "warnings": [],
  "error": null
}
```

Błąd:

```json
{
  "schema_version": "1",
  "ok": false,
  "command": "project.add",
  "data": null,
  "warnings": [],
  "error": {
    "code": "CONFLICT",
    "message": "project id is already registered",
    "details": {
      "notes": "cloned,shared"
    }
  }
}
```

`command` jest to samo dla sukcesu i błędu tej samej komendy, więc maszynowy caller nie musi
zgadywać, co się nie udało.

`message` **i wszystkie wartości w `details`** nie mogą zawierać credentials, raw env ani pełnych
command lines zawierających sekrety; finalny renderer redaguje oba po znanych kształtach jako
ostatnia linia obrony.

`warnings` jest dziś zawsze pustą tablicą: żadna komenda nie ma warunku niefatalnego, który nie
należałby już do `data`. Pole zostaje, bo caller parsujący envelope może polegać na jego obecności.

## Exit codes

| Exit | Klasa | Przykład |
|---:|---|---|
| 0 | success/idempotent success | zgodna istniejąca VM przy `vm init` |
| 2 | usage/schema validation | brak argumentu, invalid config, nieznana flaga |
| 3 | unmet precondition | VM stopped, backend not installed, Brain nieobecny |
| 5 | stale/conflict | id albo remote już zajęty |
| 6 | verification failed | drift bootstrapu, endpoint nie odpowiada 200 |
| 7 | permission/capability denied | gość nie ma prawa czytać remote'u |
| 8 | external dependency failed | brak `limactl`, niezerowy exit komendy gościa |
| 9 | reconciliation required | praca na gościu się udała, zapis registry nie |

Kod **4** („policy denied") pochodził z platformy pre-V0 i nie jest produkowany przez żadną
komendę: V1 nie ma silnika policy, który mógłby czegokolwiek odmówić. Numer pozostaje nieużyty,
a nie przydzielony ponownie — tabela jest kontraktem, a recykling kodu po cichu zmieniłby
znaczenie istniejącej czwórki.

## Global flags

```text
--json                 machine output
--config PATH          explicit non-secret config
--timeout DURATION     bounded operation; cannot exceed policy maximum
--verbose              more redacted diagnostics on stderr
```

To pełna lista. Wszystkie cztery są realnymi globalnymi (persistent) flagami, działającymi przed
i po subkomendzie; nieznana flaga jest odrzucana (usage, exit 2), nie akceptowana po cichu.
`--config` resolwuje się do typowanej konfiguracji (patrz [`config.md`](config.md)) używanej przez
wykonanie komendy — nie jest tylko parsowany. Błąd resolucji lub walidacji konfiguracji jest
usage/schema error (exit 2).

Nie ma globalnego `--force`. Komendy mogą mieć wąskie, udokumentowane recovery flags, ale żadna nie
może omijać weryfikacji ani granicy credentials: `vm init` nie recreatuje niezgodnej instancji,
`brain import` nie nadpisuje istniejących danych, a `project remove` nie kasuje checkoutu.

`--state-dir PATH` **nie istnieje**. Był globalną flagą w D2, ale wskazywał katalog, do którego
Torio nigdy nic nie zapisało; zniknął wraz z manifestem version-lock —
[ADR-0019](../adr/0019-state-directory-and-config-schema-v1-leave.md).

### `--help` a `--json`

`--help`/`-h` jest jedynym, wąskim wyjątkiem od reguły „jeden JSON envelope na stdout w trybie
`--json`". Help to afordancja dla człowieka: wypisuje tekst usage na stdout i kończy exit 0, także gdy
podano `--json` (nie emituje envelope). Każde inne wyjście w trybie `--json` MUSI być dokładnie jednym
envelope.

## Command surface

To pełna lista. Każdy parent (`vm`, `serve`, `brain`, `project`, `mcp`) bez subkomendy albo z
nieznaną subkomendą kończy się usage error (exit 2) — fail-closed, tak jak root.

### Informacyjne

```text
torio version [--json]
```

### VM

```text
torio vm init [--cpus N] [--memory SIZE] [--disk SIZE]
torio vm start
torio vm stop
torio vm bootstrap
torio vm status
torio vm ssh -- COMMAND...
```

- `init` tworzy VM z wbudowanego, pinowanego template'u Gate 0 albo kończy się idempotentnym
  sukcesem, gdy istniejąca instancja pasuje do zaufanych pinów (image digest i URL, `mounts: []`,
  `ssh.forwardAgent=false`, `vz`/`aarch64`). Niezgodna instancja **fail-closed** (exit 6): nie ma
  `--force`, a Torio nigdy nie recreatuje, nie resetuje ani nie kasuje istniejącej VM.
- `--cpus`/`--memory`/`--disk` dobierają rozmiar VM przy **tworzeniu**; domyślnie 4 vCPU, `8GiB`
  i `60GiB`. To jedyne wartości operatora podstawiane do template'u poza jego login identity;
  `--memory` i `--disk` muszą być jednoliniowe. Ponieważ `init` nigdy nie recreatuje, zmiana tych
  wartości po utworzeniu VM wymaga usunięcia instancji poza Torio.
- Każde inne pole template'u jest stałe. Ich zmiana wymagałaby nowego ADR-u, nie flagi.
- `stop` nie usuwa VM ani danych. Jest idempotentne (już `Stopped` → exit 0) i nie ufa czystemu exit code: po `limactl stop` re-query wymaga stanu `Stopped`, inaczej fail-closed (exit 3). Nigdy nie używa `--force`.
- `bootstrap` reconciliuje i weryfikuje target dla Remote Second Brain V1: stabilną, nieinteraktywną komendę `hermes` oraz układ trwałych katalogów gościa na natywnym Linux filesystem. Działa wyłącznie na istniejącym targetcie po zweryfikowanym warunku `Running`, przez typed Lima/execx boundary (fixed argv, bez `sh -c`, bez sklejanych stringów, bounded/redacted output). Jest idempotentne i wąskie: może doinstalować pinowany launcher Hermes Agent oraz symlink PATH `/usr/local/bin/hermes`, ale **nie** recreatuje/re-image'uje VM, nie instaluje modelu/providera, nie przyjmuje sekretów i nie tworzy usługi backendu (to robi `serve install`).
- **Docker: `hermes` NIE MOŻE należeć do grupy `docker`.** Członkostwo w `docker` jest równoważne rootowi na gościu, więc rootful Docker dla tożsamości serwisowej `hermes` jest zakazany przez [ADR-0015](../adr/0015-torio-v1-onboarding-projects-and-operator-push.md). `bootstrap` **weryfikuje brak** tego członkostwa (`verifyHermesNotInDocker`) i fail-closed, gdy je znajdzie; template provisioningu usuwa `hermes` z `docker`, jeśli grupa istnieje. W V1 Docker Engine nie jest w ogóle instalowany, a `bootstrap` **nie** sprawdza osiągalności serwera Docker. Przyszły container runtime wymaga rootless, hermes-owned rozwiązania za osobnym ADR-em.
- `bootstrap` **weryfikuje** (nie ufa samemu exit code): istnienie użytkownika `hermes`; istnienie grupy `torio-projects` oraz członkostwo w niej `hermes` i operatora (login identity Limy); **brak** członkostwa `hermes` w grupie `docker`; `uname -m == aarch64`; `hermes --version` przez tę samą, dokumentowaną stabilną ścieżkę; `git --version`; że każda wymagana ścieżka jest katalogiem o oczekiwanym owner/group/mode na natywnym Linux filesystem (nie host share); oraz brak szerokiego host mountu macOS. Każdy nieznany/nieweryfikowalny stan lub drift (architektura/wersja/ownership/mount) jest raportowany i fail-closed (exit 6), nie papering over. Rerun jest sukcesem tylko gdy wszystkie postconditions są udowodnione.
- Wymagane ścieżki i ich role są rozłączne (ADR-0015; stałe w `internal/lima/bootstrap.go`) — `/home/hermes/.hermes` **nie jest** Knowledge Base:

  | Stała | Ścieżka | Rola |
  |---|---|---|
  | `HermesHome` | `/home/hermes` | home tożsamości serwisowej |
  | `HermesProfilePath` | `/home/hermes/.hermes` | profil i stan aplikacyjny Hermesa (`$HERMES_HOME`) |
  | `HermesBrainPath` | `/home/hermes/brain` | vault Second Brain |
  | `HermesWorkspacePath` | `/home/hermes/projects` | współdzielony workspace projektów |

  `bootstrap` weryfikuje profil i brain **niezależnie**; żadna z tych ścieżek nie jest aliasem drugiej.
- `bootstrap` wykonuje kilka bounded guest probes i może instalować Hermesa ze źródeł; uruchamiaj go z największym timeoutem, na jaki pozwala policy: `--timeout 10m` (`config.MaxTimeout`). Akcja dotarcia do zdalnego Hermesa po bootstrapie pozostaje operator-controlled (np. `torio vm ssh -- sudo -u hermes -- hermes --version`).

### Backend

```text
torio serve install
torio serve start|stop|restart|status
torio serve logs [--lines N]
```

- `serve install` zarządza własną **user** service (custom systemd unit dla użytkownika `hermes`)
  wyłącznie po feature detection (`hermes serve --help`). W D5 (V1) generuje deterministyczny unit
  `hermes-serve.service` z pinowanym loopback bindem (`--host 127.0.0.1 --port 9119`), `HERMES_HOME=/home/hermes/.hermes`
  i `Restart=always`, waliduje go `systemd-analyze --user verify` **przed aktywacją**, po czym
  `daemon-reload` + `enable`. Zapewnia `linger` dla `hermes`, by usługa `Restart=always` działała bez
  interaktywnej sesji i po reboot. Jest idempotentne (re-run bez zmiany = `changed:false`), nie przyjmuje
  sekretów i **nie startuje** backendu. Zapis unitu jest atomowy (staging → verify → rename); niepoprawny
  unit nigdy nie jest aktywowany. Kilka bounded guest probes — używaj większego `--timeout` (np. `--timeout 2m`).
- `serve start`/`restart` startują backend i **weryfikują** readiness: re-query stanu systemd (`is-active == active`)
  **oraz** rzeczywistą odpowiedź `GET /api/status == 200` przez loopback. Aktywny proces z martwym endpointem
  to porażka (exit 6). Idempotentne. `serve stop` jest graceful i idempotentne (re-query wymaga stanu
  nie-active), nie usuwa unitu/profilu/state.
- `serve status` udowadnia **oba**: stan user-systemd i faktyczną gotowość endpointu przez loopback.
  Exit 0 tylko gdy `active` i `/api/status == 200`; brak instalacji lub inactive → exit 3; aktywny z martwym
  endpointem → exit 6. Nie modyfikuje systemu.
- `serve logs [--lines N]` zwraca bounded, redagowane wpisy journala **tylko** dla unitu
  (`journalctl --user -u hermes-serve.service -n N --no-pager`) — scope'owane do unitu i
  redagowane przez execx, więc nie ujawnia własnej konfiguracji `torio` (profil/brain/provider). Nie jest
  to jednak absolutna gwarancja: własny stdout/stderr backendu Hermes może teoretycznie zawierać
  tekst pochodny od danych użytkownika. Traktuj to jako runtime-only ograniczenie ekspozycji, nie
  formalną gwarancję prywatności.
- `serve` binduje loopback w VM. Dotarcie z Maca to operator-controlled SSH tunnel do gościa
  `127.0.0.1:9119` (patrz [runbook](../runbooks/first-run.md)); `torio` nie dodaje własnej
  funkcji tunelu. `serve` to **backend Desktopu**. `torio gateway` (messaging) nie istnieje.

### Brain

```text
torio brain init
torio brain status
torio brain import <host-directory> [--into SUBDIR] [--dry-run]
```

Second Brain to prywatny, wersjonowany lokalnym Gitem vault Markdown pod `/home/hermes/brain`,
zarejestrowany jako osobny Hermes Project ([ADR-0015](../adr/0015-torio-v1-onboarding-projects-and-operator-push.md)).

- **Output nigdy nie zawiera nazw notatek ani ich treści.** Wszystkie trzy komendy raportują
  wyłącznie bounded aggregate metadata: liczby plików, sumę bajtów, digest manifestu, stabilne
  markery driftu. Dotyczy to także `error.details`. To jest granica prywatności Braina, nie
  kwestia zwięzłości.
- `init` tworzy kanoniczny scaffold atomowo przez prywatny staging na gościu, robi pierwszy lokalny
  commit i rejestruje Hermes Project. Po weryfikacji instaluje albo odświeża globalny skill
  `torio-brain`, żeby Brain był przeszukiwalny z innych projektów; Hermes cache'uje prompt skilla
  per proces backendu, więc otwarte sesje muszą zostać zrestartowane. Idempotentne dla pasującego
  managed state; odmawia dla niepustych, niezarządzanych danych. **Nie** konfiguruje remote'u
  i nie pushuje.
- `status` raportuje stan (`initialized`/`uninitialized`/drift), natywny filesystem,
  ownership i mode, stan worktree Gita, agregaty, rejestrację Hermes Projectu i stan skilla.
  Nie modyfikuje niczego.
- `import` przenosi allowlistowane pliki (Markdown, Canvas, lokalne załączniki) przez prywatny
  staging hosta i gościa, weryfikowany checksumem po stronie gościa. Pliki o kształcie credentiala,
  metadane repozytoriów, linki, hardlinki, pliki specjalne i wykonywalne są odrzucane albo pomijane.
  Istniejące dane **nigdy** nie są nadpisywane — z jedynym wyjątkiem dokładnie pristine scaffoldu
  Torio. `--into` importuje jako jedno nowe, zawarte poddrzewo (sposób na uniknięcie kolizji);
  `--dry-run` wykonuje preflight bez transferu i bez zmiany danych Braina.

**Torio wnosi dane do środka i nie wynosi ich na zewnątrz.** `brain export` nie istnieje
([ADR-0018](../adr/0018-brain-export-leaves-v1.md)). Skopiowanie Braina na Maca to jawna operacja
operatora:

```bash
limactl copy torio:/home/hermes/brain/ <host-destination>/
```

Torio nie deklaruje, że jest to backup, i niczego w niej nie weryfikuje.

### Projects

```text
torio project add <name> <remote> [--id SLUG] [--use]
torio project list
torio project show <id>
torio project use <id>
torio project remove <id>
torio project shell <id>
```

- **Workspace path nie jest inputem.** Jest zawsze wyprowadzany jako
  `/home/hermes/projects/<id>`, nigdy przyjmowany od operatora i nigdy przechowywany w configu
  (patrz [`config.md`](config.md)). Bez `--id` identyfikatorem jest samo `<name>`, które musi być
  lowercase slugiem.
- **Torio nie przechowuje żadnych credentials Gita.** Remote, którego gość nie potrafi już czytać
  nieinteraktywnie, kończy się fail-closed (exit 7) — remedium jest ludzkie nadanie dostępu poza
  Torio, a nie retry.
- `add` klonuje dokładnie podany remote do wyprowadzonej ścieżki **albo** weryfikuje i adoptuje
  checkout, który już tam jest, nadaje operatorowi i `hermes` współdzielony dostęp i rejestruje
  projekt w Hermesie przed zapisem do configu. Nic na gościu nie jest resetowane, czyszczone ani
  kasowane, więc rerun po błędzie dokańcza pracę. `--use` czyni projekt aktywnym po sukcesie.
- `list` czyta wyłącznie config i nie uruchamia żadnej komendy na gościu — działa przy wyłączonej VM.
- `show` raportuje wpis registry, stan checkoutu i rejestracji w Hermesie. **Raportuje drift jako
  stabilne markery zamiast go naprawiać** i nigdy nie zwraca nazw plików, diffów ani surowego
  outputu Gita.
- `remove` archiwizuje Hermes Project i usuwa wpis z configu. Katalog checkoutu **nigdy** nie jest
  kasowany, a output mówi wprost, gdzie nadal jest. V1 nie ma `--delete`.
- `shell` otwiera efemeryczną sesję operatora w checkoucie z forwardowanym agentem SSH. **To jedyna
  droga, którą write capability wobec remote'ów Gita dociera do gościa**, i żyje dokładnie do
  wyjścia z sesji; persistent Hermes ma wobec origin wyłącznie read. Zdanie było kiedyś napisane
  bez tego zawężenia i przez to nieprawdziwe: zdolność zapisu docierająca przez serwer MCP nie
  przechodzi tą drogą i nie kończy się z sesją — jest osobnym, jawnie przyznanym kanałem opisanym
  niżej ([ADR-0022](../adr/0022-mcp-credential-broker.md)). Sesja jest preflightowana (projekt zarejestrowany, VM zweryfikowana
  bootstrapem, checkout obecny z zarejestrowanym origin i współdzielonymi uprawnieniami, lokalny
  agent trzyma tożsamość do forwardu), ale Torio **nigdy nie robi testowego pusha**, żeby cokolwiek
  udowodnić. Sesja nie jest ograniczana `--timeout`: kończy ją operator.
  Po jej zakończeniu Torio nie twierdzi, czego dotyczył push — sprawdź remote sam.
- `shell` jest interaktywne i **nie wspiera `--json`**: nie ma dokumentu do wyemitowania, więc
  `--json` jest usage error (exit 2), a nie po cichu zignorowaną flagą.

### MCP

```text
torio mcp install
torio mcp status
```

Docelowo serwery MCP będą osiągane przez brokera działającego pod własną tożsamością gościa
`torio-mcp`, aby credential upstreamu nie istniał pod tożsamością, pod którą agent ma powłokę
([ADR-0022](../adr/0022-mcp-credential-broker.md)). Obecnie Torio provisionuje i weryfikuje granicę
custody potrzebną temu brokerowi, ale nie dostarcza jeszcze daemona ani transportu upstream
([ADR-0027](../adr/0027-mcp-boundary-before-daemon-delivery.md)).

- `install` tworzy nieuprzywilejowaną tożsamość `torio-mcp`, jej magazyn credentiali `0700`, grupę
  `torio-mcp-clients` oraz root-owned katalog policy — po czym **dowodzi** wyniku zamiast ufać exit
  code'om komend, które go wyprodukowały. Idempotentne (`changed:false` przy przebiegu bez zmian),
  nie przyjmuje sekretów i **nie przyznaje niczego** poza członkostwem w grupie klientów, którego
  `hermes` potrzebuje, by otworzyć socket, oraz którego `torio-mcp` potrzebuje, by przekazać grupie
  własny socket. `torio-mcp` nigdy nie trafia do `torio-projects`, a `hermes` nigdy do grupy
  `torio-mcp`; to dwie pomyłki, które unieważniłyby decyzję, zostawiając wszystkie pozostałe checki
  zielone.
- `install` **nie instaluje ani nie aktywuje daemona**. Transport upstream i lifecycle OAuth wymagają
  osobnego zaakceptowanego kontraktu; dopóki go nie ma, release nie publikuje binariów brokera ani
  przekaźnika. Publiczna komenda provisionuje wyłącznie granicę custody, której potrzebuje przyszły
  daemon.
- Policy jest jawnym grantem operatora, więc `install` nie generuje jej ani nie zgaduje. Na świeżym
  gościu pierwszy przebieg może utworzyć root-owned katalog policy i zakończyć się precondition
  z `changed:true`; operator zapisuje co najmniej jeden
  `/etc/torio-mcp/policy.d/<service>.json` jako `root:root 0644`, po czym ponawia `install`.
  Pusta albo niepoprawna policy nie daje pozornie zdrowej granicy z pustym grantem.
- `install` **nie blokuje się** na credentialach zalegających pod profilem Hermesa. Są dokładnie
  tym, co broker ma zlikwidować, ale odmowa instalacji przy ich obecności to zakleszczenie: operator
  nie może zbudować rzeczy, do której ma migrować. Ten ciągły invariant należy do `status`.
- Gdy `hermes` dopiero co dołączył do grupy klientów, `install` raportuje `restart_required` i mówi
  o tym wprost. Długo żyjący proces nie nabywa grupy dlatego, że zmieniła się pod nim baza grup —
  backend trzyma to, z czym wystartował, aż do `torio serve restart`.
- `status` **dowodzi i raportuje; niczego nie naprawia.** Weryfikuje, że tożsamość brokera istnieje,
  że jego magazyn credentiali nie jest czytelny dla nikogo poza nim, że `hermes` może otworzyć socket
  brokera, ale **nie** należy do grupy samego brokera, nie ma sudo ani grup spoza zarządzanego zestawu
  `hermes`, `torio-projects`, `torio-mcp-clients`, oraz że pod profilem Hermesa nie pojawił się żaden
  credential MCP. Nie uruchamia żadnej komendy mutującej.
- Do tego weryfikuje **dwa dokumenty, które przesądzają, po co ta custody w ogóle jest**. Pliki
  policy muszą być `root:root 0644`, zwykłymi plikami (nigdy dowiązaniami) w katalogu, do którego
  nikt poza rootem nie pisze — dokument policy zapisywalny przez agenta unieważnia decyzję,
  zostawiając wszystkie pozostałe checki zielone. Ich zawartość przechodzi ten sam ścisły parser
  co broker. Brak runtime jest poprawnym stanem niezależnie od obecności dormant unit. To obecność
  `/run/torio-mcp`, a nie pliku unit, uruchamia weryfikację daemona. Gdy runtime istnieje, musi istnieć
  również dokładny, aktywny trusted unit; zbiór usług musi być dokładnie równy zbiorowi zwykłych,
  nasłuchujących socketów, a digest policy działającego procesu musi odpowiadać zweryfikowanym
  dokumentom.
  A `mcp_servers` w `config.yaml` musi wskazywać
  wyłącznie na przekaźnik: ten plik jest zapisywalny przez agenta, więc wpis wskazujący gdzie
  indziej to serwer MCP, którego broker nigdy nie zobaczy — bez policy i bez audytu.
- Kontrola `mcp_servers` czyta **jeden kształt YAML-a i odmawia reszty**. Blok w składni inline, z
  kotwicą, aliasem, merge key, tabulacją albo w drugim dokumencie jest raportowany jako drift, a nie
  zgadywany. Nie jest to granica i nie wolno jej tak opisywać: plik należy do tożsamości, którą
  kontrola ogranicza. Wykrywa rozjazd i `hermes mcp add` uruchomiony ręcznie — nie przeciwnika
  piszącego pod różnicę parserów.
- Gość, na którym broker nigdy nie był provisionowany, to **niespełniony precondition (exit 3)**, a
  nie drift. Granica, która przestała trzymać, to **verification failed (exit 6)**. Rozróżnienie jest
  częścią kontraktu: operator, który po prostu nie uruchomił jeszcze instalatora, nie może dostać
  alarmu o złamanej gwarancji, bo nauczy się ignorować ten, który ma znaczenie.
- Wykrycie credentiali pod profilem Hermesa raportuje **liczbę plików i nigdy ich nazw**. Zwykłym
  źródłem tego driftu jest `hermes mcp add` uruchomiony wprost na zarządzanym gościu, który
  uwierzytelnia się w upstreamie i zapisuje token z powrotem pod tożsamość agenta.
- **Zakres narzędzi jest jawny, sekrety nie.** Policy leży w `/etc/torio-mcp/policy.d/<usługa>.json`
  jako `root:root 0644` — czytelna dla agenta i niezapisywalna przez niego. Domyślnie deny; przechodzą
  wyłącznie narzędzia wymienione z nazwy, bez wnioskowania z nazw i bez wzorców.
- Broker **nie broni przed confused deputy w pełni**: wstrzyknięta instrukcja może użyć każdego
  narzędzia przyznanego w policy, także zapisującego, wobec każdego dozwolonego celu. Przyznanie
  zapisu pozostaje jawną decyzją operatora zapisaną w root-owned policy, a nie skutkiem ubocznym
  instalacji.

## Idempotency

Każda komenda zmieniająca stan jest idempotentna, a idempotentny sukces to exit 0:

- `vm init` — zgodna istniejąca instancja: `created:false`. Niezgodna: fail-closed, nigdy recreate.
- `vm start`/`stop`, `serve start`/`stop`/`restart` — pożądany stan jest **re-query'owany** po
  akcji; czysty exit code sam w sobie nie jest postcondition.
- `serve install` — rerun bez zmiany daje `changed:false`.
- `brain init` — pasujący managed state jest sukcesem bez akcji.
- `project add` — rerun po błędzie dokańcza pracę, bo nic nie jest cofane ani czyszczone.
- `project remove` — brakujący albo już zarchiwizowany Hermes Project nie jest błędem.
- `mcp install` — rerun bez zmiany daje `changed:false`, tak jak `serve install`.
