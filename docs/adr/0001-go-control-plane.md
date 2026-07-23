# ADR-0001: Go dla control plane i CLI

- Status: Accepted
- Date: 2026-07-23

## Context

`hb` ma działać na macOS arm64 i Linux arm64, zarządzać procesami, SQLite, Git, Lima, Dockerem i systemd oraz być łatwy do uruchomienia przez lokalnego developera i LLM. Nie powinien wymagać systemowego Pythona, modyfikować środowiska Hermesa ani importować jego prywatnych modułów.

## Decision

Control plane i CLI implementujemy w Go 1.26.x. Dokładny toolchain jest pinowany przez `go.mod` i `.tool-versions`. Moduł ma lokalną nazwę `hermes-box.local/hb` do czasu wyboru publicznego hostingu.

Preferencje:

- standard library przed zależnościami,
- `log/slog`, `context`, `os/exec.CommandContext`,
- SQLite driver bez CGO, jeśli spike nie wykaże istotnej przeszkody,
- jawne interface adapters dla Git, Hermes, Docker/Lima i filesystemu.

## Consequences

- Jedna binarka ułatwia dystrybucję i recovery.
- Nie używamy prywatnego Python API Hermesa.
- Integracja odbywa się przez zweryfikowane CLI/API/plugin contracts.
- Przed dodaniem SQLite/CLI library implementer dokumentuje wybór i pinning.

## Rejected

- Bash jako główna implementacja: zbyt słabe typowanie, quoting i testability.
- Python jako główny control plane: łatwiejsza integracja, ale bardziej złożone packaging/runtime isolation.
- Rust: mocny safety model, lecz wolniejszy PoC i większy koszt dla LLM workflow.
