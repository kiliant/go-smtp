# T13 — Release engineering

**Agent:** `docs-release` · **Milestone:** M4 · **Depends on:** T11, T12

**Owns:** `.github/**`, `CHANGELOG.md`

## Goal

Make the rules enforceable by machine, so they survive the humans and agents who
stop paying attention.

## Deliverables

### CI jobs

| Job | Trigger | Gate |
|---|---|---|
| `go test ./...` on the two most recent Go majors | every PR | must pass |
| `go test -race ./...` | every PR | must pass |
| `gofmt -l` and `go vet` | every PR | must be clean |
| `staticcheck` | every PR | must be clean |
| `examples/**` build | every PR | must compile |
| fuzz smoke, 60 s per discovered target | every PR | must pass |
| fuzz long-run, 30 min per target | nightly | reported |
| interop matrix | nightly | reported, not blocking |
| `apidiff` against the previous tag | every PR | see below |

Two of these encode failures the sibling repo actually had: `gofmt` was dirty and
`staticcheck` had 21 findings at its freeze audit, and nothing compiled the
examples directory at all.

### The apidiff gate

`golang.org/x/exp/cmd/apidiff` against the previous tag.

- **Pre-v1.0:** post the diff as a PR comment. The point is that a break is
  deliberate rather than accidental.
- **Post-v1.0:** an incompatible change fails the build.

This is the mechanism that makes the v1.0 promise real rather than aspirational.
Ship it before the tag, not after.

### Fuzz jobs discover targets

Reuse the discovery approach in `.state/run-fuzz.sh`: enumerate packages, then
`go test -list '^Fuzz'` within each. **Never a hand-maintained list.** In the
sibling repo a hand-maintained list is exactly how three extension groups shipped
with no fuzz targets — nothing failed, the list simply did not mention them.

Note the script's comments before adapting it: the `jobs -r | wc -l` subshell
bug, `wait -n` needing bash 4.3 which macOS does not ship, and oversubscription
producing 11 spurious failures in one campaign. Those are recorded incidents, not
style notes.

### Interop in CI

The matrix needs a container runtime and is slow, so it runs nightly and reports
rather than blocking PRs. Tier 3 (emulated) is opt-in even there.

Skips must be visible in the summary. A run that skipped everything and reported
green is the failure mode `docs/INTEROP.md` exists to prevent.

### CHANGELOG.md

Keep-a-Changelog format, Conventional Commits as the input. Every entry touching
an exported symbol says so explicitly. Pre-v1.0 breaking changes get their own
section per release — that record is what justifies the freeze when it comes.

### Release process

Document it in the file, not in someone's memory: tag format, the pre-tag
checklist (full fuzz campaign, interop re-run, apidiff review, coverage doc has
no stray `planned` rows), and how a release candidate is cut.

## Done when

Every job above runs on a PR; the apidiff gate is active in its pre-v1.0 mode;
the nightly fuzz and interop jobs have each completed at least one successful
run; `CHANGELOG.md` covers the history to date; the release process is written
down.
