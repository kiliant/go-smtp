#!/usr/bin/env bash
set -euo pipefail

report=${1:?usage: comment-apidiff.sh REPORT PR_NUMBER}
pr=${2:?usage: comment-apidiff.sh REPORT PR_NUMBER}
marker='<!-- go-smtp-apidiff -->'
body=$(jq -Rs '{body: .}' <"$report")
comment_id=$(gh api --paginate "repos/$GITHUB_REPOSITORY/issues/$pr/comments" --jq ".[] | select(.body | contains(\"$marker\")) | .id" | head -n 1)
if [[ -n "$comment_id" ]]; then
  gh api --method PATCH "repos/$GITHUB_REPOSITORY/issues/comments/$comment_id" --input - <<<"$body"
else
  gh api --method POST "repos/$GITHUB_REPOSITORY/issues/$pr/comments" --input - <<<"$body"
fi
