#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TIMEOUT="${ROOT}/e2e/platform/with_timeout.py"
TORIO_INSTANCE="${TORIO_INSTANCE:-torio-ci-local}"

if ! command -v limactl >/dev/null 2>&1; then
  exit 0
fi

list_names() {
  python3 "${TIMEOUT}" 10 limactl list --quiet
}

# Returns 0 when present, 1 when absent, and 2 when Lima state is unreadable.
instance_exists() {
  local names name
  if ! names="$(list_names)"; then
    return 2
  fi
  while IFS= read -r name; do
    if [[ "${name}" == "${TORIO_INSTANCE}" ]]; then
      return 0
    fi
  done <<<"${names}"
  return 1
}

set +e
instance_exists
instance_rc=$?
set -e
case "${instance_rc}" in
  0) ;;
  1) exit 0 ;;
  *) printf 'platform E2E cleanup: cannot read Lima instance state\n' >&2; exit 1 ;;
esac

python3 "${TIMEOUT}" 30 limactl stop --tty=false "${TORIO_INSTANCE}" || true

# Worst case is 202 seconds, below the workflow's five-minute cleanup budget.
for attempt in 1 2 3; do
  python3 "${TIMEOUT}" 40 \
    limactl delete --force --tty=false "${TORIO_INSTANCE}" || true

  set +e
  instance_exists
  instance_rc=$?
  set -e
  if [[ ${instance_rc} -eq 1 ]]; then
    exit 0
  fi
  sleep "$((attempt * 2))"
done

printf 'platform E2E cleanup: failed to remove instance %s\n' "${TORIO_INSTANCE}" >&2
exit 1
