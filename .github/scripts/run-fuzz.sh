#!/usr/bin/env bash
set -euo pipefail

duration=${1:?usage: run-fuzz.sh DURATION [MAX_JOBS]}
max_jobs=${2:-2}
if ! [[ "$max_jobs" =~ ^[1-9][0-9]*$ ]]; then
  echo "MAX_JOBS must be a positive integer" >&2
  exit 2
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/go-smtp-fuzz.XXXXXX")
trap 'rm -rf -- "$work_dir"' EXIT
targets="$work_dir/targets.tsv"
"$script_dir/discover-fuzz.sh" >"$targets"
target_count=$(awk 'END { print NR + 0 }' "$targets")
if (( target_count == 0 )); then
  echo "fuzz discovery found no targets" >&2
  exit 1
fi

echo "discovered $target_count fuzz targets; duration=$duration max_jobs=$max_jobs"

# xargs -P is the worker pool. Do not replace this with `jobs -r | wc -l`:
# command substitution runs jobs in a subshell and sees no parent jobs. Do not
# use `wait -n` either; macOS's Bash 3.2 lacks it. Keeping max_jobs explicit
# also prevents the oversubscription that previously caused spurious failures.
pool_status=0
while IFS=$'\t' read -r package target; do
  printf '%s\0%s\0' "$package" "$target"
done <"$targets" | xargs -0 -n 2 -P "$max_jobs" bash -c '
  set -uo pipefail
  work_dir=$1
  duration=$2
  package=$3
  target=$4
  key=$(printf "%s_%s" "$package" "$target" | tr "/." "__")
  log="$work_dir/$key.log"
  if go test -run "^$" -fuzz "^${target}$" -fuzztime "$duration" "$package" >"$log" 2>&1; then
    printf "PASS\t%s\t%s\n" "$package" "$target" >"$work_dir/$key.result"
  else
    status=$?
    printf "FAIL\t%s\t%s\n" "$package" "$target" >"$work_dir/$key.result"
    exit "$status"
  fi
' _ "$work_dir" "$duration" || pool_status=$?

result_count=$(find "$work_dir" -name '*.result' -type f | awk 'END { print NR + 0 }')
missing_results=0
if (( result_count != target_count )); then
  echo "fuzz worker infrastructure produced $result_count results for $target_count targets" >&2
  while IFS=$'\t' read -r package target; do
    key=$(printf '%s_%s' "$package" "$target" | tr '/.' '__')
    if [[ ! -f "$work_dir/$key.result" ]]; then
      echo "MISSING RESULT: $package $target" >&2
      missing_results=$((missing_results + 1))
    fi
  done <"$targets"
  pool_status=1
fi

summary=${GITHUB_STEP_SUMMARY:-/dev/null}
result_files="$work_dir/result-files.txt"
find "$work_dir" -name '*.result' -type f -print | sort >"$result_files"
{
  echo "### Fuzz campaign"
  echo
  echo "Duration per target: \`$duration\`; maximum workers: \`$max_jobs\`."
  echo
  echo '| Result | Package | Target |'
  echo '|---|---|---|'
  while IFS= read -r result_file; do
    IFS=$'\t' read -r result package target <"$result_file"
    printf '| %s | `%s` | `%s` |\n' "$result" "$package" "$target"
  done <"$result_files"
  if (( missing_results > 0 )); then
    echo
    echo "**Infrastructure failure:** $missing_results discovered targets produced no result file."
  fi
} >>"$summary"

if (( pool_status != 0 )); then
  while IFS= read -r result_file; do
    IFS=$'\t' read -r result package target <"$result_file"
    [[ "$result" == FAIL ]] || continue
    key=$(printf '%s_%s' "$package" "$target" | tr '/.' '__')
    echo "===== FAIL $package $target =====" >&2
    sed -n '1,240p' "$work_dir/$key.log" >&2
  done <"$result_files"
  exit 1
fi
