#!/bin/sh
# Put a map of the vault in front of the agent at the start of a session.
#
# Everything else in this kit is a skill, which is a request: the model decides
# whether a prompt is the kind of prompt that should reach for the vault. That
# decision is a good one to leave to a model. Knowing the vault exists is not.
#
# This hook closes that half. It costs a few hundred tokens, it runs once per
# session, and after it the model is never choosing between "search the vault"
# and "I have no vault" — only between "this question turns on something written
# down" and "it does not".
#
# Three rules govern every line below.
#
# It is silent unless it is sure. No vault, no `index.md`, no `type: vault`:
# print nothing and exit 0. A directory called `brain` that is not a vault is
# somebody else's data, and describing it here would be the first step to
# writing into it.
#
# It never fails a session. This runs at the start of every session in every
# repository, including all the ones with no vault anywhere near them. Any
# error is silence.
#
# It carries the map, not the contents. Directory names, curated index titles
# and counts — never note bodies. The map is what makes retrieval targeted; the
# contents are what retrieval is for.

set -u

# Any unexpected exit — an unreadable file, a directory that vanished between
# two commands — leaves the session exactly as it found it.
trap 'exit 0' EXIT

emit_nothing() {
	exit 0
}

# STANDARD.md §7 resolves the vault path in four steps. A shell hook can do the
# first and the third: the second lives in the agent's own memory file, and the
# fourth is a question, which is not a hook's to ask.
vault=""
if [ -n "${BRAIN_VAULT:-}" ] && [ -d "${BRAIN_VAULT}" ]; then
	vault="${BRAIN_VAULT}"
elif [ -d "${HOME:-}/brain" ]; then
	vault="${HOME}/brain"
fi
[ -n "${vault}" ] || emit_nothing

index="${vault}/index.md"
[ -f "${index}" ] || emit_nothing

# The test that separates a vault from a directory that happens to be called
# one. STANDARD.md §7 makes this the identifying mark, and treating a plain
# folder as a vault is the worst failure mode this kit has.
head -n 20 "${index}" 2>/dev/null | grep -q '^type: *vault' || emit_nothing

notes=$(find "${vault}" -name '*.md' -type f 2>/dev/null | wc -l | tr -d ' ')
unrouted=$(find "${vault}/inbox" -name '*.md' -type f 2>/dev/null | wc -l | tr -d ' ')

# Built in one shot into a variable: no temporary file to leave behind, and the
# whole map is bounded by construction rather than by truncating it afterwards.
# Truncation is what this deliberately avoids — cutting escaped text can split
# an escape sequence, and an unparseable document is worse than a short one.
map=$(
	{
		printf 'The user keeps a Second Brain vault at %s.\n' "${vault}"
		printf 'It holds %s notes, %s of them unrouted in inbox/.\n' "${notes}" "${unrouted}"
		printf 'This is the vault the brain-kit skills read and write.\n\n'

		# The root index is curated prose about what this vault is for, which
		# is the single most useful thing to carry. Twenty-five lines is enough
		# for a map and short of enough for an essay.
		printf 'Its root index says:\n\n'
		awk 'NR == 1 && $0 == "---" { inside = 1; next }
		     inside && $0 == "---" { inside = 0; next }
		     !inside { print }' "${index}" 2>/dev/null | sed -n '1,25p'

		# One line per directory. A directory that curates itself gets its own
		# description; one that does not is named and left at that, because
		# inventing a description here would be this hook guessing.
		printf '\nDirectories:\n'
		for dir in "${vault}"/*/; do
			[ -d "${dir}" ] || continue
			name=$(basename "${dir}")
			# Not `case`: a pattern's `)` closes the command substitution this
			# loop runs inside, on more shells than it does not.
			[ "${name}" = "attachments" ] && continue
			[ "${name}" = "${name#.}" ] || continue
			count=$(find "${dir}" -name '*.md' -type f 2>/dev/null | wc -l | tr -d ' ')
			about=""
			if [ -f "${dir}index.md" ]; then
				about=$(grep -m 1 '^description:' "${dir}index.md" 2>/dev/null |
					cut -d: -f2- | sed 's/^ *//' | cut -c 1-120)
			fi
			unit="notes"
			[ "${count}" = "1" ] && unit="note"
			if [ -n "${about}" ]; then
				printf -- '- %s/ (%s %s) — %s\n' "${name}" "${count}" "${unit}" "${about}"
			else
				printf -- '- %s/ (%s %s)\n' "${name}" "${count}" "${unit}"
			fi
		done

		printf '\nConsult it when a task turns on something the user already decided, wrote or\n'
		printf 'was told: a convention, a past decision, a person, a project. Leave it alone\n'
		printf 'when the task does not. It is private, most of it is irrelevant to any one\n'
		printf 'question, and reading it in bulk puts all of it in the transcript.\n'
	} 2>/dev/null | sed -n '1,120p'
)
[ -n "${map}" ] || emit_nothing

# JSON assembled by hand, because a kit whose only stated requirement is the
# agent itself does not get to depend on jq or on an interpreter being present.
escaped=$(printf '%s\n' "${map}" | awk 'BEGIN { ORS = "" }
	{
		gsub(/\\/, "\\\\")
		gsub(/"/, "\\\"")
		gsub(/\t/, "\\t")
		gsub(/\r/, "")
		printf "%s\\n", $0
	}' 2>/dev/null)
[ -n "${escaped}" ] || emit_nothing

trap - EXIT
printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"%s"}}\n' "${escaped}"
