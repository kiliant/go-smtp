#!/usr/bin/env bash
set -euo pipefail

output=${1:-apidiff-report.md}
# The repository workspace may contain independently versioned nested modules.
# This gate protects the released root module; each nested module owns its own
# baseline and release gate.
module=$(GOWORK=off go list -m -f '{{.Path}}')
baseline=$(git tag --list 'v[0-9]*' --sort=-v:refname | head -n 1)
marker='<!-- go-smtp-apidiff -->'

if [[ -z "$baseline" ]]; then
  {
    echo "$marker"
    echo '## API compatibility report'
    echo
    echo '**Baseline:** none.'
    echo
    echo 'No `v*` release tag exists yet. This is an explicit pre-release baseline report, not a silently skipped comparison. The first release tag will become the baseline for subsequent pull requests.'
  } >"$output"
  echo "baseline=none" >>"${GITHUB_OUTPUT:-/dev/null}"
  exit 0
fi

tmp=$(mktemp -d "${TMPDIR:-/tmp}/go-smtp-apidiff.XXXXXX")
trap 'rm -rf -- "$tmp"' EXIT
mkdir "$tmp/old" "$tmp/new"
git archive "$baseline" | tar -x -C "$tmp/old"
# A plain copy, not another git archive: this must see uncommitted local
# changes too, the same way the rest of this gate's output does when a
# developer runs it before committing.
rsync -a --exclude=.git --exclude=.state --exclude=interop -- ./ "$tmp/new/"

# interop/** is dropped from both snapshots before apidiff ever sees them.
# It is harness/test tooling housed inside this module's directory tree, not
# the released API this gate protects, and at least one profile
# (interop/servers/gosmtp) has an ordinary, non-test-file import of the
# nested smtpserver module — resolvable in normal builds only via the
# repository's go.work, which this gate deliberately runs with GOWORK=off to
# see the module the way a real consumer without that workspace file would.
# Removing interop/** from the copies given to apidiff -m sidesteps that
# entirely rather than trying to teach apidiff a package exclusion it has no
# flag for.
rm -rf "$tmp/old/interop"

(cd "$tmp/old" && GOWORK=off apidiff -m -w "$tmp/old.export" "$module")
(cd "$tmp/new" && GOWORK=off apidiff -m -w "$tmp/new.export" "$module")
apidiff -m "$tmp/old.export" "$tmp/new.export" >"$tmp/all.txt"
apidiff -m -incompatible "$tmp/old.export" "$tmp/new.export" >"$tmp/incompatible.txt"

{
  echo "$marker"
  echo '## API compatibility report'
  echo
  echo "**Baseline:** \`$baseline\`"
  echo
  if [[ -s "$tmp/all.txt" ]]; then
    echo '```text'
    sed -n '1,600p' "$tmp/all.txt"
    echo '```'
  else
    echo 'No exported API differences.'
  fi
} >"$output"

major=${baseline#v}
major=${major%%.*}
post_v1=false
if [[ "$major" =~ ^[0-9]+$ ]] && (( major >= 1 )); then
  post_v1=true
fi
{
  echo "baseline=$baseline"
  echo "post_v1=$post_v1"
  if [[ -s "$tmp/incompatible.txt" ]]; then echo 'incompatible=true'; else echo 'incompatible=false'; fi
} >>"${GITHUB_OUTPUT:-/dev/null}"

if [[ "$post_v1" == true && -s "$tmp/incompatible.txt" ]]; then
  echo "incompatible API changes relative to $baseline" >&2
  cat "$tmp/incompatible.txt" >&2
  exit 1
fi
