#!/usr/bin/env bash
set -euo pipefail

tags=${1:-interop}
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/go-smtp-interop.XXXXXX")
trap 'rm -rf -- "$work_dir"' EXIT
status=0

run_suite() {
  local name=$1
  shift
  echo "===== $name ====="
  set +e
  go test -v -count=1 -race -tags="$tags" "$@" 2>&1 | tee "$work_dir/$name.log"
  local command_status=${PIPESTATUS[0]}
  set -e
  printf '%d\n' "$command_status" >"$work_dir/$name.status"
  if (( command_status != 0 )); then status=1; fi
}

# These processes stay sequential because each owns an independent harness
# lifecycle and parallel runs collide on container names and host resources.
run_suite smtpclient ./smtpclient
run_suite interop ./interop/...
run_suite examples ./examples

summary=${GITHUB_STEP_SUMMARY:-/dev/null}
{
  echo '### Interoperability matrix'
  echo
  echo "Build tags: \`$tags\`."
  echo
  echo '| Suite | Explicit skip lines | Result |'
  echo '|---|---:|---|'
  for name in smtpclient interop examples; do
    skips=$(grep -Ec -- '--- SKIP:|SKIP ' "$work_dir/$name.log" || true)
    command_status=$(<"$work_dir/$name.status")
    if (( command_status == 0 )); then result=PASS; else result=FAIL; fi
    printf '| `%s` | %d | %s |\n' "$name" "$skips" "$result"
  done
  for name in smtpclient interop examples; do
    skips=$(grep -Ec -- '--- SKIP:|SKIP ' "$work_dir/$name.log" || true)
    if (( skips > 0 )); then
      echo
      echo "<details><summary>$name skips</summary>"
      echo
      echo '```text'
      grep -E -- '--- SKIP:|SKIP ' "$work_dir/$name.log" || true
      echo '```'
      echo '</details>'
    fi
  done
} >>"$summary"

exit "$status"
