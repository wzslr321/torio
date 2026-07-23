# Handoff — Etap 0 (runtime contract spike)

- Data: 2026-07-23
- Sesja: wykonanie wyłącznie Etapu 0 wg `docs/plans/01-spike.md` (bez kodu produkcyjnego)
- Wykonawca: Claude Code (Opus 4.8) w imieniu wzslr821
- Adresat: LLM-głowa projektu (orchestrator). `AGENTS.md` pozostaje nadrzędny.

> Uwaga o statusie planu: Etap 0 **NIE jest ukończony** — jest częściowo wykonany i częściowo
> BLOCKED przez środowisko. Nie oznaczaj Etapu 0 jako completed. Bramka do Etapu 1 (Demo A) wymaga
> `Demo A: GO` w `docs/spike-results/99-decision.md`, którego **nie ma** (jest NO-GO).

## Goal

Zweryfikować realne zachowanie Hermes/Lima/Docker/Git na Apple Silicon **przed** kodem produkcyjnym,
zapisać evidence w `docs/spike-results/`, i wydać osobne GO/NO-GO dla Demo A i Demo B. Bez
implementacji Demo A/B. Bez zgadywania zachowania Hermesa.

## Completed behavior

- **S0-HOST (PASS):** spisana pełna macierz wersji/architektur **hosta macOS**; zidentyfikowany
  Hermes commit; opisany wybrany compatibility surface bez zgadywania.
- **S0-TARGET-VM (UNKNOWN/BLOCKED):** docelowy Linux arm64 runtime nie istniał w tej sesji (brak
  Lima) — macierz S0 dla VM nie została zebrana. To osobny, nierozstrzygnięty poziom dowodu.
- **S3 (PARTIAL):** potwierdzony **runtime** kontrakt kolejki Kanban bez modelu, w izolowanym
  `HERMES_HOME` (bez zapisu do SQLite): create→`ready`, atomic claim z lockiem `<host>:<pid>`,
  heartbeat, reclaim, jedna `run`-row na próbę, idempotency-key dedup, workspace kinds
  `scratch`/`worktree`/`dir`.
- **S5 (PASS dla mechaniki host-side):** scharakteryzowana granica Git worktree i rekonstrukcja
  dokładnego tree przez zaufaną stronę.
- **S4/S6 (source-of-truth):** kontrakt izolacji Dockera i wektory env/credential odczytane z kodu
  zainstalowanego Hermesa (commit 91546b83), bo runtime jest BLOCKED.
- **S1, S2, S7, S8:** oznaczone **BLOCKED** (nie PASS) — środowisko nie pozwala na eksperyment.

Dwa ustalenia, które zmieniają sposób integracji `hb`:
1. `hermes kanban claim` przy konflikcie **wypisuje odmowę, ale kończy się exit `0`** → adapter musi
   parsować output / re-query status, nie ufać exit code.
2. Natywny Kanban worktree powstaje **wewnątrz repo** (`<repo>/.worktrees/<id>`), **tworzy branch** i
   daje ref-authoritative `.git`; a **samo zamaskowanie `.git` NIE odbiera władzy** (git discovery
   wchodzi w górę do repo-przodka — odtworzone). CP musi przygotować własny workspace i wymusić
   brak osiągalnego repo (mount-root / `GIT_CEILING_DIRECTORIES`).

## Real verification commands + exit codes

Tylko realnie uruchomione (bez fabrykacji). Pełne outputy: `docs/spike-results/*`.

```text
uname -m                                   -> arm64                              (exit 0)
sw_vers                                     -> macOS 26.5.2 (25F84)               (exit 0)
limactl --version                           -> command not found                  (exit 127)
hermes --version                            -> v0.19.0 upstream 91546b83          (exit 0)
docker version                              -> Cannot connect to the daemon       (exit 1)
git --version / go version                  -> 2.53.0 / go1.26.5                   (exit 0/0)

# S5 (spikes/s5_git_boundary.sh + ceiling probe)
git -C repo worktree add -b task-branch ../wt        (exit 0)
cat wt/.git   -> gitdir: $HOME/.../repo/.git/worktrees/wt   (absolutna ścieżka hosta) (exit 0)
mv wt/.git wt/.git.severed; git -C wt status         -> "On branch main" (ESCAPE)   (exit 0)
   git -C wt update-ref refs/heads/attacker HEAD     -> utworzył ref w hermes-box    (exit 0)  [posprzątane]
GIT_CEILING_DIRECTORIES=$OUT git -C wt status        -> "not a git repository"       (exit 128)
git -C wt add -A && git -C wt write-tree             -> tree 2f3483bf… ; mody 100755/120000 (exit 0)

# S3 (HERMES_HOME izolowany, bez modelu)
hermes kanban init / boards create hbspike / create --json / claim / heartbeat / runs --json  (exit 0 każde)
hermes kanban ... claim <już-running>  -> "cannot claim … lock=mac.home:18155"        (exit 0)  <- caveat
hermes kanban ... reclaim / re-claim   -> run 1 reclaimed + run 2 running              (exit 0)
create --idempotency-key spike-key-1 (x2) -> ten sam id t_94ffdbc9

# walidacja artefaktów
python3 scripts/validate_artifacts.py       -> PASS x4, internally consistent          (exit 0)
```

## Changed files

- Zaktualizowane evidence (były `[NOT-RUN]`): `docs/spike-results/00…08` + `99-decision.md` (10 plików).
- Nowy throwaway (reprodukcja S5, gitignore-safe lokalizacja `spikes/`): `spikes/s5_git_boundary.sh`.
- Ten plik: `HANDOFF.md`.
- **Brak** zmian w `cmd/`, `internal/`, `schemas/`, ADR-ach. Fixtures runtime (`spikes/output/`)
  utworzone i **usunięte** po zebraniu evidence.

## Decisions/contract changes

- **Żaden ADR ani contract nie został zmieniony** (AGENTS §9 — nie modyfikuję ADR-ów po cichu).
- W `docs/spike-results/99-decision.md` są **rekomendacje** wymagające nowego/superseding ADR:
  ADR-0005 (asercja „brak osiągalnego repo po masking” + ceiling), ADR-0004/`contracts/executor.md`
  (przypięcie knobów Dockera), ADR-0007/`contracts/effective-policy.md` (pola polityki),
  `contracts/cli.md` (claim = exit 0 przy konflikcie), odświeżenie `docs/07-source-verification.md`
  do commitu 91546b83 po re-runie w VM. **Decyzję o ADR podejmuje głowa projektu, nie ta sesja.**

## Security invariants checked (AGENTS §5)

- #1 repos/state na Linux fs VM — **UNMET/BLOCKED**: brak Lima; Hermes działa na hoście macOS (topologia odrzucana przez ADR-0003).
- #4 worker bez docker.sock — **wsparte ze źródła** (brak mountu socketa w `docker.py`); live BLOCKED.
- #5 worker bez używalnego `.git`/push creds — **kluczowe ustalenie S5**: masking `.git` sam w sobie NIE wystarcza; natywny worktree daje władzę Git → CP musi ją odebrać i to zweryfikować.
- #7 policy obejmuje host tools/MCP/skills/env — **wsparte ze źródła** (S6: `required_credential_files`, `terminal.credential_files`, `docker_forward_env`).
- #3 świeży kontener per task — **BLOCKED** (brak daemona); źródło pokazuje **domyślnie reuse ON** → ryzyko do wyłączenia (S4).
- #2,#6,#8–#14 — nie ćwiczone tej sesji (wymagają workera/Demo B lub VM). Nie deklaruję ich jako sprawdzone.

## Known failures/blockers

- **B1 Lima nieobecna** → blokuje S1, S2, reboot w S8, samą granicę zaufania.
- **B2 Docker daemon down** (i to Docker Desktop, nie Docker-in-Lima) → blokuje S4, S7, sandbox-część S3/S6.
- **B3 brak live model providera + brak headless Desktop drivera**; spike zabrania realnych credentials → blokuje live egzekucję S3 i live chat S1.
- Wersja Hermesa (91546b83) różni się od referencyjnej (d9165d7a w `docs/07-source-verification.md`).
- **S5 characterization: PASS** (mechanika linked-worktree i rekonstrukcja tree odtworzone na hoście),
  ale **masking-only security hypothesis: FAIL** — hipoteza „zamaskowanie `.git` odbiera władzę Git”
  została obalona (git discovery escapuje do repo-przodka, utworzono ref w realnym repo). Nie jest więc
  prawdą, że „nic nie obaliło hipotezy”: S5 obalił `masking-only` jako security control. Pozostałe
  kroki są niedokończone z powodu braku środowiska (BLOCKED/UNKNOWN), nie z powodu FAIL.

## Uncommitted state

Wszystko niezacommitowane (nie commituję bez prośby). `git status --short`:
```text
 M docs/spike-results/00…08 + 99-decision.md   (10 plików)
?? spikes/s5_git_boundary.sh
?? HANDOFF.md
```
Repo `refs/heads/` = tylko `main` (przypadkowy `attacker` z S5 usunięty i zweryfikowany). Realne
`~/.hermes` nietknięte (wszystkie operacje Kanban przez izolowany `HERMES_HOME`).

## Exact next task (one slice)

**Nie zaczynać Demo A.** Osobna sesja typu *spike-completion / provisioning* — jeden slice:

> Zainstaluj i przypnij wersję Lima; utwórz VM **Linux arm64** wg `docs/adr/0003-lima-trust-boundary.md`;
> zainstaluj Docker Engine **w VM** i uruchom Hermesa **w VM**; ponownie uruchom macierz **S0 wewnątrz
> VM** i **udowodnij, że repos/state leżą na Linux fs VM, nie na mountcie macOS** (security invariant #1).

Acceptance tego slice'u: `limactl` obecny+wersja przypięta; VM arm64 działa; `docker version` w VM
zwraca serwer; `hermes --version` w VM; repos/state poza VirtFS/9p. Dopiero potem kolejne slice'y:
S1 (live Desktop/WebSocket) → S2 → S3 (realna egzekucja workera + SIGKILL reclaim; mock/throwaway
model, bez realnych credentials) → S4 → S5 (mount worktree jako `/workspace`) → S6 → S7 → S8, a na
końcu re-ewaluacja bramek w `99-decision.md`.

## Files to read first

1. `AGENTS.md` (nadrzędny kontrakt)
2. `docs/spike-results/99-decision.md` (bramki, blockers, następny task)
3. `docs/plans/01-spike.md` (S0–S8, gate'y)
4. `docs/07-source-verification.md` (zakaz zgadywania, oficjalne źródła, drift wersji)
5. `docs/adr/0003-lima-trust-boundary.md`, `0004-native-docker-poc.md`, `0005-control-plane-git.md`, `0007-policy-includes-tools-skills.md`
6. Evidence szczegółowe: `docs/spike-results/03-kanban-worker.md`, `04-docker-isolation.md`, `05-worktree-git-boundary.md`
7. `prompts/00-implementer-system.md` (system context dla implementera)
