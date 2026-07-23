# Handoff — Etap 0B (runtime) + 0C (evidence durability) + 0D/S2 (gateway lifecycle live)

- Data: 2026-07-24 (live run S2 + Etap 0E driver-correction rev-2: 2026-07-24)
- Sesja: live spike **S2** — natywny gateway jako systemd **user** service w VM `hermes-box`.
  Etap 0E rev-2 hardening drivera: read-only preflight PRZED jakąkolwiek mutacją; ownership-safe
  cleanup (nigdy nie rusza cudzego gatewaya); izolowany `env -i` + rozdzielny disposable
  `HERMES_HOME`; fail-closed empty-state gate przed install/start; probes 3-stanowe
  (absent/present/query-error); raw exit codes; kill -0 na każdym PID; boot-id/Lima/DB-identity jako
  twarde postconditions; runtime socket proof; redakcja stdout; **niezerowy exit przy FAIL/UNKNOWN**;
  tracked evidence ścieżek porażki (pre-seed conflict + injected failure).
  Bez kodu produkcyjnego, bez S1 i S3–S8, bez model/messaging credentials, bez workerów.
- Wykonawca: Claude Code (Opus 4.8) w imieniu wzslr821
- Adresat: LLM-głowa projektu (orchestrator). `AGENTS.md` pozostaje nadrzędny.

> **Status planu:** Etap 0 **NIE jest ukończony** (INCOMPLETE). Target runtime istnieje
> (S0-TARGET-VM: PASS) i S2 (gateway/systemd lifecycle) jest **PASS** live, ale S1 i S3–S8 nie zostały
> wykonane. Nie oznaczaj Etapu 0 jako completed. Jeden dowiedziony slice ≠ GO.

## Gate status (bieżący, po S2)

```text
S0-HOST:        PASS
S0-TARGET-VM:   PASS
S2:                    PASS   (native gateway systemd user-service lifecycle, live w VM)
Etap 0:                INCOMPLETE
Demo A:                NO-GO
Demo B native Docker:  NO-GO
```

**NO-GO reason:** runtime istnieje i S2 jest dowiedzione live, ale S1, S3, S4, S5 (live), S6, S7, S8
nie mają wymaganego live evidence. Bramki mogą zmienić się dopiero po zebraniu i review pozostałego
live evidence (fail closed, AGENTS §9).

## Co jest udowodnione

- **S0-HOST (PASS):** macierz wersji/architektur hosta macOS (Etap 0A, historyczny baseline).
- **S0-TARGET-VM (PASS):** docelowy Linux arm64 runtime **istnieje** i jego macierz S0 zebrano w-VM —
  Lima v2.2.0 → `hermes-box` (Ubuntu 24.04.4 arm64, vz); Docker Engine 29.6.2 client↔server; Hermes
  v0.19.0 upstream 91546b83; git 2.43.0; Python 3.12.3; cały stan Hermes/HB/Docker na natywnym ext4;
  **brak macOS host share** w VM. To dowodzi *istnienia* runtime, nie zachowania S1–S8.
- **S2 (PASS) — native gateway systemd user-service lifecycle (live w VM):** cały test biegnie w
  **izolowanym, jednorazowym** `HERMES_HOME=/home/hermes/.hermes-s2-spike` (realne `~/.hermes` nietknięte),
  pod `env -i` (allowlist). Unit `hermes-gateway.service` (user scope,
  `/home/hermes/.config/systemd/user/`), `Restart=always`, `WantedBy=default.target`,
  `HERMES_HOME=/home/hermes/.hermes-s2-spike`, brak `0.0.0.0` (potwierdzone też runtime: proces gatewaya
  nie posiada żadnego nasłuchującego socketu TCP). Przebieg (PIDy z tego przebiegu):
  install `--no-start-now --start-on-login` → enabled+inactive+MainPID=0; start → active/running
  (PID **2289**, `Result=success`, `kill -0` żywy); SIGKILL → nadzorowany restart (PID **2387**≠2289,
  `NRestarts≥1`); native `restart` → PID **2462**; stop → inactive/PID 0 + zwolniony dispatcher lock;
  **VM reboot** → auto-start przez linger (`Linger=yes` przetrwał; **boot_id zmieniony** — realny reboot),
  PID **2590→859**; uninstall → unit usunięty, `HERMES_HOME` + board DB zachowane. Każdy deklarowany PID
  checkpoint zweryfikowany `kill -0`. Embedded dispatcher trzyma
  `<HERMES_HOME>/kanban/.dispatcher.lock` (CONTENDED gdy active / FREE gdy stopped, probe pollowany do
  steady-state). Board DB sprawdzany po **path + (st_dev, st_ino) + schema + integrity + zero counts**
  (tasks=0, task_runs=0) w baseline, po restart, reboot i uninstall; fail-closed empty-state gate PRZED
  install (schema + zero tasks/runs, `cron`=0, brak workera). **`hermes gateway status` kończy 0 w każdym
  stanie** — **realny raw exit** obu wywołań (nie-zainstalowany i zatrzymany) = **0**, łapany natychmiast
  → exit 0 nie jest postcondition (D2). Bez platform/modelu/workera. Preflight jest **read-only** i
  **odmawia (exit≠0) PRZED jakąkolwiek mutacją** przy istniejącym unit/procesie — dowiedzione tracked
  evidence (`s2-preflight-abort.txt`: seed byte/state-identyczny po odmowie). Fail-closed klasyfikacja:
  driver **kończy niezerowo** przy każdym FAIL/UNKNOWN (dowiedzione `s2-injected-failure.txt`).
  Sterownik: [`spikes/s2_gateway_lifecycle.sh`](spikes/s2_gateway_lifecycle.sh); evidence:
  [`docs/spike-results/evidence/s2-gateway-lifecycle/`](docs/spike-results/evidence/s2-gateway-lifecycle/).
- **S5 (trójstopniowa klasyfikacja):**
  - **S5 legacy worktree characterization: PASS** — mechanika linked-worktree i rekonstrukcja
    dokładnego tree (host-side).
  - **S5 masking-only security hypothesis: FAIL** — samo zamaskowanie `.git` nie odbiera władzy
    (discovery escapuje do repo-przodka); zaadresowane przez ADR-0011.
  - **S5 materialized Git-free workspace live proof: UNKNOWN** — nowy mechanizm ADR-0011
    (materializowany katalog bez `.git`, brak osiągalnego repo-przodka, `GIT_CEILING_DIRECTORIES=`
    `/workspace`, negatywne `rev-parse`/`update-ref`, trusted exact-tree reconstruction) nie był
    jeszcze wykonany w-VM. Superseded wariant worktree-mount **nie** będzie uruchamiany.
- **S3 (PARTIAL):** kontrakt kolejki Kanban bez modelu w izolowanym `HERMES_HOME`:
  create→`ready`, resolve workspace przy claim z lockiem `<host>:<pid>`, heartbeat, reclaim, jedna
  `run`-row na próbę, idempotency-key dedup, workspace kinds `scratch`/`worktree`/`dir`.
  **Sequential ownership guard i double-claim refusal: PASS. Concurrent claim atomicity pod realnym
  race: UNKNOWN.**
- **S4/S6 (source-of-truth):** kontrakt izolacji Dockera i wektory env/credential odczytane ze źródła
  Hermesa (commit 91546b83); live half UNKNOWN.

## Tracked evidence (durable)

```text
docs/spike-results/00-runtime-versions.md   (S0-HOST + sekcja "Etap 0B", pinned versions, Lima config)
docs/spike-results/01..08 + 99-decision.md  (per-slice evidence + decyzja bramek)
docs/spike-results/evidence/etap-0b/s0-target-vm.txt     (zsanityzowany transcript, komendy + exit codes)
docs/spike-results/evidence/etap-0b/lima-hermes-box.yaml (użyty config VM, byte-identyczny z resolved)
docs/spike-results/evidence/etap-0b/SHA256SUMS           (manifest SHA-256 obu plików)
docs/spike-results/evidence/s2-gateway-lifecycle/s2-gateway-lifecycle.txt  (S2 clean run: komendy + raw exit codes)
docs/spike-results/evidence/s2-gateway-lifecycle/s2-preflight-abort.txt    (negatyw: pre-seed conflict → refusal, seed nietknięty)
docs/spike-results/evidence/s2-gateway-lifecycle/s2-injected-failure.txt   (negatyw: injected FAIL → niezerowy exit)
docs/spike-results/evidence/s2-gateway-lifecycle/hermes-gateway.service    (wygenerowany unit, verbatim)
docs/spike-results/evidence/s2-gateway-lifecycle/SHA256SUMS                (manifest SHA-256 czterech plików)
spikes/s5_git_boundary.sh                    (throwaway reprodukcja S5)
spikes/s2_gateway_lifecycle.sh               (throwaway reproducible driver S2)
```

Poprzednie surowe artefakty w `docs/spike-results/artifacts/` pozostają gitignored (konwencja repo dla
runtime output); durable evidence to powyższe tracked pliki.

## Commity na `main` (istniejące, tło Etapu 0)

```text
58e73cd6d0a136e919236779cf3a254f7127c0a5  spike: record partial host runtime evidence
a48306bd0e9c63eedb6a440746bdea99aa760ee7  docs: adopt host spike contract findings
d843f8a478312e0ac130bd0074b39f68a3dbef9b  spike: establish pinned Lima target runtime
```

Od tego czasu `origin/main` posunął się do **`bc34acb`** (merge PR #1 — durable evidence Etap 0B/0C;
commity 58e73cd/a48306b/d843f8a pozostają jego przodkami). Praca S2 jest na osobnym branchu
`spike/s2-gateway-systemd-lifecycle` (PR #2, niezmergowany).

## Zaakceptowane ADR / contracts (bez zmian w tej sesji)

- ADR-0001..0010 oraz **ADR-0011 (Accepted, supersedes ADR-0005)** — materialized Git-free workspaces:
  worker dostaje materializowany katalog bez `.git` montowany jako `/workspace`, **nie** worktree.
- Applied wcześniej na `main` (commit `a48306b`): `cli.md` mutation postcondition (`claim` exit 0 nie
  jest postcondition; structured output + re-query + fail closed) oraz pola ADR-0011 w
  `effective-policy.md` / `effective-policy.schema.json` (`workspace.kind=materialized-tree`,
  `repository_ancestor_reachable=false`, `git_metadata=denied`, `git_ceiling_directories=["/workspace"]`,
  `worker.fresh_per_task=true`, `worker.persist_across_processes=false`).
- Pozostałe contracty w `docs/contracts/` (executor, service-lifecycle, state-ledger, task-request,
  review-evidence, backup-recovery, project-config) — niezmienione.
- Ta sesja (0E, korekta S2) **nie modyfikuje** żadnego ADR-a ani contractu (AGENTS §9) — zmienia
  wyłącznie driver S2, evidence i statusy dowiedzione przez S2. Podział na *Applied decisions* i
  *Remaining work* jest w [`docs/spike-results/99-decision.md`](docs/spike-results/99-decision.md);
  pozostałe pozycje czekają na in-VM behavioural re-run i decyzję głowy projektu.

## Status slice'ów (S2 = PASS; pozostałe live nie wykonane — bramki NO-GO)

- **S1** — brak live Desktop/WebSocket; potrzebna strategia mock/throwaway provider (bez realnych creds).
- **S2** — **PASS (rozwiązane)**: in-VM gateway/systemd **user-scope** lifecycle wykonany live (patrz
  wyżej + `docs/spike-results/02-gateway-service.md`). Otwarte poza S2: scope `--system` nietestowany.
- **S3 (live)** — realna egzekucja workera + SIGKILL→auto-reclaim + concurrent-claim atomicity: UNKNOWN.
- **S4** — Docker istnieje w VM, ale isolation/freshness canaries nieuruchomione.
- **S5 (live)** — materialized Git-free workspace boundary (ADR-0011): materializowany katalog bez
  `.git` montowany jako `/workspace`, negatywne testy Git w kontenerze i trusted exact-tree
  reconstruction — nieuruchomione w-VM. (Legacy characterization PASS, masking-only FAIL — powyżej.)
- **S6 (live)** — diff effective env/mounts (empty forward list) + host-tool enumeration workera: UNKNOWN.
- **S7** — fresh verifier (druga świeża izolacja) nieuruchomiony.
- **S8** — kill/reboot reconciliation matrix nieuruchomiona.

## Odchylenia zarejestrowane w evidence (ratyfikowane przez orchestratora poprzez akceptację 0B)

Zebrane w `docs/spike-results/00-runtime-versions.md` (sekcja „Etap 0B → Deviations recorded"):

1. Hermes install method raportuje `unknown` (VM to `git archive` przypiętego commitu, bez `.git`);
   commit potwierdzony przez sankcjonowany marker `.hermes_build_sha` → `hermes --version` = `91546b83`.
2. Python w VM 3.12.3 vs host 3.11.15 — oba spełniają `requires-python ">=3.11,<3.14"`.
3. `stat -f -c '%T %m'` zwraca `ext2/ext3 ?` — GNU `stat` nazywa całą rodzinę ext; `findmnt` jest
   autorytatywne i pokazuje `ext4` wszędzie.
4. `/mnt/lima-cidata` (iso9660 na `/dev/vdb`) to read-only cloud-init seed Limy, nie macOS host share.

## Security invariants (AGENTS §5) — stan po Etapie 0B

- #1 repos/state na Linux fs VM — **MET (presence)**: cały stan na natywnym ext4, brak macOS share
  (dowód: `findmnt`/host-share count = 0 w evidence). Live behaviour dalej do sprawdzenia w S1–S8.
- #4 worker bez docker.sock — **wsparte ze źródła**; live UNKNOWN (S4 canaries nieuruchomione).
- #5 worker bez używalnego `.git`/push — kluczowe ustalenie S5; zaadresowane przez ADR-0011 (materialized
  dir, nie worktree); live weryfikacja `git -C /workspace rev-parse` fails — UNKNOWN.
- #7 policy obejmuje host tools/MCP/skills/env — **wsparte ze źródła** (S6); live UNKNOWN.
- #3 świeży kontener per task — źródło pokazuje domyślnie reuse ON → do wyłączenia; live UNKNOWN (S4).
- #2,#6,#8–#14 — nie ćwiczone; nie deklaruję jako sprawdzone.

## Exact next task

**Dokończyć korektę drivera/evidence S2 w tym samym PR (#2) i przejść orchestrator re-review.**
Nic więcej. **Nie** rozpoczynać S1, S3–S8, S2 `--system` ani Demo A/B, i **nie** wybierać kolejnego
slice'a — następny slice zostanie wskazany osobnym, jawnym handoffem orchestratora dopiero po
zaakceptowaniu tego PR.

## Files to read first

1. `AGENTS.md` (nadrzędny kontrakt)
2. `docs/spike-results/99-decision.md` (bramki, open items, następny task)
3. `docs/spike-results/00-runtime-versions.md` (Etap 0A baseline + Etap 0B target matrix)
4. `docs/spike-results/evidence/etap-0b/` (tracked transcript + Lima config + SHA256SUMS)
5. `docs/plans/01-spike.md` (S0–S8, gate'y)
6. `docs/adr/0011-materialized-git-free-workspaces.md`, `0003`, `0004`, `0007`
7. `docs/07-source-verification.md` (zakaz zgadywania, drift wersji 91546b83 vs d9165d7a)
