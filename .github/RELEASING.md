# Releasing go-smtp

Releases are deliberate compatibility events. Do not tag from an unreviewed
working tree or treat a green unit-test run as the complete checklist.

## Version and tag format

- Releases use Semantic Versioning and an annotated tag named
  `vMAJOR.MINOR.PATCH`, for example `v1.0.0`.
- Release candidates use `vMAJOR.MINOR.PATCH-rc.N`, beginning with `.1`.
- v0 releases may break the exported API, but each break must be called out in
  CHANGELOG.md. At v1 and later, incompatible API changes are blocked by CI and
  require a new major version.
- The tag and GitHub release must point at the same reviewed commit. Never move
  or reuse a published tag.

## Pre-tag checklist

Run this checklist on the exact candidate commit:

1. The worktree is clean and every commit follows Conventional Commits.
2. `CHANGELOG.md` has a dated version section. Every exported symbol addition or
   change says **Exported API**, and every pre-v1 incompatibility says
   **Breaking exported API change (pre-v1)**.
3. `docs/RFC-COVERAGE.md` has no stray `planned` or `in progress` row intended
   for this release. Deferred rows remain explicit and justified.
4. CI passes on Go 1.24 (the `go.mod` floor) and 1.26 (the current release),
   including race, gofmt, vet under every interop tag set, the zero-dependency
   gate, Staticcheck 2026.1 (`v0.7.0`), compiled examples, and the API-surface
   gates.
5. Run the discovered fuzz campaign for 10 minutes per target:
   `.github/scripts/run-fuzz.sh 10m 2`. Review every failure and ensure the
   discovered-target count matches the result count.
6. Re-run the native Podman matrix and inspect visible skips:
   `.github/scripts/run-interop.sh interop`. For a major or final v1 release,
   also run Tier 3 explicitly:
   `.github/scripts/run-interop.sh 'interop interop_emulated'`.
7. Run the pinned apidiff tool against the latest release tag and review both
   compatible and incompatible changes. Before v1, reconcile every incompatible
   line with CHANGELOG.md. At and after v1, there must be no incompatibilities.
8. Confirm there are no unreviewed dependency additions (`go.sum` must remain
   absent), no unpinned CI tools or container images, and no generated-file
   drift.

The nightly jobs are evidence, not a substitute for this rerun: record links to
the candidate commit's CI, fuzz, and interop runs in the release notes.

## Cutting a release candidate

1. Decide the target version and move Unreleased entries to a heading such as
   `## [1.0.0-rc.1] - YYYY-MM-DD`; restore an empty Unreleased heading above it.
2. Commit with `chore(release): prepare v1.0.0-rc.1` and obtain review.
3. Complete the full checklist above on that commit.
4. Create the annotated tag: `git tag -a v1.0.0-rc.1 -m 'go-smtp v1.0.0-rc.1'`.
5. Push the commit and tag only after approval, then create a GitHub prerelease
   whose notes reproduce the matching changelog section and evidence links.
6. Collect interoperability and API feedback. A later RC increments `N`; never
   replace the previous tag.

## Cutting the final release

Repeat the candidate process with the final version. Review apidiff both against
the latest stable tag and against the final RC so the stable compatibility
baseline and RC-only changes are both understood. Create an annotated final tag
and a non-prerelease GitHub release, then verify that the next pull request's
apidiff report names that tag as its baseline.

After publishing, add comparison links for the new changelog sections and keep
the next Unreleased section open.
