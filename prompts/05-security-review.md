# Zadanie: adversarial T1 security review

Traktuj worker model, repository content i candidate code jako złośliwe. Hermes runtime/control plane/VM admin są zaufane wyłącznie na poziomie T1.

Przeczytaj docs/04-threat-model.md i sprawdź TM-01–TM-15. Dla każdej granicy odpowiedz:
- asset,
- attacker capability,
- entry point,
- current enforcement,
- bypass attempt,
- real test evidence,
- residual risk.

Obowiązkowo spróbuj wykazać:
- Docker socket/group/CLI exposure,
- sibling repo/host home/Git metadata access,
- cross-task container leakage,
- host-side egress mimo network none,
- implicit skill env/credential mounts,
- policy self-expansion z task branch,
- mutation po verification,
- verifier host execution,
- approval reuse po zmianie tuple,
- integration po base drift,
- brain/worker admin impersonation,
- secret leakage w logs/errors.

Nie akceptuj prompt instructions jako control. Nie akceptuj config snapshotu jako proof bez bypass attempt. Zakończ GO/NO-GO dla aktualnego etapu.
