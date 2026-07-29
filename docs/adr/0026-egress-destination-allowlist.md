<!--
AI-Provenance:
  model: Claude Opus 5 (1M context)
  harness: Claude Code
-->

# ADR-0026: Allowlista celów egressu

- Status: Proposed
- **Wstrzymany: sprzeczny z `AGENTS.md` §4.** Patrz „Blokada" poniżej. Do czasu
  jej zdjęcia nic na tej decyzji nie powstaje.
- Date: 2026-07-29
- Dotyczy: `internal/lima`, `internal/cli`, ruleset nftables gościa, registry
  projektów
- Powiązane: [ADR-0024](0024-guest-egress-control.md) (decyzja, z której ta
  została wydzielona), [ADR-0022](0022-mcp-credential-broker.md) (nazywa
  wyniesienie danych jako nierozwiązane),
  [ADR-0016](0016-normative-documents-are-corrected-not-archived.md)

## Blokada

`AGENTS.md` §4 wymienia pod „Torio NIE MOŻE implementować":

> secret managera klasy Vault ani **domenowego network allowlistu**

To jest dokładnie przedmiot tej decyzji. `AGENTS.md` §3 stawia ten plik na
pierwszym miejscu wśród źródeł prawdy, a `AGENTS.md:3` każe przy sprzeczności
zatrzymać pracę i zgłosić konflikt zamiast rozstrzygać go zgadywaniem.

Konflikt został zgłoszony operatorowi 2026-07-29. Rozstrzygnięcie: część
kluczowana po uid idzie dalej jako [ADR-0024](0024-guest-egress-control.md), a
allowlista celów zostaje wydzielona tutaj i **czeka**. Decyzja o §4 nie jest
decyzją implementera.

Dwa możliwe wyjścia, oba należące do operatora:

1. **Zmiana §4** — skreślenie „domenowego network allowlistu" z listy zakazów,
   z uzasadnieniem w tym ADR-ze. Argument za: §4 spisano, gdy Torio było
   wyłącznie control plane nad Limą. ADR-0022 i ADR-0023 to zmieniły — Torio
   trzyma dziś credentiale za agenta, więc to, dokąd gość wychodzi, przestało
   być dodatkiem, a stało się częścią tego, za co Torio wzięło odpowiedzialność.
2. **Odrzucenie tej decyzji** — §4 zostaje, a `docs/03-architecture.md` i
   ADR-0022 zachowują zdanie, że wyniesienie danych jest nierozwiązane, bez
   obietnicy, że kiedyś przestanie takie być.

Trzeciego wyjścia nie ma. Zbudowanie tego bez zdjęcia blokady byłoby cichym
rozstrzygnięciem sprzeczności przez implementera, czyli tym, czego `AGENTS.md`
zakazuje wprost.

## Context

ADR-0024 domyka, **kto** wychodzi z gościa. Nie domyka **dokąd**: `hermes` ma
przepustkę bez ograniczenia adresata, więc agent prowadzony wstrzykniętą
instrukcją wysyła dane dokładnie tak, jak przed tamtą decyzją.

Ta decyzja jest jedyną częścią pracy nad egressem, która dotyka wyniesienia
danych. Reszta — custody z ADR-0022 i ADR-0023, tożsamości z ADR-0024 — pilnuje,
żeby agent nie zdobył trwałej zdolności. Żadna z nich nie pilnuje, żeby to, co
agent legalnie przeczytał, nie wyszło.

Wartość trzeba przy tym wycenić uczciwie, bo model zagrożeń jest tu wąski.
Kontrola celów jest wymierzona w **prompt injection i zdezorientowanego agenta**,
nie w agenta świadomie przeciwnego. Wobec tego drugiego nie ma szans:
[ADR-0024](0024-guest-egress-control.md) pokazuje, że DNS pozostaje
dwukierunkowym kanałem pod cudzym uid, a każde dopuszczone narzędzie zapisujące
MCP jest confused deputy z legalnym wyjściem.

## Decision (szkic — nie do implementacji przed zdjęciem blokady)

**Zbiór dozwolonych celów jest jawny, wyliczalny i wyprowadzany z rzeczy, które
gość i tak deklaruje.**

1. **Filtrowanie po nazwie musi się odbywać tam, gdzie nazwa jest widoczna** —
   czyli na proxy po SNI/CONNECT, do którego kernel wymusza ruch, a nie na
   regule adresowej.
2. **Źródłem allowlisty są `projects[].remote`, endpoint inferencji i endpointy
   upstreamów MCP.** Każdy z nich jest już gdzieś zadeklarowany; allowlista
   utrzymywana ręcznie obok nich rozjedzie się z nimi.
3. **Odmowa musi mówić, czego brakuje.** Nowy provider, nowy MCP czy nowy
   projekt wymagają jawnej zmiany — i to jest cecha, nie koszt, dopóki komunikat
   nazywa brakujący cel.

## Problemy, które trzeba rozwiązać przed przyjęciem

Wszystkie ustalone przy pracy nad ADR-0024 i przeniesione tutaj w całości.

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

**Sprzężenie z historią obrazu.** `vm bootstrap` pobiera dziś `install.sh` z
`hermes-agent.nousresearch.com` i wykonuje `apt-get` wobec `ports.ubuntu.com`
**po zwykłym HTTP**. Albo te hosty zostają na allowliście na stałe — czyli
mirror apt jako trwała powierzchnia supply-chain — albo przenoszą się do
zapieczonego obrazu. To jest realne ograniczenie kolejności prac.

## Rejected

- **Allowlista po adresach IP.** CDN-y rotują adresy; reguła psuje się po cichu i
  w najgorszym momencie. Filtrowanie po nazwie musi się odbywać tam, gdzie nazwa
  jest widoczna.
- **Przechwytywanie TLS w gościu (MITM CA).** Pozwoliłoby filtrować treść kosztem
  wstawienia własnego CA do zaufanych. Lekarstwo gorsze od choroby.
- **Opisanie tego kiedykolwiek jako szczelnego.** DNS i confused deputy zostają
  otwarte niezależnie od tego, jak dobra będzie allowlista. Wolno powiedzieć, że
  zbiór celów jest wyliczalny i egzekwowany; nie wolno powiedzieć, że dane nie
  wychodzą.

## Niezweryfikowane

Hosty PyPI/npm/uv-python — wywnioskowane z artefaktów w drzewie, a nie odczytane
z konfiguracji ani zaobserwowane na łączu. Faktyczny host endpointu inferencji.
