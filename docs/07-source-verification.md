# Source verification

## Runtime baseline (Etap 0)

Runtime baseline dla dalszego Etapu 0 jest przypięty do zainstalowanego, faktycznie działającego
buildu Hermesa (pełny commit):

```text
repository: https://github.com/NousResearch/hermes-agent
commit:     91546b8337068891cc0a6b834d89d0d9270fb3ec   (v0.19.0, upstream)
```

Wcześniejszy `d9165d7a678d4105f42921a7fc1886df3804531b` pozostaje **historycznym fact-checkiem**
(analiza źródła poniżej), ale **nie jest runtime baseline**. Nie wolno mieszać evidence między tymi
rewizjami bez jawnego compatibility check (zapisany diff zachowań/kontraktu). Odświeżenie analizy
źródła do `91546b83` następuje po in-VM re-runie potwierdzającym zachowanie.

## Zweryfikowany punkt odniesienia (historyczny fact-check — commit d9165d7a)

W dniu 2026-07-23 przeanalizowano oficjalne repozytorium Hermes Agent:

```text
repository: https://github.com/NousResearch/hermes-agent
commit: d9165d7a678d4105f42921a7fc1886df3804531b
```

Potwierdzone przy tym commitcie:

- `hermes serve` uruchamia headless backend Desktop/remote clients.
- `hermes dashboard` i `serve` współdzielą backend, ale dashboard dodaje SPA.
- Messaging gateway jest osobnym procesem.
- Kanban dispatcher działa domyślnie w gatewayu.
- Kanban ma SQLite, claims, retries, heartbeats, run history i workspaces.
- Profile nie są sandboxem.
- Docker backend może współdzielić persistent container między procesami tego samego profilu.
- Skills mogą automatycznie forwardować env i credential files do Docker backendu.
- `hermes -w` oraz Kanban worktrees to różne mechanizmy/lifecycle.

## Reguła implementacyjna

Przed rozpoczęciem etapu 0 implementer MUSI zapisać nowy wynik w `docs/spike-results/00-runtime-versions.md`:

```bash
hermes --version
hermes serve --help
hermes dashboard --help
hermes gateway --help
hermes kanban --help
docker version
git --version
limactl --version
go version
```

Jeżeli zachowanie aktualnej wersji różni się od dokumentów, implementer:

1. nie dodaje cichego workaroundu,
2. zapisuje reprodukcję,
3. tworzy/aktualizuje ADR,
4. aktualizuje contract i test gate,
5. dopiero potem implementuje.

## Oficjalne źródła

- Hermes docs: <https://hermes-agent.nousresearch.com/docs/>
- Hermes repository: <https://github.com/NousResearch/hermes-agent>
- Kanban: <https://hermes-agent.nousresearch.com/docs/user-guide/features/kanban>
- Git worktrees: <https://hermes-agent.nousresearch.com/docs/user-guide/git-worktrees>
- Docker backend: <https://hermes-agent.nousresearch.com/docs/user-guide/docker>
- Security: <https://hermes-agent.nousresearch.com/docs/user-guide/security>
- Lima: <https://lima-vm.io/docs/>
- Docker Engine Ubuntu: <https://docs.docker.com/engine/install/ubuntu/>
- Dev Container spec: <https://containers.dev/implementors/json_reference/>

## Zakaz zgadywania

Przykłady, które wymagają runtime evidence:

- nazwa service unitu wygenerowanego przez gateway,
- port i auth mode backendu,
- exact worker command i environment,
- sposób przypisania Docker containera do taska,
- zachowanie worktree `.git` w mountcie,
- rzeczywisty toolset Kanban workera,
- lifecycle po SIGKILL/reboocie.
