# Zadanie: Etap 0 — runtime contract spike

Przeczytaj:
- AGENTS.md
- docs/07-source-verification.md
- docs/plans/01-spike.md
- docs/adr/0002-hermes-native-orchestration.md
- docs/adr/0004-native-docker-poc.md
- docs/adr/0005-control-plane-git.md
- docs/adr/0007-policy-includes-tools-skills.md

Wykonaj wyłącznie Etap 0. Nie twórz kodu produkcyjnego w `cmd/` ani `internal/`.

Dopuszczalne:
- throwaway scripts i fixtures w `spikes/`,
- testowe repo/board/profile/container bez realnych credentials,
- aktualizacja `docs/spike-results/`,
- korekta contracts/ADR po realnym evidence.

Wymagane:
1. Wykonuj S0–S8 po kolei.
2. Po każdym eksperymencie zapisuj command, exit code, redacted output i conclusion.
3. Nie uznawaj HTTP status za dowód Desktop/WebSocket.
4. Nie uznawaj wygenerowanej konfiguracji za dowód izolacji; wykonaj negatywne próby.
5. Dla skills używaj canary, nigdy realnych tokenów.
6. Dla Docker task A/B sprawdź cross-task leakage.
7. Dla Git sprawdź tracked/untracked/deleted/mode/symlink oraz brak dostępu do innych refs.
8. Dla verifiera wykonaj exact frozen snapshot w świeżym kontenerze.
9. Zakończ `docs/spike-results/99-decision.md` z osobnym GO/NO-GO dla Demo A i Demo B.

Nie rozpoczynaj Demo A w tej samej sesji. Jeśli środowisko lokalne nie ma macOS/Lima/Desktop, wykonaj tylko osiągalne testy i oznacz resztę BLOCKED, a nie PASS.

Final response:
- tabela S0–S8: PASS/FAIL/BLOCKED,
- linki do evidence,
- zmienione założenia/ADR-y,
- blockers,
- dokładna rekomendowana następna czynność.
