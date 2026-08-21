#!/usr/bin/env bash
#
# Supply-chain gate: the module must stay dependency-free.
#
# This assertion *is* the zero-dependency policy. Without it the policy erodes
# on the first convenient import, and every erosion is a `go.sum` entry — a
# stability liability this project does not control. See CLAUDE.md, "Zero
# external dependencies": the rule covers test-only dependencies too, which is
# why the check looks at the whole module graph and not just the build list.
#
# Kept deliberately in step with the sibling go-imap repository's copy. The
# duplication is the policy's own consequence: sharing it would mean a module
# dependency between the two repositories.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

ROOT_MODULE="$(GOWORK=off go list -m)"
status=0

# The repository may contain nested modules. Their only permitted non-stdlib
# dependency is the released root module itself; API-STABILITY.md §9 records
# why that self-reference is controlled rather than external. Discover modules
# from go.mod files so adding one cannot silently evade this gate.
while IFS= read -r modfile; do
  module_dir="${modfile%/go.mod}"
  [ "$module_dir" = "$modfile" ] && module_dir=.
  module="$(cd "$module_dir" && GOWORK=off go list -m)"
  echo "checking $module ($module_dir)"

  # 1. Root keeps no go.sum. A nested module may contain sums for the root
  # module only; any other line is an uncontrolled dependency.
  if [ -s "$module_dir/go.sum" ]; then
    if [ "$module" = "$ROOT_MODULE" ]; then
      echo "FAIL: root go.sum is non-empty" >&2
      status=1
    elif grep -v "^${ROOT_MODULE//./\\.} v" "$module_dir/go.sum" | grep -q .; then
      echo "FAIL: $module_dir/go.sum contains a non-root dependency:" >&2
      grep -v "^${ROOT_MODULE//./\\.} v" "$module_dir/go.sum" >&2
      status=1
    else
      echo "ok: go.sum contains only the controlled root-module self-reference"
    fi
  else
    echo "ok: go.sum absent or empty"
  fi

  # 2. A committed replace would make the release work only in this checkout.
  if grep -qE '^[[:space:]]*replace([[:space:](]|$)' "$modfile"; then
    echo "FAIL: $modfile contains a replace directive" >&2
    status=1
  else
    echo "ok: no replace directive"
  fi

  # 3. The complete standalone graph is either just the root module or a
  # nested module plus that root. go.sum cannot hide a transitive addition.
  mods="$(cd "$module_dir" && GOWORK=off go list -m all)"
  unexpected="$(printf '%s\n' "$mods" | awk -v self="$module" -v root="$ROOT_MODULE" '$1 != self && $1 != root')"
  if [ -n "$unexpected" ]; then
    echo "FAIL: $module has unexpected module dependencies:" >&2
    echo "$unexpected" >&2
    status=1
  elif [ "$module" != "$ROOT_MODULE" ] && ! printf '%s\n' "$mods" | grep -q "^${ROOT_MODULE//./\\.} "; then
    echo "FAIL: nested module $module does not depend on a released $ROOT_MODULE" >&2
    status=1
  else
    echo "ok: controlled module graph"
  fi

# 4. Nothing imports a package outside the standard library and this repository.
#    Build tags matter here: an interop-only test dependency is still a
#    dependency, and a plain `go list ./...` never sees those files.
#
#    `.Standard` is the authoritative test, not a regexp on the import path:
#    the standard library vendors x/net and x/crypto under `vendor/`, and go1.26
#    added `crypto/internal/entropy/v1.0.0`, both of which a "contains a dot"
#    heuristic reports as external.
  for tags in "" "interop" "interop interop_emulated"; do
    args=(-deps -test -f '{{if not .Standard}}{{.ImportPath}}{{end}}')
    [ -n "$tags" ] && args+=(-tags "$tags")
    external="$(cd "$module_dir" && go list "${args[@]}" ./... 2>/dev/null \
      | grep -v '^$' \
      | grep -v "^${ROOT_MODULE//./\\.}" || true)"
    if [ -n "$external" ]; then
      echo "FAIL: $module has non-stdlib imports with tags '${tags:-<none>}':" >&2
      echo "$external" >&2
      status=1
    else
      echo "ok: repository/stdlib-only imports with tags '${tags:-<none>}'"
    fi
  done
done < <(find . -name go.mod -not -path './.git/*' -print | sort)

exit "$status"
