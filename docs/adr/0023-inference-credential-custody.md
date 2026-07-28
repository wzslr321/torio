<!--
AI-Provenance:
  model: Claude Opus 5 (1M context)
  harness: Claude Code
-->

# ADR-0023: Credential inferencji przestaje leżeć pod tożsamością agenta

- Status: Proposed
- Date: 2026-07-29
- Dotyczy: `internal/lima`, `internal/cli`, nowy komponent gościa `torio-infer`
- Powiązane: [ADR-0022](0022-mcp-credential-broker.md) (ten sam ruch dla tokenów
  MCP; tu nie da się go powtórzyć wprost), [ADR-0015](0015-torio-v1-onboarding-projects-and-operator-push.md)
  (granica „Torio nie dotyka sekretów"), [ADR-0003](0003-lima-trust-boundary.md)

## Context

ADR-0022 wyprowadził tokeny MCP spod tożsamości agenta. Został credential
większej wartości: `/home/hermes/.hermes/auth.json`, `0600 hermes:hermes`, z
żywą parą OAuth OpenAI Codex. Agent ma powłokę jako `hermes`, a denylista
odczytu Hermesa sama o sobie mówi, że nie jest granicą. To jest ten sam problem,
tylko dotyczy klucza, którego wyciek jest najdroższy.

Powtórzenie ADR-0022 jeden do jednego okazało się niemożliwe i powód jest
strukturalny, nie implementacyjny.

**Refresh token Codeksa rotuje i jest jednorazowy.** `refresh_codex_oauth_pure`
podmienia zapisany token na `next_refresh` z odpowiedzi, a
`refresh_token_reused` jest pierwszoklasowym błędem z komunikatem *„Codex
refresh token was already consumed by another client"* i `relogin_required`.
Refresh jest wywoływany także **reaktywnie, w trakcie tury, na HTTP 401**, i
bezwarunkowo zapisuje wynik do `auth.json`.

Z tego wynikają dwie rzeczy, które przesądzają całą decyzję:

1. **Custody „tylko do odczytu" nie istnieje dla tego credentialu.** Cokolwiek
   go trzyma, musi umieć zapisać zrotowaną wartość. To jest różnica wobec
   tokenów MCP, gdzie broker mógł być prostym strażnikiem.
2. **Może być dokładnie jeden posiadacz.** Dwóch to gwarantowana awaria: drugi
   dostanie `refresh_token_reused` i wymuszenie ponownego logowania. Migracja
   musi być **przeniesieniem**, nigdy kopią.

Druga przeszkoda dotyczy transportu. `model.base_url` jest URL-em http(s)
podawanym do SDK OpenAI; Hermes nie ma żadnej powierzchni konfiguracyjnej na
transport po unix sockecie. **Kształt z ADR-0022 — tożsamość rozmówcy ustalana
przez kernel — nie przenosi się na ścieżkę inferencji.**

Jedno ustalenie działa na naszą korzyść. Dla `provider: custom` z base URL-em na
loopbacku Hermes rozwiązuje klucz do literału `"no-key-required"`
(`runtime_provider.py`, trzy miejsca), a hosty loopbackowe są jawnie wyłączone z
wyprowadzania `<VENDOR>_API_KEY`. **Hermes potrafi działać nie trzymając żadnego
credentialu inferencji** — bez zmian w upstreamie, samą konfiguracją.

Sam flow Codeksa nie wymaga niczego, co przywiązywałoby go do `hermes`: to
klient publiczny bez client secretu, logowanie jest device-code z przeglądarką
gdziekolwiek, a refresh to gołe `POST /oauth/token`. Może więc w całości
wykonywać się pod innym uid.

## Decision

**Credential inferencji przenosi się pod osobną tożsamość gościa `torio-infer`,
a Hermes przestaje trzymać jakikolwiek credential inferencji.**

1. **Tożsamość i custody.** Nieuprzywilejowany uid `torio-infer`, home `0700`,
   bez sudo, poza `torio-projects` i poza `torio-mcp-clients`. Para OAuth
   Codeksa żyje wyłącznie w jego home. On i tylko on wykonuje logowanie oraz
   rotację refresh tokenu.

2. **Hermes nie trzyma nic.** Konfiguracja gościa przechodzi na nazwanego
   custom providera z base URL-em na loopbacku brokera; klucz rozwiązuje się do
   `"no-key-required"`. Wpis `providers.openai-codex` oraz wiersze
   `credential_pool["openai-codex"]` o źródle `device_code` / `manual:device_code`
   muszą **zniknąć**, nie zostać obok.

3. **Broker tłumaczy protokół.** Upstream to `codex_responses` wobec
   `https://chatgpt.com/backend-api/codex`, a nie zwykłe `chat/completions`.
   Broker mówi po stronie Hermesa kształtem, który Hermes potrafi wysłać, i
   przekłada go na kształt Codeksa.

4. **Torio nadal nie dotyka sekretów.** Logowanie jest interaktywną akcją
   operatora wykonywaną jako `torio-infer`; broker sam mennicuje i zapisuje
   token do swojego home. Granica z ADR-0015 obowiązuje bez zmian.

5. **Weryfikacja fail-closed** — dowodzona, nie zakładana: istnienie uid i tryb
   home; **brak** wpisu `providers.openai-codex` i **brak** wierszy puli o
   źródle device-code w `auth.json`; brak członkostwa `hermes` w grupie
   `torio-infer`. Kontrola raportuje wyłącznie obecność albo nieobecność klucza
   providera — **nigdy zawartości pliku**, tak jak reszta powierzchni Torio.

## Czego ta decyzja NIE kupuje

**Zamienia kradzież credentialu na jego użycie — i na tym poprzestaje.**

Transportem jest loopback TCP, bo Hermes nie umie inaczej. Tożsamości rozmówcy
nie ustala więc kernel, tylko nikt. Dowolny proces na gościu, który potrafi
otworzyć port brokera, wyda subskrypcję. Agent nie ukradnie pary tokenów, ale
może przez brokera pytać model o cokolwiek.

To jest **realne obniżenie** względem kształtu z ADR-0022 i nie wolno tego
opisywać inaczej. Wartość, która zostaje, jest jednak konkretna: skradziony
credential działa wszędzie i długo, a użycie brokera kończy się z brokerem, jest
policzalne i widoczne w jednym miejscu.

Domknięcie tej dziury istnieje i leży poza tym ADR-em: reguła netfiltera
kluczowana po uid, dopuszczająca połączenia na port brokera wyłącznie z uid
agenta. Połączenia na loopback przechodzą przez `OUTPUT`, więc ten sam mechanizm,
który obsłuży kontrolę egressu, obsłuży i to. **Ta decyzja jest z nią sprzężona i
bez niej jest niepełna.**

## Consequences

- Torio dostaje drugiego brokera. Dzieli z ADR-0022 wzorzec separacji
  tożsamości, ale **nie** transport — i mieszanie tych dwóch w opisie byłoby
  wprowadzaniem w błąd.
- **Funkcje specyficzne dla Codeksa przestaną działać**: sondy limitów,
  `/usage`, `codex_app_server`. Hermes nie będzie już wiedział, że rozmawia z
  Codeksem. To jest cena, nie usterka.
- Migracja jest jednokierunkowa i musi być przeniesieniem. Po niej trzeba
  udowodnić brak starego wpisu, a nie założyć go.
- Dopóki to nie wyląduje, gość trzyma wykradalny credential inferencji.
  Stan na 2026-07-29: żywy token OAuth Codex, odświeżony 2026-07-28 17:32.
- Rotacja przenosi się do brokera, więc to on staje się komponentem, którego
  awaria kładzie inferencję. Jego restart i backup przestają być błahe.

## Rejected

- **`hermes proxy` jako broker.** Rejestr adapterów to `{"nous", "xai"}` — nie
  ma adaptera `openai-codex`, więc aktywnego providera nie da się przez niego
  puścić. Do tego słucha na loopback TCP i **odrzuca nagłówek `Authorization`
  klienta**, czyli nie uwierzytelnia nikogo. Słabsze niż „loopback TCP z bearer
  tokenem", które ADR-0022 już odrzucił.
- **`hermes secrets` (Bitwarden / 1Password / `command`).** Pobiera sekret przy
  starcie procesu **do `os.environ` agenta** i cache'uje wartości pod
  `<hermes_home>/cache/` jako `0600 hermes`, a token bootstrapowy trzyma w
  `~/.hermes/.env` należącym do `hermes`. Nie umie też wyrazić pary OAuth ani
  zapisać zrotowanego refresh tokenu. Przenosi **miejsce przechowywania**, nie
  **custody**.
- **`auth.json` należący do roota i nieczytelny dla `hermes`.** Katalog
  `$HERMES_HOME` należy do `hermes`, a zapis idzie przez tmp + `os.replace` w
  tym katalogu — więc pierwszy zapis puli po cichu odtworzy plik jako
  hermes-owned. Gorsze: odczyt bez uprawnień jest łapany przez `except
  Exception` i raportowany jako *„failed to parse … starting with empty store"*,
  nieodróżnialnie od uszkodzenia. Granica, która przy naruszeniu kłamie o
  przyczynie, jest gorsza niż jej brak.
- **Zachowanie `provider: openai-codex` z `HERMES_CODEX_BASE_URL` i atrapą pary
  tokenów.** Mechanicznie działa i oszczędza tłumaczenia protokołu, bo
  `_read_codex_tokens` wymaga tylko dwóch niepustych stringów, a nie-JWT nie
  wyzwala proaktywnego refreshu. Odrzucone, bo `auth.json` zawierałby wtedy
  credential-atrapę, a `hermes status` mówiłby „logged in" o tożsamości, której
  nie ma. Cała ta decyzja opiera się na tym, że stan jest czytelny wprost;
  wstawienie w to miejsce dekoracji, która wprowadza w błąd narzędzie
  diagnostyczne, kosztuje więcej niż zaoszczędzony adapter.
- **Dwóch posiadaczy refresh tokenu.** Wykluczone przez rotację: drugi dostanie
  `refresh_token_reused`. Nie jest to kwestia higieny, tylko gwarantowana awaria.
- **Przeniesienie transportu z ADR-0022 (unix socket, `SO_PEERCRED`).** Hermes
  nie ma dla inferencji żadnej powierzchni na unix socket; `base_url` idzie do
  SDK OpenAI jako http(s).
