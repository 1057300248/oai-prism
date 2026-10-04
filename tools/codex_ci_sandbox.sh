#!/usr/bin/env bash
# CI-only sandbox prerequisite, not a production deployment script.
# Keeps Codex workspace-write/read-only, seccomp and network sandboxing enabled.
# Ubuntu's AppArmor userns restriction needs a profile for the bwrap executable:
# https://developers.openai.com/codex/concepts/sandboxing#prerequisites
set -euo pipefail
if [[ "${GITHUB_ACTIONS:-}" != true || "${RUNNER_OS:-}" != Linux || $# -eq 0 ]]; then
  echo 'This wrapper is only for disposable Linux GitHub Actions runners.' >&2
  exit 2
fi
if ! command -v bwrap >/dev/null; then
  sudo apt-get update -qq
  sudo apt-get install -y bubblewrap
fi
BWRAP=$(readlink -f "$(command -v bwrap)")
if [[ "$BWRAP" != /usr/bin/bwrap ]]; then
  echo 'Refusing to authorize an unexpected bubblewrap executable.' >&2
  exit 2
fi
PROFILE="${RUNNER_TEMP:?}/oaiprism-ci-bwrap-${GITHUB_RUN_ID:?}.profile"
LOADED=false
cleanup() {
  if [[ "$LOADED" == true ]]; then sudo apparmor_parser --remove "$PROFILE" || true; fi
  rm -f "$PROFILE"
}
trap cleanup EXIT
if [[ -r /sys/module/apparmor/parameters/enabled ]] && grep -q Y /sys/module/apparmor/parameters/enabled; then
  command -v apparmor_parser >/dev/null || { echo 'AppArmor parser is required on this runner.' >&2; exit 2; }
  cat > "$PROFILE" <<'PROFILE_BODY'
abi <abi/4.0>,
include <tunables/global>
profile oaiprism-ci-bwrap /usr/bin/bwrap flags=(unconfined) {
  userns,
}
PROFILE_BODY
  sudo apparmor_parser --replace "$PROFILE"
  LOADED=true
fi
# This changes neither global kernel sysctls nor the Codex permission policy.
"$@"
