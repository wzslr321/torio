# Prompty dla LLM-a

Prompty zakładają uruchomienie agenta w root repozytorium. `AGENTS.md` pozostaje nadrzędny.

| Prompt | Użycie |
|---|---|
| `00-implementer-system.md` | trwały project/system context dla coding agenta |
| `01-spike.md` | obowiązkowy pierwszy etap, bez kodu produkcyjnego |
| `02-demo-a.md` | wybór i realizacja jednego następnego slice Demo A |
| `03-demo-b.md` | wybór i realizacja jednego następnego slice Demo B |
| `04-code-review.md` | niezależny correctness/maintainability review |
| `05-security-review.md` | adversarial review granic T1 |
| `06-handoff.md` | zakończenie sesji i przygotowanie następnego agenta |
| `task-template.md` | szablon jednego małego taska |

Nie uruchamiaj całego Demo B jednym promptem. Jedna sesja implementacyjna powinna zamknąć jeden pionowy slice, jego testy, dokumentację i evidence.
