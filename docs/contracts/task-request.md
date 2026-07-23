# Task request contract

Task request opisuje intencję, nie capability.

Dozwolone pola:

- project ID,
- title,
- specification/body,
- requested base ref,
- acceptance criteria,
- labels,
- idempotency key.

Request NIE MOŻE ustawiać:

- image,
- network,
- mounts,
- env/credentials,
- skills lub toolsets poza ewentualną sugestią ignorowaną przez enforcement,
- verification commands,
- integration mode,
- push target.

Control plane wiąże request z effective project policy. Jeśli request wymaga capability spoza policy, zwraca `POLICY_DENIED`, zamiast automatycznie rozszerzać policy.

Request trafiający z Braina jest untrusted input i podlega limitom rozmiaru oraz redakcji logów.
