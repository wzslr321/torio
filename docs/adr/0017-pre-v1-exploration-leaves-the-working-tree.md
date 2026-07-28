<!--
AI-Provenance:
  model: Claude Opus 5
  harness: Claude Code
-->

# ADR-0017: Pre-V1 eksploracja opuszcza drzewo robocze

- Status: Accepted
- Date: 2026-07-28
- Supersedes: klauzulę archiwalną [ADR-0014](0014-rename-to-torio.md) w części
  nakazującej **zachowanie** materiału w drzewie roboczym, oraz retencyjną część
  [ADR-0016](0016-normative-documents-are-corrected-not-archived.md)
  („Materiał **archiwalny** pozostaje dosłowny"). Zasada, że materiału
  archiwalnego **się nie przepisuje**, obowiązuje bez zmian — ten ADR jej nie
  narusza, bo niczego nie przepisuje.

## Context

Przed Torio V0 repozytorium trzymało znacznie szerszą eksplorację: staged roadmap
(Spike → Demo A → Demo B → Hardening), trusted control plane z project registry
i admission control, per-task worker isolation, fresh sandboxed verification oraz
pipeline review/evidence. Nic z tego nie zostało dostarczone.
`docs/legacy-architecture.md` opisywał ten materiał jako superseded i zapowiadał,
że „may be reorganized or removed in a later, dedicated change — not here".
To jest ta zmiana.

Skala rozjazdu, zmierzona na `main @ f8856ee`:

- **680 śledzonych plików, ~3.8 MB.** `docs/` to 490 z nich i 2.3 MB.
- `docs/spike-results/evidence/` — 109 plików, 1781 KB. Same przebiegi
  `s2-gateway-lifecycle-rev7` i `rev9` to 1.5 MB transkryptów bramki, która
  zamknęła się jako PASS w lipcu 2026 i nie steruje już żadną decyzją.
- `spikes/s2_gateway_lifecycle.sh` i `s2_negative_harness.sh` — 155 KB throwaway
  drivera do tej samej bramki, plus `scripts/test_s2_contracts.py` (33 KB), który
  testuje wyłącznie te drivery.
- Czternaście numerowanych dokumentów projektowych opisuje platformę workerów;
  `docs/plans/` opisuje etapy, których nie realizujemy; `schemas/` i `examples/`
  nie są czytane przez żaden plik `.go`.

Materiał jest **całkowicie odcięty** od żywego produktu. `docs/content/` i `site/`
nie linkują do niego ani razu, a `scripts/build_docs.py` i
`scripts/check_site_links.py` go nie dotykają. Trzyma go tylko lista `REQUIRED`
w `scripts/validate_artifacts.py`, sekcja „Legacy architecture" w `README.md`,
sekcje 4–5 `AGENTS.md` i dwa pojedyncze linki.

Koszt tej masy nie jest hipotetyczny. `AGENTS.md` musi w bloku statusu tłumaczyć,
których własnych sekcji nie wolno implementować. ADR-0016 powstał dokładnie
dlatego, że implementer czytający kolejność autorytetu dosłownie mógł nadać
tożsamości agenta uprawnienia root-equivalent na podstawie superseded kontraktu.
Każdy nowy czytelnik — człowiek czy LLM — płaci tę cenę przy wejściu.

## Decision

Materiał archiwalny wychodzi z drzewa roboczego. Jego nośnikiem staje się
**adnotowany tag `archive/pre-v1`** wskazujący `main @ f8856ee` — ostatni commit,
w którym komplet jest obecny.

Tag jest kanonicznym punktem odcięcia. Odtworzenie dowolnego pliku to
`git show archive/pre-v1:<ścieżka>`; odtworzenie całości to
`git checkout archive/pre-v1`. Wierność evidence, której broniły ADR-0014
i ADR-0016, zostaje zachowana co do bajtu — zmienia się nośnik, nie treść.

### Wychodzi z drzewa roboczego

- `docs/spike-results/evidence/` w całości oraz pre-V0 write-upy
  (`00-`…`08-`, `99-decision.md`, `_template.md`, `README.md`, `artifacts/`);
- osiem superseded przebiegów `docs/spike-results/v1-*`;
- `spikes/s2_gateway_lifecycle.sh`, `spikes/s2_negative_harness.sh`,
  `spikes/s5_git_boundary.sh` oraz `scripts/test_s2_contracts.py`;
- `docs/01-`…`docs/14-*.md` poza `03-architecture.md`;
- `docs/plans/` w całości;
- `docs/contracts/` poza `cli.md` i `config.md`;
- ADR-y 0002, 0004–0009, 0011;
- `prompts/`, `schemas/`, `examples/`;
- `HANDOFF.md`, `docs/legacy-architecture.md`,
  `.hermes/plans/2026-07-23_172055-hermes-box.md`.

### Zostaje

- **Evidence, które nadal steruje decyzją:** `v1-brain-transfer-20260727T211411Z`
  (Gate 0 Taska 10, human PASS), `v1-brain-exchange-20260727T204508Z`,
  `v1-onboarding-20260727T115633Z` i `v1-brain-transfer-STATUS.md`. Są cytowane
  przez plan V1 i `spikes/v1-onboarding/README.md`.
- **ADR-y opisujące dostarczony kod:** 0001 (Go), 0003 (Lima trust boundary),
  0010 (prebuilt image — `lima.PromotedImageURL`), 0012 (Cobra),
  0013 (trusted config authority — `internal/config/trust_*.go`), 0014, 0015, 0016.
- **Kontrakty normatywne:** `docs/contracts/cli.md` i `config.md`. ADR-0016
  nakazuje je poprawiać, nie kasować, i to nadal obowiązuje.
- `docs/03-architecture.md`, przepisany na V1 — jedno miejsce opisujące granice
  zaufania i podział własności, zastępujące czternaście dokumentów.

### Odsyłacze do zarchiwizowanych ścieżek

Dwa rodzaje plików, które zostają, wskazują na materiał, który wychodzi.

**Zachowane ADR-y.** `docs/adr/0013` odsyła do trzech archiwizowanych plików.
Odsyłacze zamieniamy z linków Markdown na inline code spans: **tekst zostaje
dosłowny, znika wyłącznie cel linku**. To operacja mechaniczna na referencji,
nie zmiana decyzji, więc nie narusza zakazu z `AGENTS.md` §9. Alternatywa —
zostawić martwe linki — psuje `validate_links` albo wymaga wyłączenia kontroli,
która ma łapać prawdziwe regresje.

**Komentarze w kodzie.** Sześć plików źródłowych cytuje evidence uzasadniające
konkretną decyzję implementacyjną — promowany kształt argv operator shella,
zaobserwowany format `limactl list --json`, wykryty loopback `hermes serve`.
Cytowania przepisujemy na formę tagową, na przykład:

```text
archive/pre-v1:docs/spike-results/v1-operator-shell-20260727T132420Z/FINDINGS.md
```

Odczyt to `git show <powyższe>`. Provenance zostaje weryfikowalne, a komentarz
przestaje wskazywać ścieżkę, której nie ma. To ta sama operacja co wyżej:
zmienia się adres, nie treść.

## Consequences

- Drzewo robocze schodzi z ~680 plików / ~3.8 MB do ~270 plików / ~1.4 MB.
  `docs/` — z 490 plików do ~76.
- `scripts/validate_artifacts.py` traci `REQUIRED`, `EXAMPLES` i `validate_json`:
  po czystce nie mają czego sprawdzać. Zostają `validate_links`
  i `validate_secrets` (260 → ~110 LOC).
- `AGENTS.md` traci sekcje 4–5 (project registry, per-task isolation, fresh
  verifier, approval/integration/push, 14 worker invariants) i akapity bloku
  statusu, które ostrzegały przed ich implementacją. Ostrzeżenie przestaje być
  potrzebne, gdy nie ma czego ostrzegać. Sekcje 6–10 — TDD, evidence, redakcja,
  definition of done — zostają bez zmian.
- `docs/adr/README.md` przestaje odsyłać do `docs/04-threat-model.md`.
- Kto szuka historycznego uzasadnienia, potrzebuje jednej dodatkowej komendy
  (`git show archive/pre-v1:<ścieżka>`). To realny koszt i świadomie go ponosimy:
  materiał opisuje platformę, której nie budujemy, a jego obecność w drzewie
  kosztowała już jeden ADR poświęcony na wyjaśnianie, że nie należy go czytać
  jako instrukcji.

## Rejected

- **Zostawić wszystko i polegać na banerach.** Stan sprzed tej zmiany. Każdy
  archiwalny plik ma notę „superseded", a mimo to ADR-0016 musiał powstać, bo
  nota nie powstrzymała czytania kontraktu jako instrukcji. Baner nie skaluje się
  na 424 pliki.
- **Przenieść do gałęzi `archive/legacy` zamiast taga.** Gałąź zaprasza do
  commitów; archiwum, do którego ktoś dopisuje, przestaje być punktem odcięcia.
  Tag jest niezmienny i wyraża dokładnie tę intencję.
- **Usunąć bez taga, polegając na `git log`.** Działa, ale wymaga znajomości SHA
  albo przeszukiwania historii. Adnotowany tag kosztuje jedną komendę i nazywa
  punkt odcięcia wprost.
- **Zachować wszystkie ADR-y 0001–0016.** Rozważane poważnie: ADR-y są zapisem
  decyzji, a nie evidence. Odrzucone dla ośmiu z nich, bo opisują decyzje
  o systemie, który nie istnieje (natywny Docker per task, control-plane jako
  Git authority, content-addressed approval, fresh verifier). Zachowane są
  wszystkie ADR-y, których konsekwencje da się wskazać w dostarczonym kodzie.
- **Skasować także trzy cytowane przebiegi V1 i odsyłać do nich po SHA.** Bramka
  Taska 10 nadal jest jedynym dowodem na promowany transport
  (`limactl copy`), a Task 23 jej jeszcze nie zastąpił. Evidence, które steruje
  otwartą decyzją, zostaje w drzewie.
