# Etap 0 — Runtime contract spike

## Cel

Zweryfikować zachowanie aktualnie zainstalowanych komponentów na Apple Silicon/macOS + Lima Linux arm64. To jest eksperyment, nie implementacja produkcyjna.

## Reguły

- Throwaway code wyłącznie w `spikes/`.
- Każdy test zapisuje commands, versions, exit codes i redacted output.
- Nie kopiuj realnych credentials do repo.
- Nie zmieniaj produkcyjnych kontraktów bez opisania różnicy.
- Po spike'u zrób GO/NO-GO dla Demo A i Demo B osobno.

## S0 — Environment matrix

Zapisz `docs/spike-results/00-runtime-versions.md`.

Sprawdź realnie:

```bash
uname -a
uname -m
sw_vers                      # macOS
limactl --version
hermes --version
hermes serve --help || true
hermes dashboard --help || true
hermes gateway --help
hermes kanban --help
docker version
git --version
go version
```

Acceptance:

- wszystkie wersje i architektury zapisane,
- Hermes commit/package version identyfikowalny,
- wybrany compatibility surface opisany bez zgadywania.

## S1 — Desktop backend over SSH

Hipoteza: backend można trzymać na loopback w VM i używać przez SSH tunnel/transport.

Test:

1. Uruchom zweryfikowane `serve` albo compatibility command.
2. Potwierdź listening tylko na `127.0.0.1`.
3. Potwierdź `/api/status` w VM.
4. Utwórz tunnel z macOS.
5. Połącz Desktop, wykonaj harmless chat, potwierdź session persistence.
6. Restart backendu i VM, potwierdź reconnect.

Evidence: `01-desktop-backend.md`.

Gate:

- PASS tylko po live Desktop/WebSocket, nie samym HTTP status.

## S2 — Gateway service lifecycle

1. Feature-detect `hermes gateway install/start/status/logs`.
2. Zainstaluj testowy profil bez produkcyjnych adapter credentials.
3. Zapisz wygenerowaną nazwę unitu i service manager.
4. Restart procesu i VM.
5. Potwierdź dispatcher behavior osobno.

Evidence: `02-gateway-service.md`.

## S3 — Kanban worker contract

Na izolowanym testowym boardzie:

1. Utwórz task z worker profile.
2. Zapisz task/run IDs, env vars, cwd, workspace kind/path.
3. Potwierdź claim, heartbeat, completion i run history.
4. SIGKILL worker i potwierdź reclaim/retry.
5. Sprawdź ownership guards i idempotency.
6. Sprawdź, które tools worker naprawdę otrzymuje.

Evidence: `03-kanban-worker.md`.

Zakaz: nie zapisuj bezpośrednio do Kanban SQLite.

## S4 — Docker freshness and tool routing

Dla natywnego Docker backendu:

1. Ustaw cross-process persistence off.
2. Uruchom task A i zapisz canary w `/workspace`, `/tmp`, `$HOME`, background process.
3. Zakończ task A.
4. Uruchom task B.
5. Potwierdź brak canary/process/package state.
6. Sprawdź `docker inspect`: mounts, network, labels, user, caps, security opts.
7. Sprawdź terminal/file/execute routing.
8. Spróbuj znaleźć Docker socket/CLI/group wewnątrz workloadu.

Evidence: `04-docker-isolation.md`.

Gate:

- PASS tylko jeśli task B nie widzi stanu A albo implementacja dostarcza zweryfikowany unique isolation key/fresh destroy.

## S5 — Worktree without Git authority

1. Utwórz testowe repo i linked worktree na hoście VM.
2. Zamontuj tylko worktree jako `/workspace`.
3. Zapisz zawartość `.git` pointer i rzeczywiste zachowanie `git status`.
4. Zamaskuj/odetnij `.git` bez blokowania edycji plików.
5. Zmień tracked, untracked, deleted, executable bit i symlink.
6. Po stopie candidate control plane ma odtworzyć exact diff/tree.
7. Udowodnij, że workload nie może zmieniać innych refs/worktrees.

Evidence: `05-worktree-git-boundary.md`.

## S6 — Skills, env and credentials

1. Minimalny worker profile bez skills.
2. Skill deklarujący testową zmienną/canary credential file.
3. Porównaj effective env i mounts przy pustym explicit forward list.
4. Zidentyfikuj API/source do statycznej inspekcji skill requirements.
5. Potwierdź, że policy może fail closed przed startem.
6. Sprawdź host-side web/browser/MCP tool availability.

Używaj wyłącznie canary `HB_TEST_SECRET=[REDACTED]`, nigdy realnego tokenu.

Evidence: `06-skills-secrets-tools.md`.

## S7 — Fresh verifier

1. Zamroź harmless candidate.
2. Uruchom trusted argv command na exact snapshot w drugim świeżym containerze.
3. Potwierdź brak worker canary/home/background process.
4. Potwierdź network/credential policy.
5. Zapisz image digest, command IDs, exit/log hashes.
6. Zmień candidate i udowodnij, że poprzednie evidence nie pasuje.

Evidence: `07-verifier.md`.

## S8 — Restart/reconciliation

Dla kontrolowanych punktów kill/reboot:

- po admission,
- w trakcie worker execution,
- po stopie przed snapshotem,
- po candidate przed verification,
- po approval przed integration,
- po integration przed push.

Zapisz obserwacje Docker/Git/Kanban/filesystem i wymagane repair actions.

Evidence: `08-reconciliation.md`.

## Final output

`docs/spike-results/99-decision.md` zawiera:

```text
Demo A: GO / NO-GO
Demo B native Docker: GO / NO-GO
Confirmed contracts
Changed assumptions
Open blockers
Required ADR updates
Pinned versions/digests
Exact next task
```

Nie rozpoczynaj Demo A w tej samej sesji LLM. Oddziel spike review od implementacji.
