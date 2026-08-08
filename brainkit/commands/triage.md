---
description: Empty the inbox — merge, promote, action or drop every captured item
---

Process the Second Brain inbox to zero.

Use the `brain-triage` skill, which owns the four outcomes and the
search-before-deciding rule. This command runs it end to end.

1. Resolve the vault (`${CLAUDE_PLUGIN_ROOT}/STANDARD.md` §7) and list `inbox/`,
   oldest first.
2. If the inbox is empty, say so and stop. That is a good result, not a wasted
   command.
3. If it holds more than ten items, hand the whole set to the `brain-librarian`
   subagent and report what it did. Reading thirty captures here costs the user
   the context they are working in.
4. Otherwise work each item: search for a merge target, then merge, promote,
   turn into an action, or drop.
5. Delete each capture only after its content has landed somewhere else.

Finish with a table — item, outcome, destination — and, separately, any item you
could not place, with the two candidates you were choosing between. Ask about
those in one batch at the end; never guess between two existing notes, because a
merge into the wrong one is invisible afterwards.

If `$ARGUMENTS` names a file or a filter, triage only what it matches, and say
how many items you left untouched.
