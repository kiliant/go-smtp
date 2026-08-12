# T23 — Server API review, docs, examples, `smtpserver` release

**Agent:** docs-release + api-guardian · **Milestone:** M6 ·
**Depends on:** T21, T22

**Owns:** `smtpserver` doc comments, `smtpserver/examples/**`, the nested
module's `CHANGELOG.md` and release process, and the `docs/` updates the server
release implies.

**Implementation waits for the v1.0 tag**, like every other server task. Note the
asymmetry this task exists to manage: the **root** module is frozen at v1 by the
time this runs, and `smtpserver` is **not** — it ships v0.x.

## Part 1 — the API review

`api-guardian` reviews the whole exported `smtpserver` surface, not only the diff
that arrives with this task. Its standing question applies unchanged: *can an
ESMTP extension nobody has written yet be added without a breaking change?*

The rule it is enforcing here is §2's, and it is narrower than the root module's:

> A new extension may add a **field** to `Backend`, to `Session`, or to any
> options struct. It may never change the signature of an existing field, and it
> may never introduce an exported interface.

The mechanical half is already gated — T18 extended `api_surface_test.go` to scan
`smtpserver/`, so `TestAPISurfaceContextFirst`, `TestAPISurfaceOptionsStruct`,
`TestAPISurfaceNoExportedInterfaces` and `TestAPISurfaceKeyedLiteralDocNote` all
apply. **The gate is not the review.** `API-STABILITY.md` §3's record of what
happens to a rule with no gate is why both exist.

Two things to check that no gate can:

- **v0.x is not a licence to defer thinking.** The version makes a mistake
  *fixable*; it does not make one *cheap*. Every third-party backend written
  against a v0.x API still breaks when it changes.
- **Nothing leaked upward.** `smtpserver` must not have required a change to
  `package smtp`. Demonstrate it with `apidiff` against the root module's v1 tag,
  not by assertion. If something did leak, that is a T16 finding arriving late,
  and after the tag it is a v2 — record it as such rather than quietly reshaping.

## Part 2 — documentation

Every exported symbol gets a doc comment naming its RFC, per the gate that has
been live since T12. Beyond the mechanical requirement, four things need prose
because a backend author gets them wrong otherwise, and each has a written source:

| Topic | Where it must be said | Source |
|---|---|---|
| the re-entrancy contract | `Session`'s doc comment | §4: handlers are never called concurrently for the same session, so per-session state needs no locking; `Backend` fields do |
| what `ctx` actually promises | `Session`'s doc comment, in these words | §4: a **per-command deadline** is the bound that holds; shutdown cancels immediately; **peer disconnect is best-effort** and a backend blocked without I/O learns of it when it returns |
| verification versus commit | `Authenticate`, `ChallengeResponse`, `SCRAMCredentials`, `CommitAuth` | §2d: verification handlers validate and must not commit; `SCRAMCredentials` returning keys is not an authentication event |
| `DATA` streams, `BDAT` does not | `Session.Data` and the spool options | §2a: under `DATA` backpressure reaches the peer through TCP; under `BDAT` the whole message is spooled first. Intrinsic to CHUNKING, not to this design |

Also document, because operators read this and not the design document: the spool
bounds are **server-instance-wide, not process-wide** (two listeners are two
budgets), and `smtpserver/memory` is **not durable and not for production**.

## Part 3 — examples

Runnable, CI-compiled, in the same style as the client's `examples/**`:

- a minimal SMTP sink using `smtpserver/memory`;
- a submission listener: TLS required, AUTH required, size limit — RFC 6409 as
  *policy*, which is what §1 says it is;
- an LMTP listener showing per-recipient `DataResult` construction, which is the
  one place a backend author must return N outcomes rather than one;
- a custom `Backend`/`Session` implementing the five required handlers and
  nothing else, to show the floor honestly;
- **an `smtpclient` test-double example** — the fourth reason `memory` ships as a
  supported package (§6) is that it is what an application's test suite needs
  instead of a container. If that claim is real, an example demonstrates it; if no
  example can, the claim was wrong and should be struck.

## Part 4 — the release

Two modules, two tags, and the coordination is the accepted cost of §9's
decision:

- `smtpserver/CHANGELOG.md`, Keep a Changelog, with **Exported API** labels — the
  same discipline the root module uses, because v0.x means breaks are permitted,
  not unannounced.
- Tag `smtpserver/v0.1.0` (Go's nested-module tag convention: the directory
  prefix). The root module's tag is independent and does **not** move for a server
  release.
- A deliberate bump of the nested module's dependency on the root module. Never a
  committed `replace` — `go.work` is the development mechanism (§9).
- Extend `.github/RELEASING.md` with the nested-module procedure: which tag,
  which order, and the fact that the root module's `apidiff` baseline is
  unaffected. That file is T13's; record the request in `.state/progress/T23.md`.
- CI must run both modules — tests, vet, staticcheck, gofmt, the
  zero-dependency check taught about the self-referential entry, and the fuzz
  discovery pool over both. `.github/**` is T13's; same recording rule.

## Part 5 — the documentation that lives outside `smtpserver`

- `docs/RFC-COVERAGE.md`: server-side status for every keyword the server
  implements. Sourced from the IANA registry, never from memory.
- `docs/ROADMAP.md`: M6's exit criteria are *the reference server joins the
  interop matrix as its own entry, real MTAs relay through it, and the server-side
  fuzz campaign is clean* — T22 satisfies them; this task points at the evidence.
- `docs/ARCHITECTURE.md`: the layering diagram gains `smtpserver` as a nested
  module. The dependency direction is unchanged and must be shown to be:
  `smtpserver` → `package smtp` → nothing.
- `README.md`: the server exists, it is v0.x, and the client is v1 frozen. A
  reader must not have to infer the version asymmetry.
- `API-STABILITY.md` already carries the §9 exception, approved 2026-08-04. Verify
  the shipped reality matches the approved text; if it diverged, the document is
  what needs the amendment, and it needs the human's approval, not an agent's.

## Done when

- `api-guardian` has **APPROVED** the full `smtpserver` surface, in writing, with
  its findings recorded.
- `apidiff` demonstrates the root module's v1 surface unchanged by the server's
  arrival.
- Every exported symbol has a doc comment naming its RFC; the four prose topics in
  Part 2 are present in the places named.
- Every example compiles in CI and runs against the matrix where applicable,
  including the `smtpclient` test-double example.
- Both modules are tagged, the CHANGELOG labels the exported surface, and
  `.github/RELEASING.md` documents the two-tag procedure.
- The four `docs/` files in Part 5 are updated, and `docs/ROADMAP.md`'s M6 exit
  criteria are satisfiable by pointing at recorded evidence rather than by
  assertion.
