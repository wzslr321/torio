# Architektura Torio V1

> Ten dokument zastępuje czternaście numerowanych dokumentów projektowych
> (`01-product-brief` … `14-local-development`), które opisywały pre-V0 platformę
> workerów. Są pod tagiem `archive/pre-v1`
> ([ADR-0017](adr/0017-pre-v1-exploration-leaves-the-working-tree.md)).
>
> Zakres V1 rozstrzyga
> [ADR-0015](adr/0015-torio-v1-onboarding-projects-and-operator-push.md). Ten
> dokument nie jest streszczeniem kodu — opisuje wyłącznie to, czego z kodu nie
> widać: gdzie przebiega granica zaufania i dlaczego akurat tam.

## Czym Torio jest

Cienkim control plane'em nad Limą, Hermes Agentem i Gitem. Nie jest frameworkiem
agentowym, task queue ani worktree managerem — te warstwy albo należą do
Hermesa, albo świadomie nie istnieją.

## Granica zaufania

Jedna maszyna wirtualna Lima o nazwie `torio`, arm64, `vmType: vz`.
Wszystko, co dotyczy pracy agenta, dzieje się na jej natywnym filesystemie.
Granicą jest brzeg VM, nie proces i nie profil Hermesa.

Dwie konsekwencje ustawiają resztę architektury:

**Brak szerokiego mountu macOS.** `mounts: []` w szablonie gościa. Repozytoria
i Brain leżą na dysku VM, nie w katalogu domowym Maca widzianym przez guest.
Dlatego transport danych (`torio brain import`) jest jednorazowym, ograniczonym
`limactl copy` przez prywatny staging, a nie kopiowaniem po współdzielonej
ścieżce. Profil Hermesa nie jest sandboxem i nie próbujemy go nim uczynić —
izolację daje brzeg VM.

**Tożsamość serwisowa nie jest root-equivalent.** Guest ma dedykowanego
użytkownika `hermes`, który **nie należy do grupy `docker`** — szablon usuwa go
z niej przy provisioningu, a `torio vm bootstrap` weryfikuje nieobecność
i zawodzi closed. Rootful Docker Engine nie jest instalowany. Członkostwo
w `docker` to root na gościu, więc dawałoby agentowi dokładnie tę władzę, której
granica VM ma go pozbawiać.

## Podział własności

| Warstwa | Owner |
| --- | --- |
| Lifecycle Limy, provisioning, weryfikacja gościa | Torio |
| Deklaracja podpiętych projektów (niesekretna) | Torio (`config.json` V2) |
| Ścieżki workspace'ów i vaulta | Torio (wyprowadzane, nie podawane) |
| Sesja operatora z write capability | Torio (`project shell`) |
| Model execution, sesje, pamięć, profile | Hermes Agent |
| Rejestr projektów po stronie agenta | Hermes Agent (`hermes project` CLI) |
| Kanban, dispatch, retry | Hermes Agent |

Torio nie zapisuje do wewnętrznego stanu Hermesa. Rejestracja projektu idzie
przez publiczne `hermes project`, nie przez `~/.hermes/kanban.db`.

## Ścieżki danych

Trzy katalogi pod `/home/hermes`, celowo rozdzielone:

- `/home/hermes/.hermes` — **profil i stan aplikacyjny** Hermesa
  (`HermesProfilePath`), `hermes:hermes 0750`;
- `/home/hermes/brain` — **Second Brain**, prywatny vault Markdown
  (`HermesBrainPath`), `hermes:hermes 0750`;
- `/home/hermes/projects` — **workspace'y**, `hermes:torio-projects 2770` (setgid).

Rozdzielenie pierwszych dwóch jest decyzją, nie kosmetyką: przed V1 kod nazywał
katalog stanu aplikacji „Knowledge Base", co mieszało prywatne notatki
z artefaktami Hermesa. `torio vm bootstrap` sprawdza ownership i mode każdej
z tych ścieżek.

Setgid na `projects` jest tym, co pozwala operatorowi i tożsamości `hermes`
pracować na tym samym checkoutcie: oba konta należą do grupy `torio-projects`,
więc pliki tworzone przez jedno są zapisywalne dla drugiego. Bez tego sesja
operatora zostawiałaby checkout, w którym agent nie może dalej pracować.

**Workspace path nie jest wejściem.** Wyprowadza się z id projektu jako
`/home/hermes/projects/<id>`. Użytkownik podaje id i remote; ścieżki nie podaje
nigdy, a config obiektu z polem `path` nie przyjmuje.

## Skąd bierze się prawo zapisu do origin

To jest właściwa treść V1 i jedyne miejsce, gdzie architektura mówi „nie" czemuś,
co byłoby wygodne.

Persistentny Hermes ma do origin wyłącznie **read**. Nie ma tokenu, nie
dziedziczy `SSH_AUTH_SOCK`, a szablon gościa ustawia `ssh.forwardAgent: false`
globalnie.

Write capability istnieje wyłącznie w czasie trwania jednej interaktywnej sesji:

```text
torio project shell <id>
  → ssh -A -t lima-torio /usr/local/bin/torio-project-shell /home/hermes/projects/<id>
  → zwykłe komendy Git w tożsamości operatora, w grupie torio-projects
  → exit — forwarded agent znika razem z sesją
```

Helper na gościu jest `root:root 0755` i materializuje go szablon Limy przy
każdym starcie. Bootstrap tylko dowodzi jego stanu i zgłasza drift zamiast go
naprawiać. Powód jest wprost: przez tę ścieżkę przechodzi przekazany agent
operatora, więc nic, co `hermes` albo operator mogą nadpisać, nie może na niej
leżeć.

Torio nie przechowuje credentiali Git write, nie automatyzuje push, merge ani
release, i nie wykonuje test-pusha, żeby cokolwiek udowodnić. Remote z wbudowanym
hasłem, tokenem, query albo fragmentem jest odrzucany.

## Second Brain w projektach

Brain jest osobnym Hermes Project do bezpośredniej pracy. Dostęp z pozostałych
projektów daje **globalny skill `torio-brain`** — retrieval przez file/search
tools, nie wstrzyknięcie treści.

Wybór jest świadomy. Bulk injection całego vaulta do system promptu każdego
projektu unieważniałby prompt cache przy każdej zmianie notatki i przenosiłby
prywatne treści do kontekstu projektów, które ich nie potrzebują. Dodanie
`/home/hermes/brain` jako folderu każdego projektu ma ten sam skutek i jest
zakazane.

## Backend i dostęp z Maca

`hermes serve` biegnie jako **user systemd service** na gościu, związany
z `127.0.0.1:9119`. Loopback gościa, nie interfejs VM. Z Maca dostęp idzie
wyłącznie przez tunel SSH, który zestawia operator — Torio żadnego tunelu nie
otwiera i żadnej sesji czatu nie zaczyna.

## Gdzie Torio się kończy

Świadomie nie istnieją: agent loop, drugi Kanban, dispatcher, queue, retry
engine, per-task worker containers, fresh verifier, automatyczny
merge/push/release, secret broker, domenowy egress allowlist, import hostowego
checkoutu i szeroki mount katalogu macOS.

Ta lista nie jest roadmapą. Pierwsza wersja tego repozytorium zaprojektowała
większość z tych rzeczy i żadnej nie dostarczyła; materiał jest pod
`archive/pre-v1` i nie wraca do implementacji.
