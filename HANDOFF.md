# Handoff — Etap 0B (target runtime provisioned) + Etap 0C (evidence durability)

- Data: 2026-07-23
- Sesja: utrwalenie i uspójnienie evidence Etapu 0B (Etap 0C). Bez kodu produkcyjnego, bez S1–S8.
- Wykonawca: Claude Code (Opus 4.8) w imieniu wzslr821
- Adresat: LLM-głowa projektu (orchestrator). `AGENTS.md` pozostaje nadrzędny.

> **Status planu:** Etap 0 **NIE jest ukończony** (INCOMPLETE). Target runtime istnieje
> (S0-TARGET-VM: PASS), ale live S1–S8 nie zostały wykonane. Nie oznaczaj Etapu 0 jako completed.

## Gate status (bieżący, po provisioningu)

```text
S0-HOST:        PASS
S0-TARGET-VM:   PASS
Etap 0:         INCOMPLETE
Demo A:         NO-GO
Demo B:         NO-GO
```

**NO-GO reason:** target runtime istnieje, ale S1–S8 nie mają wymaganego live evidence. Provisioning
≠ GO. Bramki mogą zmienić się dopiero po zebraniu i review live evidence (fail closed, AGENTS §9).

## Co jest udowodnione

- **S0-HOST (PASS):** macierz wersji/architektur hosta macOS (Etap 0A, historyczny baseline).
- **S0-TARGET-VM (PASS):** docelowy Linux arm64 runtime **istnieje** i jego macierz S0 zebrano w-VM —
  Lima v2.2.0 → `hermes-box` (Ubuntu 24.04.4 arm64, vz); Docker Engine 29.6.2 client↔server; Hermes
  v0.19.0 upstream 91546b83; git 2.43.0; Python 3.12.3; cały stan Hermes/HB/Docker na natywnym ext4;
  **brak macOS host share** w VM. To dowodzi *istnienia* runtime, nie zachowania S1–S8.
- **S5 (PASS host-side):** mechanika linked-worktree i rekonstrukcja dokładnego tree; hipoteza
  „masking-only” obalona (FAIL) — teraz zaadresowana przez ADR-0011.
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
spikes/s5_git_boundary.sh                    (throwaway reprodukcja S5)
```

Poprzednie surowe artefakty w `docs/spike-results/artifacts/` pozostają gitignored (konwencja repo dla
runtime output); durable evidence to powyższe tracked pliki.

## Commity na `main` (istniejące, tło Etapu 0)

```text
58e73cd6d0a136e919236779cf3a254f7127c0a5  spike: record partial host runtime evidence
a48306bd0e9c63eedb6a440746bdea99aa760ee7  docs: adopt host spike contract findings
d843f8a478312e0ac130bd0074b39f68a3dbef9b  spike: establish pinned Lima target runtime
```

`origin/main` = `d843f8a478312e0ac130bd0074b39f68a3dbef9b` (nietknięty; ta praca jest na osobnym branchu).

## Zaakceptowane ADR / contracts (bez zmian w tej sesji)

- ADR-0001..0010 oraz **ADR-0011 (Accepted, supersedes ADR-0005)** — materialized Git-free workspaces:
  worker dostaje materializowany katalog bez `.git` montowany jako `/workspace`, **nie** worktree.
- Contracts w `docs/contracts/` (cli, effective-policy, executor, service-lifecycle, state-ledger,
  task-request, review-evidence, backup-recovery, project-config) — niezmienione.
- Ta sesja **nie modyfikuje** żadnego ADR-a ani contractu (AGENTS §9). Pozostałe rekomendacje
  contract-update wypisane w `99-decision.md` czekają na decyzję głowy projektu.

## Otwarte S1–S8 (live nie wykonane — bramki NO-GO)

- **S1** — brak live Desktop/WebSocket; potrzebna strategia mock/throwaway provider (bez realnych creds).
- **S2** — in-VM gateway/systemd lifecycle: test jeszcze nie zaplanowany/wykonany.
- **S3 (live)** — realna egzekucja workera + SIGKILL→auto-reclaim + concurrent-claim atomicity: UNKNOWN.
- **S4** — Docker istnieje w VM, ale isolation/freshness canaries nieuruchomione.
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

## Następny slice (wybiera orchestrator)

**Nie zaczynać Demo A. Nie startować S1 samodzielnie.** Po zaakceptowaniu tego evidence przez
orchestratora następny slice to:

> **S2 — in-VM gateway/systemd lifecycle characterization.** Bez model credentials.
> Nie rozpoczynać bez nowego handoffu orchestratora.

Kolejne slice'y (S1, S3 execution, S4, S6, S7, S8) i finalna re-ewaluacja bramek Demo A/B — każdy na
osobny handoff, z mock/throwaway modelem tam gdzie potrzebna egzekucja, bez realnych credentials.

## Files to read first

1. `AGENTS.md` (nadrzędny kontrakt)
2. `docs/spike-results/99-decision.md` (bramki, open items, następny task)
3. `docs/spike-results/00-runtime-versions.md` (Etap 0A baseline + Etap 0B target matrix)
4. `docs/spike-results/evidence/etap-0b/` (tracked transcript + Lima config + SHA256SUMS)
5. `docs/plans/01-spike.md` (S0–S8, gate'y)
6. `docs/adr/0011-materialized-git-free-workspaces.md`, `0003`, `0004`, `0007`
7. `docs/07-source-verification.md` (zakaz zgadywania, drift wersji 91546b83 vs d9165d7a)
