# Security Policy

## Zakres PoC

Torio PoC chroni przede wszystkim przed:

- błędnymi lub prompt-injected tool calls workera,
- kodem projektu wykonywanym w task containerze,
- przypadkowym dostępem workera do innych repozytoriów i credentials,
- niezatwierdzoną integracją lub push.

PoC nie gwarantuje ochrony przed:

- exploitem kernela VM lub container escape,
- złośliwym/skompro­mitowanym procesem Hermes runtime,
- administratorem Linux VM,
- malware wymagającym klasy enterprise sandbox,
- wieloużytkownikowym hostile tenancy.

Dla takich przypadków wymagany jest poziom Hardening: osobne OS identities, rootless/dedicated executor albo ephemeral VM/managed runner.

## Raportowanie

Nie umieszczaj realnych sekretów w issue, logach ani reprodukcji. Zastępuj je `[REDACTED]`. Raport powinien zawierać:

- naruszony invariant,
- minimalną reprodukcję,
- wersje komponentów,
- blast radius,
- sugerowane fail-closed zachowanie.

## Zakazane konfiguracje

- backend Hermesa dostępny bez auth na non-loopback,
- szeroki mount katalogu domowego macOS,
- Docker socket w workload containerze,
- host Git credentials w workerze,
- host-side egress tools przy `network: none`,
- candidate verification na VM hoście,
- automatyczny merge/push,
- policy odczytywana z task branch.
