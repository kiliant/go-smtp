# T31 — `smtpdeliver` API review, docs, release (D07)

**Agent:** docs-release + api-guardian · **Milestone:** M7 · **Depends on:** T30

**Owns:** `smtpdeliver` doc comments and `smtpdeliver/example_*_test.go`; the
`CHANGELOG.md` release section (T24–T30 appended to `[Unreleased]`); the
`smtpdeliver` rows of `docs/RFC-COVERAGE.md`; the delivery sections of
`docs/ARCHITECTURE.md`, `docs/ROADMAP.md` (M7 evidence) and
`docs/API-STABILITY.md`, if a precedent was set; and the release itself.

This is work package D07 of `docs/DELIVERY-DESIGN.md` §11.

**The root module is v1. `smtpdeliver` ships from it and is therefore stable from
its first tag.** There is no v0 phase to absorb a mistake, unlike T23. That is
why this review is the last gate and not a formality.

## Part 1 — the API review

api-guardian reviews the **complete** exported surface of `smtpdeliver`, plus
T24's `smtpclient` addition if it has not shipped yet, against CLAUDE.md's
acceptance criterion. Can the next routing input, the next DNS lookup, the next
transport policy, or the next outcome field be added without a breaking change?
Check at least:

- Address-literal routing as a new `Destination` field;
- parallel destination execution as a new options field;
- a fifth `Resolver` lookup;
- a new `PolicyKind` (say, a successor to MTA-STS);
- connection reuse added later as a callback or a method.

Each must come out additive. If one does not, the shape changes **now**.

## Part 2 — documentation

- A package doc that states the boundary plainly. This is a delivery
  *attempt* library, not a queue, not a scheduler, and not an MTA. Point to
  design §1's out-of-scope list.
- The in-memory MTA-STS cache's restart caveat in its doc comment and in an
  example.
- Runnable examples: a basic `Deliver`, a durable `PolicyCache` over a map plus a
  file (as an *example* of storage, not shipped storage), a DNSSEC-aware
  `Resolver` adapter sketch with **no import** of any DNS library, and a queue
  loop that reads `Disposition`, including `indeterminate`.
- `docs/RFC-COVERAGE.md`: the 5321 §5, 7505, 8461 and 7672 rows go from
  "post-v1.0" to implemented, with the package path.
- `docs/ARCHITECTURE.md` and the layering tree: `smtpdeliver` added.
  **CLAUDE.md's layering tree is the human's file.** Propose the edit and do not
  make it.

## Part 3 — the release

- The version is the next root **minor**. `DELIVERY-DESIGN.md` §1 expected
  `v1.1.0`, but `v1.1.0` shipped 2026-08-21 with the `smtpserver` vocabulary. The
  version is therefore `v1.2.0`, or `v1.3.0` if T24 shipped alone as `v1.2.0`.
  Correct the stale sentence in the design document as a documentation fix, with
  the human's approval, because that document is approved.
- If the integration-branch option from T25 was chosen, merge
  `feat/smtpdeliver` into `main` via PR, so that `apidiff` and `CI required`
  run.
- A signed, annotated tag. Release evidence on the exact tagged tree: CI, Fuzz
  (long) and Interop run IDs, as for `v1.0.0`.
- A clean external consumer: a throwaway module outside the repository with
  `GOWORK=off` and `GOFLAGS=-mod=mod`, which downloads the tag and runs an
  example.
- A GitHub Release marked Latest.

## Done when

api-guardian has approved the complete surface. The examples run. The release
evidence and the external-consumer check are recorded in
`.state/progress/T31.md`. The tag and the GitHub Release are published. ROADMAP
M7 lists its evidence.
