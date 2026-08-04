#!/usr/bin/env bash
#
# Report the podman the interop harness will use, and fail early if there is
# none.
#
# Deliberately *not* an exact version assertion. Pinning the runner image's
# podman build (`grep -Fx 'podman version 4.9.3'`) turns every GitHub runner
# image rollout into a red nightly for a reason unrelated to this project, and
# the harness does not depend on that build anyway — interop/harness/container.go
# resolves podman with exec.LookPath and shells out to a stable CLI surface.
# What is worth failing on is podman being absent, and what is worth recording
# is which version produced a given run's results.
set -euo pipefail

if ! command -v podman >/dev/null 2>&1; then
  echo "no container engine found: the interop harness requires podman" >&2
  exit 1
fi

version="$(podman --version 2>&1 || true)"
echo "interop container engine: podman — ${version}"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  echo "Container engine: \`podman\` — ${version}" >> "$GITHUB_STEP_SUMMARY"
fi
