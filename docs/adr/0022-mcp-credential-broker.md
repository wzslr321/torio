<!--
AI-Provenance:
  model: Claude Opus 5 (1M context)
  harness: Claude Code
-->

# ADR-0022: MCP przez brokera pod osobną tożsamością gościa

- Status: Proposed
- Date: 2026-07-29
- Dotyczy: `internal/lima`, `internal/cli`, nowy komponent gościa `torio-mcp`
- Powiązane: [ADR-0003](0003-lima-trust-boundary.md) (granica zaufania to brzeg
  VM), [ADR-0015](0015-torio-v1-onboarding-projects-and-operator-push.md)
  (tożsamość serwisowa, zakaz `docker`, granica credentials),
  [ADR-0016](0016-normative-documents-are-corrected-not-archived.md) (korekta
  `cli.md` wymuszona tą decyzją)

## Context

MCP jest tym, przez co agent w pudełku sięga do Slacka, Jiry i Confluence. Bez
tego Torio jest środowiskiem, w którym nie da się pracować. Pytanie nie brzmi
więc „czy MCP", tylko „na jakich warunkach".

Warunki, jakie daje dziś sam Hermes, sprawdzone w źródłach i na żywej instancji
`torio` (2026-07-28/29):

- Tokeny OAuth serwerów MCP leżą w `$HERMES_HOME/mcp-tokens/`, konfiguracja
  serwerów w `$HERMES_HOME/config.yaml`, a credentiale providera inferencji w
  `$HERMES_HOME/auth.json`. Wszystko własnością `hermes`, `0600`.
- Agent działa jako `hermes` z `terminal.backend: local`, czyli ma powłokę pod
  tym samym uid.
- Hermes ma denylistę odczytu tych plików dla `read_file` i sam ją opisuje w
  `agent/file_safety.py` tak: *„**This is NOT a security boundary.** The terminal
  tool runs as the same OS user with shell access; the agent can still `cat
  auth.json` […] and exfiltrate the file."*
- Jedyny mechanizm least privilege, jaki Hermes oferuje dla MCP, to
  `mcp_servers.<nazwa>.tools.include` w `config.yaml`. Ten plik **nie** jest na
  write-denyliście, a `HERMES_WRITE_SAFE_ROOT` jest nieustawiony
  (`get_safe_write_roots()` zwraca pusty zbiór). Allowlista jest więc wartością
  domyślną, nie kontrolą — ograniczany może ją sobie przepisać.

Z tego wynikają dwie osobne rzeczy, które łatwo pomylić w jedną nierozwiązywalną.
**Custody** — czego agent może dotknąć. **Capability** — co może zrobić z
przyznanym dostępem. Konfiguracja Hermesa nie rozstrzyga żadnej z nich: token
jest wykradalny, bo leży pod uid agenta, a zakres jest przepisywalny, bo leży w
pliku agenta.

AGENTS.md §5.2 mówi już wprost, że profil Hermesa nie jest sandboxem i granicą
jest brzeg VM. Ta decyzja nie podważa tego zdania — dociąga je do końca. Skoro
profil nie jest sandboxem, to nic wartościowego nie powinno w nim leżeć.

Warunek, który czyni rozwiązanie możliwym, jest już spełniony: `hermes` nie ma
sudo (`User hermes is not allowed to run sudo on lima-torio`) i należy wyłącznie
do grup `hermes` i `torio-projects`. Istnieje więc poziom, do którego agent nie
sięga.

## Decision

**Credentiale MCP przestają istnieć pod tożsamością agenta. Hermes rozmawia z
serwerami MCP wyłącznie przez brokera działającego pod osobnym uid.**

1. **Tożsamość.** Gość dostaje nieuprzywilejowany uid `torio-mcp` z własnym
   home `/home/torio-mcp` (`0700 torio-mcp:torio-mcp`), bez sudo i poza grupą
   `torio-projects`. `hermes` nie może przeczytać tego katalogu.

2. **Custody.** Wszystkie sekrety upstreamu — tokeny OAuth, client secrets,
   klucze API — należą do `torio-mcp` i istnieją wyłącznie w jego home.
   Po migracji `$HERMES_HOME/mcp-tokens/` musi być pusty, a brak tego stanu jest
   driftem.

3. **Transport.** Broker jest długo żyjącą usługą systemową i słucha na unix
   socketach `/run/torio-mcp/<usługa>.sock` (`0660 torio-mcp:torio-mcp-clients`).
   `hermes` należy do `torio-mcp-clients` i to jest cały przyznany mu przywilej:
   *wolno ci nawiązać połączenie z brokerem*. Po stronie Hermesa wpis wskazuje na
   cienki, całkowicie nieuprzywilejowany przekaźnik stdio↔socket:

   ```yaml
   mcp_servers:
     atlassian:
       command: /usr/local/bin/torio-mcp-connect
       args: ["atlassian"]
   ```

   Przekaźnik nie trzyma żadnego sekretu i nie jest elementem kontroli — agent
   może gadać z socketem bezpośrednio i nic to nie zmienia. Kontrolą jest
   właściciel socketu i to, że tożsamość rozmówcy ustala kernel (`SO_PEERCRED`),
   a nie okazany sekret.

   **Czego `SO_PEERCRED` tutaj nie kupuje.** Podaje uid procesu, który się
   połączył — czyli więcej niż samo członkostwo w grupie, bo nazywa konkretną
   tożsamość. Ale **nie odróżnia klienta MCP Hermesa od czegokolwiek innego
   działającego pod tym samym uid**: jednolinijkowiec agenta wygląda identycznie.
   Żadna policy „per wołający" nie może się na tym oprzeć i nie wolno tak tego
   opisywać w dokumentacji operatora. To jest dowód tożsamości uid, nie programu.

   Nazwa usługi jest ograniczona do 32 bajtów, i to ograniczenie jest nośne, a
   nie kosmetyczne: adres unix socketu mieści się w `sun_path` (~104 bajty), a
   `/run/torio-mcp/` plus najdłuższa dopuszczona nazwa plus `.sock` to 52 bajty.
   Adres zbyt długi jest więc nieosiągalny konstrukcyjnie, zamiast wywalać się
   na `connect()`. Obie strony — broker wiążący socket i przekaźnik go szukający
   — muszą trzymać tę samą regułę; nazwa przyjęta przez jedną i odrzucona przez
   drugą to socket, do którego nic nie dotrze.

4. **Policy jest jawna, sekrety nie.** Zakres narzędzi leży w
   `/etc/torio-mcp/policy.d/<usługa>.json`, `root:root 0644` — **czytelny dla
   agenta i niezapisywalny przez niego**. Format jest JSON-em, a nie YAML-em,
   bo `internal/config` ma już fail-closed idiom schematu (jeden dokument,
   `DisallowUnknownFields`, odrzucenie nieznanej wersji bez migracji), a
   dokładanie parsera YAML do ścieżki zaufanej policy byłoby zależnością w
   najgorszym możliwym miejscu. Domyślnie deny: broker przepuszcza
   wyłącznie narzędzia wymienione z nazwy, bez wnioskowania z nazw ani wzorców.
   Każdy wpis niosący zapis jest oznaczony jawnie, żeby raport mógł podać liczbę
   przyznanych narzędzi zapisujących.

5. **Audyt.** Broker loguje każde wywołanie: znacznik czasu, usługa, nazwa
   narzędzia, uid wołającego, decyzja allow/deny. **Nigdy argumenty ani treść
   odpowiedzi** — inaczej zawartość Jiry i Confluence wyciekłaby do logów. To ta
   sama reguła, którą `cli.md` nakłada na Braina.

6. **Weryfikacja fail-closed**, w idiomie reszty Torio — dowodzona, nie zakładana:
   istnienie uid/gid i tryb home; członkostwo `hermes` w `torio-mcp-clients`
   i **brak** członkostwa w `torio-mcp`; pustość `$HERMES_HOME/mcp-tokens/`;
   brak wpisu `mcp_servers` wskazującego gdziekolwiek poza przekaźnik; owner,
   grupa i mode socketu; parsowalność i własność plików policy; zgodność
   raportowanego zakresu z plikami.

   **Sam socket obecny to za mało.** Plik socketu zostawiony przez brokera,
   który się wywrócił, przechodzi każdy test właściciela, grupy i trybu, a
   odrzuca każde połączenie. Weryfikacja musi traktować obecny-lecz-martwy
   socket jako drift, inaczej `status` powie „granica trzyma" o maszynie, na
   której nic nie działa. Rozróżnienie jest już wyrażone po stronie klienta:
   brak socketu to inny błąd niż `ECONNREFUSED`.

   Unit brokera jest walidowany
   `systemd-analyze verify` **przed** aktywacją, tak jak w `serve install`.
   Każdy drift to exit 6 i stabilne markery, nigdy cicha naprawa.

7. **Torio nadal nie dotyka sekretów.** Logowanie do usługi jest interaktywną
   akcją operatora wykonywaną jako `torio-mcp`; broker sam mennicuje i zapisuje
   token do swojego home. Torio go nie widzi, nie przechowuje i nie pośredniczy —
   granica z ADR-0015 obowiązuje bez zmian.

8. **Upstream jest konfigurowalny.** Broker kieruje ruch przez jawnie wskazany
   endpoint, a nie prosto w internet, żeby późniejsza decyzja o kontroli egressu
   mogła wstawić proxy bez przeprojektowania.

## Co ta decyzja rozstrzyga o write accessie

**Nie zakazujemy zapisu. Czynimy go jawnym, przypisywalnym i odwoływalnym.**

Broker nie broni przed confused deputy. Wstrzyknięta instrukcja może użyć każdego
przyznanego narzędzia wobec każdego dozwolonego celu — i żadna ilość weryfikacji
tego nie zmieni, bo z punktu widzenia brokera to poprawne wywołanie.

Wartość leży gdzie indziej: w każdej chwili wiadomo czarno na białym, co jest
przyznane. Zakres jest wyliczalny, maszynowo czytelny i zweryfikowany, a nie
wywnioskowany z tego, co kiedyś wyklikał instalator. Bywa, że write access do
MCP jest na tyle wartościowy i na tyle bezpieczny, że warto go wziąć — wtedy
bierzemy go świadomie i bierzemy za niego odpowiedzialność. Rolą Torio jest
uczynić ryzyko czytelnym, nie odebrać wybór.

Dlatego pkt 4 wymaga jawnego oznaczenia narzędzi zapisujących, a pkt 5 audytu bez
treści: żeby „przyznaliśmy zapis do Jiry" było zdaniem, które ktoś kiedyś napisał
i podpisał, a nie skutkiem ubocznym instalacji.

## Consequences

- Torio przestaje być wyłącznie control plane nad Limą i zaczyna dowozić usługę
  gościa oraz tożsamość systemową. To realne rozszerzenie zakresu, przyjęte
  świadomie; kształt jest ten sam co `serve install` — deterministyczny unit,
  walidacja przed aktywacją, idempotencja, drift raportowany.
- **`hermes mcp add` przestaje być wspieraną drogą na zarządzanym gościu.**
  Zapisuje credentiale do `$HERMES_HOME` i omija brokera, więc jego użycie jest
  driftem, który weryfikacja musi wykryć i zaraportować.
- **Istniejące połączenie Atlassian wymaga migracji.** Skonfigurowane 2026-07-28
  przez `hermes mcp add`, 40 narzędzi, wszystkie włączone, tokeny w
  `$HERMES_HOME/mcp-tokens/`. Musi zostać odtworzone przez brokera z jawną
  policy, a stare tokeny usunięte i unieważnione po stronie Atlassiana.
- **AGENTS.md §5.8 i `cli.md` są dziś nieprawdziwe.** „Write capability pochodzi
  wyłącznie z sesji `torio project shell`" było prawdą o gicie i przestało być
  prawdą w ogóle. Zgodnie z ADR-0016 to defekt do poprawy: invariant zawęża się
  do write'u wobec origin, a MCP staje się osobnym, jawnie opisanym kanałem
  zdolności.
- Członkostwo `hermes` w `torio-mcp-clients` jest przyznaniem zdolności i musi
  być weryfikowane jak każde inne. Nie jest odpowiednikiem grupy `docker`
  z §5.3: tamta jest równoważna rootowi, ta pozwala wyłącznie otworzyć połączenie
  z usługą, która i tak waliduje każde żądanie.
- **Custody credentiali providera inferencji zostaje nierozwiązane.**
  `$HERMES_HOME/auth.json` trzyma dziś żywy credential OAuth (Codex, odświeżony
  2026-07-28 17:32) i jest wykradalny dokładnie tak samo jak tokeny MCP przed tą
  decyzją. Broker tego nie dotyka. To pilniejszy problem niż MCP i wymaga
  osobnego ADR-a.
- **Wyniesienie danych zostaje nierozwiązane.** Agent legalnie otrzymuje treść
  przez dozwolone narzędzia odczytu i może ją wysłać dowolnie, bo ma egress.
  Zamyka to dopiero kontrola egressu — osobny ADR, sprzężony z domknięciem
  historii obrazu, bo dzisiejszy `vm bootstrap` pobiera `install.sh` i wykonuje
  `apt-get`.

## Rejected

- **`mcp_servers.<n>.tools.include` jako mechanizm least privilege.** Leży w
  pliku, który agent może nadpisać, bo `config.yaml` nie jest write-denied,
  a `HERMES_WRITE_SAFE_ROOT` jest pusty. Wartość domyślna, nie kontrola.
- **Denylista odczytu Hermesa jako granica.** Odrzucona na podstawie jej własnej
  dokumentacji, cytowanej w Context. Kontrola i obejście na tym samym poziomie
  uprawnień to nie jest granica.
- **`HERMES_WRITE_SAFE_ROOT` jako rozwiązanie.** Podnosi koszt przypadkowego
  zapisu przez file tools i nic więcej — powłoka omija to tak samo. Może zostać
  ustawiony jako higiena, ale nie wolno go opisywać jako granicy.
- **Broker pod uid `hermes`.** Znosi cały sens: token wraca pod tożsamość, przed
  którą go chronimy.
- **Loopback TCP z bearer tokenem.** Wprowadza z powrotem sekret pod uid agenta,
  dokłada port i drugą warstwę uwierzytelniania do pomylenia. Unix socket
  załatwia tożsamość przez kernel, bez sekretu.
- **stdio przez `sudo -u torio-mcp`.** Działa, ale wręcza uid agenta prymityw
  przejścia uprawnień i przenosi bezpieczeństwo do reguł sudoers, które trzeba
  pinować co do argv i w których łatwo o cichy błąd (`SETENV`, wildcard,
  przepuszczone env). Socket nie potrzebuje żadnej reguły sudo.
- **Izolacja brokera kontenerem.** §5.3 zakazuje rootful Dockera dla `hermes`,
  a V1 nie instaluje żadnego container runtime. Osobny uid daje tu potrzebną
  separację bez nowej zależności.
- **Przechwytywanie TLS w gościu (MITM CA).** Pozwoliłoby filtrować treść, ale
  wstawienie CA do zaufanych to lekarstwo gorsze od choroby.
- **Zaufanie do przekaźnika `torio-mcp-connect` jako elementu kontroli.** Świadomie
  odrzucone: agent może pominąć przekaźnik i gadać z socketem wprost. Dlatego
  cała policy jest egzekwowana przez brokera, a przekaźnik jest wyłącznie
  adapterem protokołu.
