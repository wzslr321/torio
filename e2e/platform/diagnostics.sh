#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TIMEOUT="${ROOT}/e2e/platform/with_timeout.py"
TORIO_INSTANCE="${TORIO_INSTANCE:-torio-ci-local}"
ARTIFACT_DIR="${PLATFORM_E2E_ARTIFACT_DIR:-${TMPDIR:-/tmp}/torio-platform-e2e-artifacts}"
mkdir -p "${ARTIFACT_DIR}"

if ! command -v limactl >/dev/null 2>&1; then
  printf 'limactl is unavailable\n' >"${ARTIFACT_DIR}/limactl-unavailable.txt"
  exit 0
fi

python3 "${TIMEOUT}" 20 limactl --version \
  >"${ARTIFACT_DIR}/outer-limactl-version.txt" 2>&1 || true
python3 "${TIMEOUT}" 20 limactl list --json \
  >"${ARTIFACT_DIR}/outer-lima-list.json" \
  2>"${ARTIFACT_DIR}/outer-lima-list.stderr.txt" || true

found=0
instance_dir="${HOME}/.lima/${TORIO_INSTANCE}"
while IFS= read -r name; do
  if [[ "${name}" == "${TORIO_INSTANCE}" ]]; then
    found=1
    break
  fi
done < <(python3 "${TIMEOUT}" 20 limactl list --quiet 2>/dev/null || true)

if [[ "${found}" == "0" ]]; then
  exit 0
fi

if [[ -d "${instance_dir}" ]]; then
  shopt -s nullglob
  for log in "${instance_dir}"/serial*.log; do
    cp "${log}" "${ARTIFACT_DIR}/$(basename "${log}")" || true
  done
  shopt -u nullglob
  printf '%s\n' "${instance_dir}/ha.sock" > "${ARTIFACT_DIR}/hostagent-socket-path.txt"
  if [[ -f "${instance_dir}/ha.stderr.log" ]]; then
    cp -f "${instance_dir}/ha.stderr.log" "${ARTIFACT_DIR}/hostagent-stderr.log" || true
  fi
  if [[ -f "${instance_dir}/ha.stdout.log" ]]; then
    cp -f "${instance_dir}/ha.stdout.log" "${ARTIFACT_DIR}/hostagent-stdout.log" || true
  fi
fi

python3 "${TIMEOUT}" 30 limactl shell --tty=false "${TORIO_INSTANCE}" -- \
  sudo -n journalctl -u cloud-final --no-pager -n 200 \
  >"${ARTIFACT_DIR}/outer-guest-cloud-final.txt" 2>&1 || true
python3 "${TIMEOUT}" 30 limactl shell --tty=false "${TORIO_INSTANCE}" -- \
  sudo -n -u hermes -- journalctl --user -u hermes-serve.service --no-pager -n 200 \
  >"${ARTIFACT_DIR}/outer-guest-hermes-serve.txt" 2>&1 || true
