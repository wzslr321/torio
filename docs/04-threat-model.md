# Threat model

## Poziomy ochrony

| Poziom | Chroni przed | Mechanizm |
|---|---|---|
| T0 | Agent przypadkowo szkodzi macOS | Lima VM, narrow transfer mount |
| T1 — cel Demo B | Prompt-injected worker i candidate code | fresh container, tool/skill policy, no credentials/Git/Docker, verifier |
| T2 — Hardening | Skompromitowany Hermes runtime lub daemon | osobne OS identities, CP-owned/rootless executor |
| T3 | Malware/multi-user hostile tenancy | ephemeral VM/managed sandbox |

Demo B deklaruje T1. Nie wolno komunikować go jako T2/T3.

## Assets

- macOS filesystem i prywatne dane,
- Hermes Brain memory/sessions/credentials,
- Git repositories i branch refs,
- push credentials,
- `hb.db` i approval records,
- effective policy,
- candidate/review artifacts,
- Kanban state,
- Docker daemon i Linux VM.

## Aktorzy

- **Human admin** — zaufany.
- **Hermes Brain model** — pomocny, ale podatny na prompt injection; bez admin capability.
- **Hermes runtime code** — zaufany w T1.
- **Worker model** — niezaufany decydent narzędziowy.
- **Candidate code/tests** — niezaufany kod wykonywalny.
- **Project repository content** — może zawierać prompt injection lub złośliwe hooks/config.
- **Model/provider/network** — zewnętrzna zależność; source code może opuścić VM do providera.

## Główne zagrożenia i controls

| ID | Zagrożenie | Control | Fail-closed test |
|---|---|---|---|
| TM-01 | Worker czyta macOS home | Lima bez broad mount | odczyt poza transfer folderem niemożliwy |
| TM-02 | Worker przejmuje Docker daemon | brak socket/CLI w workloadzie | `/var/run/docker.sock` nie istnieje |
| TM-03 | Worker modyfikuje inne repo/refs | jeden workspace, brak używalnego `.git` | Git i sibling paths niedostępne |
| TM-04 | Skill forwarduje token | allowlist skills + inspect implicit env/credentials | policy validator odrzuca credential-bearing skill |
| TM-05 | `network none`, ale web tool exfiltruje | host tool allowlist | web/browser/MCP nie ma w schema workera |
| TM-06 | Policy rozszerzona przez task branch | registry/trusted-base resolution | zmiana task config nie wpływa na effective policy |
| TM-07 | Worker zmienia pliki po testach | stop → snapshot | container nie działa przed snapshotem |
| TM-08 | Candidate atakuje VM przez test | fresh verifier sandbox | check nigdy nie uruchamia się lokalnie |
| TM-09 | Approval dotyczy starego artefaktu | bind OIDs/hashes | każda zmiana unieważnia approval |
| TM-10 | Main zmienia się po approval | fast-forward-only base check | integrate zwraca `STALE_BASE` |
| TM-11 | Brain sam się zatwierdza | admin capability poza brainem | submitter nie może approve/integrate/push |
| TM-12 | Secret trafia do logs/evidence | central redaction + pointers/hashes | fixture token nie pojawia się w output |
| TM-13 | Persistent container przenosi stan | per-task freshness/cross-process off | drugi task nie widzi canary pierwszego |
| TM-14 | Restart pozostawia nieznany container/worktree | leases + reconcile | doctor wykrywa i klasyfikuje orphan |
| TM-15 | Złośliwy devcontainer rozszerza uprawnienia | trusted image/spec only | task branch `privileged` ignorowany/odrzucony |
| TM-16 | Config/version-lock authority spoofowana przez symlink, world-writable lub obcy-UID trusted dir/plik | no-follow open + `Fstat` (typ + mode-private + owned-by-EUID) na tym samym fd; non-symlink zaufany katalog; walidacja katalogu przed zapisem (ADR-0013) | symlink/permissive/obcy-owner config/version-lock/`ConfigDir` odrzucony fail-closed |

## Granice modelu

- Kontener współdzieli kernel VM. Container escape może przejąć VM.
- Administrator VM może odczytać state.
- LLM provider otrzymuje prompt i kod przekazany modelowi.
- `network none` nie gwarantuje hermetycznego builda, jeśli image nie zawiera dependencies.
- Signature/attestation supply chain nie należy do PoC.
- SSH tunnel nie zabezpiecza procesu po kompromitacji konta VM.

## Security acceptance gates

Demo B nie jest ukończone bez automatycznych negatywnych testów TM-02–TM-13. Sam screenshot konfiguracji lub `docker inspect` nie wystarcza; test musi próbować naruszyć granicę i potwierdzić odmowę.
