<!--
AI-Provenance:
  model: Claude Opus 5 (1M context)
  harness: Claude Code
-->

# ADR-0025: Zapis przez MCP wymaga otwartego okna operatora

- Status: Proposed
- Date: 2026-07-29
- Supersedes: fragment [ADR-0022](0022-mcp-credential-broker.md), sekcja „Co ta
  decyzja rozstrzyga o write accessie" — zdanie *„Wstrzyknięta instrukcja może
  użyć każdego przyznanego narzędzia wobec każdego dozwolonego celu"*. Reszta
  ADR-0022 obowiązuje bez zmian.
- Dotyczy: `internal/mcpbroker`, `internal/lima`, `internal/cli`,
  `cmd/torio-mcp-broker`
- Powiązane: [ADR-0015](0015-torio-v1-onboarding-projects-and-operator-push.md)
  (sesja operatora jako jedyny nośnik write capability),
  [ADR-0016](0016-normative-documents-are-corrected-not-archived.md)

## Context

ADR-0022 uczynił zakres MCP jawnym: policy leży w `/etc/torio-mcp/policy.d/`,
jest root-owna, czytelna dla agenta i niezapisywalna przez niego, a każde
narzędzie niosące zapis jest oznaczone. To odpowiada na pytanie **co** agent może
zrobić. Nie odpowiada na **kiedy** — przyznane narzędzie zapisujące jest
wołalne zawsze, w nieskończoność, bez udziału człowieka.

Ta różnica ma znaczenie dokładnie wobec zagrożenia, które jest realne i częste.
Model zagrożeń dla tej pracy to **prompt injection i zdezorientowany agent**, nie
agent świadomie przeciwny. Zatruty ticket w Jirze albo strona w Confluence, którą
agent ma polecenie przeczytać, jest zwykłym wejściem — a instrukcja w niej
zawarta trafia na dokładnie te same przyznane narzędzia, których agent używa do
pracy.

Wobec takiego wejścia jawność zakresu jest warta tyle, że po fakcie da się
powiedzieć, co się mogło stać. Nic nie ogranicza momentu.

Torio ma już odpowiedź na ten sam kształt problemu po stronie gita: zdolność
zapisu wobec origin pochodzi wyłącznie z sesji `torio project shell` i kończy się
razem z nią (`AGENTS.md` §5.8). MCP dostał w ADR-0022 status osobnego kanału
zdolności (§5.8a), ale bez odpowiednika tego ograniczenia.

## Decision

**Narzędzie oznaczone w policy jako zapisujące wymaga dwóch niezależnych
warunków: grantu w dokumencie policy oraz otwartego, ograniczonego w czasie okna
zapisu dla tej usługi.**

1. **Dwa warunki, żaden nie zastępuje drugiego.** Grant mówi, które narzędzia w
   ogóle istnieją dla tej usługi; okno mówi, że teraz wolno użyć tych, które
   piszą. `torio mcp allow-write` **nie przyznaje narzędzi** — odblokowuje
   wyłącznie te, które policy już wymienia jako zapisujące. Przyznanie pozostaje
   edycją root-ownego pliku.

2. **Okno jest plikiem w home brokera, nie stanem demona.** `0700
   torio-mcp:torio-mcp`, jeden plik na usługę, treścią jest instant zamknięcia w
   RFC 3339. Plik przeżywa restart demona, daje się obejrzeć `ls`-em i nie
   wymaga żadnego protokołu. Leży tam, gdzie tożsamość agenta nie sięga: `hermes`
   nie ma sudo i nie wejdzie do tego katalogu, więc **nic, co robi agent, nie
   otwiera ani nie przedłuża okna**.

3. **Okno jest czytane przy każdym wywołaniu.** Wygaśnięcie nie jest zdarzeniem,
   które ktoś przysyła — jest mijającym terminem. Demon, który wczytałby okno
   raz, trzymałby otwarte drzwi po jego końcu.

4. **Każda niepewność to okno zamknięte.** Brak katalogu, brak pliku, plik
   nieczytelny, treść, której ten kod nie zapisał, nazwa usługi spoza reguły —
   wszystko daje zamknięte. „Zamknięte" i „nie wiadomo" muszą być tą samą
   odpowiedzią, bo inaczej ktoś kiedyś musiałby wybrać, które z nich znaczy
   „przepuść".

5. **Wygaśnięcie jest wyłączające.** Okno, którego instant równa się teraz, jest
   zamknięte. Granica otwarta w momencie własnego terminu to okno, które nigdy
   się nie kończy.

6. **Jedno wywołanie to jedna linia audytu.** Werdykt okna jest składany z
   werdyktem policy **przed** zapisem rekordu. Zapisanie „allow" z policy, a
   zaraz po nim „deny" z okna, dla jednego odrzuconego wywołania, dałoby log
   sam ze sobą sprzeczny — a czytający po fakcie musiałby zgadywać, która połowa
   była prawdziwa.

7. **Okno bez końca jest usage errorem.** `--for` bez dodatniej wartości kończy
   się exit 2. Domyślne 15 minut jest dość długie, żeby nie walczyć z narzędziem
   w trakcie pracy, i dość krótkie, żeby instrukcja wstrzyknięta później trafiła
   w zamknięte drzwi.

## Dlaczego oznaczenie zapisu jest ręczne i musi takie zostać

Okno czyni oznaczenie `writes` w policy nośnym: przed tą decyzją mark tylko
zasilał liczbę w raporcie, teraz bramkuje zdolność. Trzeba więc zapisać, skąd on
pochodzi — i dlaczego nie może pochodzić z serwera.

**Protokół MCP nie daje weryfikowalnego sposobu, by serwer zadeklarował
narzędzie jako zapisujące.** Definicja `Tool` to `name`, `title`,
`description`, `icons`, `inputSchema`, `outputSchema`, opcjonalne `annotations`
i opcjonalne `_meta`. Wszystkie właściwości `ToolAnnotations` —
`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint` — są
normatywnie **hintami**. Serwer może podać `readOnlyHint: true` i kasować dane;
protokół nie ma czym tego wykryć. Sama specyfikacja mówi klientom, że muszą
traktować adnotacje jako niezaufane, dopóki nie pochodzą z serwera, który klient
uznał za zaufany.

**Właściwe zdanie to „protokół nie daje weryfikowalności", nigdy „specyfikacja
zabrania".** Różnica jest nośna: spec zostawia furtkę dla serwera, któremu
klient postanowił zaufać, więc mocniejsza forma byłaby nadinterpretacją, a
decyzja stojąca na nadinterpretacji upada razem z nią. Wolno powiedzieć, że
zaufanie do adnotacji jest decyzją po stronie klienta — i że my jej nie
podejmujemy.

Defaulty adnotacji są pesymistyczne (`readOnlyHint: false`,
`destructiveHint: true`, `openWorldHint: true`), więc narzędzie bez adnotacji
jest z definicji „potencjalnie destrukcyjne". To potwierdza kształt deny-by-
default z wyliczeniem nazw, ale **nie skraca pracy operatora**: brak adnotacji i
`readOnlyHint: false` są nierozróżnialne, a oba wymagają decyzji człowieka. Te
defaulty są przy tym komentarzem w schemacie, nie słowem kluczowym `default`
egzekwowanym przez walidację — konwencja interpretacyjna, nie mechanizm.

Z tego wynikają dwa zakazy, mocniejsze niż samo „oznaczamy ręcznie":

1. **Adnotacja nie może być fallbackiem** dla narzędzia nieoznaczonego w policy.
   Przeniosłoby to decyzję o oknie zapisu na stronę, której kernel nie
   uwierzytelnia — czyli dokładnie odwrotnie niż ADR-0022, który cały opiera się
   na tożsamości ustalanej przez kernel, a nie na okazanym twierdzeniu.
2. **Generator policy z adnotacji nie ma sensu nawet jako pomoc.** Pokrycie
   adnotacji w ekosystemie jest nierówne i wiele serwerów produkcyjnych nie
   wysyła ich wcale, więc generator wyprodukowałby albo pusty plik, albo
   wszystko oznaczone jako zapis. Nie oszczędza pracy tam, gdzie miał.

Jedyne dopuszczalne zastosowanie adnotacji jest **detektorem rozjazdu**:
upstream mówi `readOnlyHint: false`, a policy przyznaje to narzędzie jako
odczyt → alert do operatora. Nigdy automatyczna zmiana decyzji.

Ustalone na tekście specyfikacji i schematu 2026-07-29; brzmienie o niezaufanych
adnotacjach jest identyczne w każdej sprawdzonej rewizji, więc twierdzenie nie
jest przypięte do jednej. **Pozostała powierzchnia klienta MCP w brokerze nie
była przy tej okazji weryfikowana** i może być zaprojektowana wobec starszego
kształtu protokołu — patrz `HANDOFF-mcp-broker.md`.

## Co to zmienia w ADR-0022

ADR-0022 stwierdzał, że wstrzyknięta instrukcja może użyć każdego przyznanego
narzędzia wobec każdego dozwolonego celu. Po tej decyzji zdanie brzmi:

> Wstrzyknięta instrukcja może użyć każdego przyznanego narzędzia **odczytu**
> wobec każdego dozwolonego celu, a narzędzia zapisu — **wyłącznie w oknie, które
> operator właśnie otworzył**.

Reszta tamtej sekcji obowiązuje bez zmian, łącznie z jej tezą główną: nie
zakazujemy zapisu, czynimy go jawnym, przypisywalnym i odwoływalnym. Okno dokłada
do tej listy „i ograniczonym w czasie".

## Czego okno nie załatwia

To jest zawężenie, nie domknięcie, i opisywanie go inaczej byłoby dokładnie tym
błędem, przed którym ADR-0022 przestrzega.

- **W trakcie otwartego okna nie zmienia nic.** Operator otwiera okno, bo chce
  wykonać pracę wymagającą zapisu — czyli w momencie, w którym agent czyta
  najwięcej cudzej treści. Instrukcja wstrzyknięta wtedy zadziała.
- **Okno da się wyprosić.** Agent, który napisze „nie mogę dokończyć, otwórz
  okno zapisu", dostanie je od operatora, który i tak zamierzał je otworzyć.
  Kontrola jest wobec czasu, nie wobec perswazji.
- **`allow-write` nie jest idempotentne** i nie da się takim uczynić: każde
  uruchomienie przesuwa termin, bo to jest odnowienie, a nie stan do
  uzgodnienia. `docs/contracts/cli.md` musi to wymienić jako jawny wyjątek od
  swojej reguły idempotencji, zamiast pozwolić, by reguła była czytana jako
  nieprawdziwa.
- **Okno jest per usługa, nie per narzędzie.** Otwarte dla Atlassiana odblokowuje
  wszystkie zapisujące narzędzia Atlassiana. Precyzja per narzędzie byłaby
  ograniczeniem, którego operator nie utrzyma w głowie w trakcie pracy, a grant
  i tak wymienia narzędzia z nazwy.

## Consequences

- Broker czyta plik przy każdym wywołaniu `tools/call` na przyznane narzędzie
  zapisujące. To jeden `open`/`read` na wywołanie, w zamian za brak stanu do
  unieważnienia.
- Okno przeżywa restart brokera. To jest cecha: restart demona nie jest zdarzeniem
  bezpieczeństwa i nie powinien ani otwierać, ani zamykać niczego po cichu.
- Zegar gościa staje się częścią granicy. Przestawienie go do tyłu przedłuża
  okno; `systemd-timesyncd` działa i jest w inwentarzu przepustek ADR-0024, ale
  nikt tego nie dowodzi. Do rozstrzygnięcia razem z weryfikacją unitu.
- Odmowa z powodu zamkniętego okna musi być odróżnialna od odmowy z powodu braku
  grantu. Lekarstwa nie mają ze sobą nic wspólnego, a komunikat wysyłający
  operatora do edycji policy, gdy wystarczyło otworzyć okno, kosztuje go
  popołudnie.

## Rejected

- **Zatwierdzanie per wywołanie.** Operatora nie ma przy trzeciej nad ranem tak
  samo jak przy oknie, a pytanie przy każdym wywołaniu uczy klikania „tak" bez
  czytania. Okno jest jedną świadomą decyzją zamiast serii odruchowych.
- **Okno jako stan w pamięci demona.** Ginie z restartem, więc restart cicho
  zamykałby albo — przy zapisie do stanu odtwarzanego — cicho otwierał. Do tego
  wymagałby protokołu, którym operator otwiera okno, czyli powierzchni, przez
  którą można próbować je otworzyć.
- **Okno w katalogu policy.** Ten katalog jest `root:root 0644` i jest miejscem,
  gdzie mieszka **grant**. Wrzucenie tam „kiedy" zmieszałoby dwie rzeczy o różnym
  cyklu życia i kazałoby rutynowej akcji operatora edytować dokument zaufany.
- **Automatyczne przedłużanie okna przy użyciu.** Okno, które przedłuża się od
  ruchu agenta, jest sterowane przez agenta. To jest dokładnie ta własność, która
  czyni z niego trwały grant.
- **Odmowa zapisu w ogóle.** Bywa, że write access do MCP jest na tyle wartościowy
  i na tyle bezpieczny, że warto go wziąć. Rolą Torio jest uczynić ryzyko
  czytelnym, nie odebrać wybór — to zdanie z ADR-0022 zostaje w mocy.
