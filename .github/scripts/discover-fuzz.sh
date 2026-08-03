#!/usr/bin/env bash
set -euo pipefail

# Print package<TAB>target for every fuzz target visible under the default
# build tags. Discovery is intentionally live: a newly added Fuzz function is
# included without editing CI.
while IFS= read -r package; do
  while IFS= read -r target; do
    [[ "$target" =~ ^Fuzz[A-Za-z0-9_]+$ ]] || continue
    printf '%s\t%s\n' "$package" "$target"
  done < <(go test -list '^Fuzz' "$package")
done < <(go list ./...)
