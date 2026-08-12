# Task board — dependency order and file ownership

The implementation plan. Task specs are in this directory, one file per task.

This file is **committed**: it is documentation, and it is static — the
dependency graph and file ownership do not change as work proceeds. Mutable
status lives in `.state/status.md`, which is gitignored. See "Plan vs state"
below.

## The two rules that make parallel work safe

1. **Only edit files your task owns.** The ownership column is the lock. If your
   task needs a change to a file it does not own, record the request in
   `.state/progress/<task>.md` and stop. Do not edit across the boundary.
2. **Do not start a task whose dependencies are unfinished.** The order exists
   because these tasks change shared type signatures; starting early produces
   rework, not speed.

Focused unit or integration tests colocated with the production file or file
prefix they exercise inherit that ownership: T03 may add
`smtpclient/conn_test.go` or `smtpclient/conn_interop_test.go`. This does not
transfer ownership of fuzz tests or shared test infrastructure, which remain
explicitly assigned below.

Where a task owns a directory tree and another task owns a filename pattern
inside it, **the pattern wins**: T11's `**/*_fuzz_test.go` covers
`internal/saslprep/saslprep_fuzz_test.go` even though T04 owns
`internal/saslprep/**`.

The same precedence applies to a **nested directory**: T11 owns
`interop/harness/adversarial/**` even though T06 owns `interop/**`. The more
specific path wins, whether it is a pattern or a subtree.

## Shared test infrastructure

One file is used by five tasks, two of which run in parallel, so it needs an
owner named here rather than in whichever spec happens to mention it first.

| File | Owner | Rule |
|---|---|---|
| `smtpclient/fakeserver_test.go` | T03 | Shared, **append-only**. T05, T07, T08, T09 and T10 add scripted cases; T03 owns its structure. Nobody deletes another task's cases. |

This is the same arrangement as `internal/smtpwire/testdata/` (T01 owns the
layout, others append). It exists because T08 and T09 are designed to run
concurrently, and two agents extending an unowned file is exactly the collision
the ownership table prevents.

The adversarial server is **not** this file — that is T11's, lives in
`interop/harness/adversarial/**`, and is hostile by design rather than scripted.

Two files are **created by one task and owned by another afterwards**. In both
cases the originating task must land the file — a parser committed without a
fuzz target is not finished, and a rule documented without a gate is how the
sibling repo accumulated 28 violations — but subsequent changes belong to the
owner:

| File | Created by | Owned from |
|---|---|---|
| `**/*_fuzz_test.go` | whichever task introduces the parser | T11 |
| `api_surface_test.go` | T02 | T12 |

## Tasks

| ID | Task | Milestone | Depends on | Owns | Agent |
|---|---|---|---|---|---|
| [T01](T01-wire-codec.md) | Wire codec | M0 | — | `internal/smtpwire/**` | wire-protocol |
| [T02](T02-core-types.md) | Core types & errors | M0 | — | `*.go` (root pkg) | client-core + api-guardian |
| [T03](T03-connection.md) | Connection, EHLO & TLS | M1 | T01, T02 | `smtpclient/{client,conn,state,ehlo,starttls,pipeline,reply}.go`, `smtpclient/fakeserver_test.go` | client-core |
| [T04](T04-auth.md) | Authentication & SASL | M1 | T03 | `smtpclient/auth.go`, `internal/smtpsasl/**`, `internal/saslprep/**`, `internal/unicodenorm/**` | client-core |
| [T05](T05-mail-transaction.md) | Mail transaction | M1 | T03 | `smtpclient/{mail,rcpt,data,txn,verify}.go` | client-core |
| [T06](T06-interop-harness.md) | Interop harness | M1 | T03 | `interop/**` | interop-harness |
| [T07](T07-lmtp.md) | LMTP | M2 | T05 | `smtpclient/lmtp.go` | client-core |
| [T08](T08-ext-group-a.md) | Extensions group A — transport core | M2 | T05 | `smtpclient/ext_a_*.go` | extensions |
| [T09](T09-ext-group-b.md) | Extensions group B — delivery control | M2 | T05 | `smtpclient/ext_b_*.go` | extensions |
| [T10](T10-ext-group-c.md) | Extensions group C — legacy & niche | M3 | T08 | `smtpclient/ext_c_*.go` | extensions |
| [T11](T11-fuzzing-hardening.md) | Fuzzing & hardening | M4 | T01, T06 | `**/*_fuzz_test.go`, `internal/smtpwire/testdata/**`, `interop/harness/adversarial/**` | fuzz-hardening |
| [T12](T12-api-review-docs.md) | API review & docs | M4 | T04, T07, T09, T10 | doc comments, `examples/**`, `api_surface_test.go` | docs-release + api-guardian |
| [T13](T13-release-engineering.md) | Release engineering | M4 | T11, T12 | `.github/**`, `CHANGELOG.md` | docs-release |
| [T14](T14-delivery-design.md) | Delivery layer design | M5 | v1.0 tagged | `docs/DELIVERY-DESIGN.md` | — |
| [T15](T15-server-design.md) | Server framework design | M5 | — | `docs/SERVER-DESIGN.md` | — (human-led) |
| [T16](T16-bidirectional-vocabulary-audit.md) | Bidirectional vocabulary audit — **blocks v1.0** | M4 | T15 | `*.go` (root pkg), `smtpclient/{ext_b_limits,trace}.go` | client-core + api-guardian |
| [T17](T17-server-direction-codec.md) | Server-direction codec | M6 | T15 approved | `internal/smtpwire/**`, `internal/smtpsasl/**` | wire-protocol |
| [T18](T18-server-core.md) | Server core: loop, state machine, capabilities, TLS, **the §2a spool** | M6 | T17, §2 approved | `smtpserver/**` except the files T19 and T21 own by name, plus `smtpserver/go.mod` | server-core |
| [T19](T19-backend-contract.md) | Backend contract, `memory`, `backendtest` | M6 | T18 | `smtpserver/{backend,session}.go`, `smtpserver/{memory,backendtest}/**` | server-core |
| [T20](T20-base-commands.md) | Base command set and the extension floor | M6 | T19 | `smtpserver/cmd_*.go` and the floor-extension files | server-core |
| [T21](T21-server-extensions.md) | Server extensions beyond the floor, incl. `ATRN` | M6 | T20 | `smtpserver/ext_*.go` | server-core |
| [T22](T22-server-conformance.md) | Server conformance, interop, fuzzing, security tests | M6 | T20 | `interop/servers/gosmtp/**`, `smtpserver/**/*_fuzz_test.go` | fuzz-hardening + interop-harness |
| [T23](T23-server-release.md) | Server API review, docs, `smtpserver` release | M6 | T21, T22 | `smtpserver` docs, examples, release | docs-release + api-guardian |

**`docs/SERVER-DESIGN.md` is approved** (revision 4, 2026-08-04) and **every
task now has a spec**: T18–T23 were written against it on 2026-08-12, satisfying
`../ROADMAP.md`'s M5 exit criterion. **Implementation of `smtpserver/**` still
waits for the v1.0 tag** — a milestone condition, separate from design approval
and from the specs existing, unchanged by either.

Three of those specs share the `smtpserver/**` tree, so the precedence rules
above do the work: T19 owns `backend.go` and `session.go` by name and T21 owns the
`ext_*.go` prefix, both of which beat T18's subtree claim.

**T16 is the only server-scoped task with a deadline.** It is an M4 exit
criterion because it removes client-only asymmetries from `package smtp`, and
removing an exported field after v1.0 is not additive.

## Critical path

```
T01 ──┬── T03 ──┬── T04 ──────────────┐
      │         ├── T05 ──┬── T07 ────┤
T02 ──┘         │         ├── T08 ──┬─┴── T10 ──┐
                │         └── T09 ──┘           ├── T12 ── T13 ──┬── v1.0
                └── T06 ────────────── T11 ─────┘                │
                                                                 │
T15 (design) ──┬── T16 (audit) ──────────────────────────────────┘
               │
               └── T17 ── T18 ── T19 ── T20 ──┬── T21 ──┬── T23
                                              └── T22 ──┘
```

T01 and T02 may run in parallel. Both must complete before dependent work
begins — they fix the type signatures every later task consumes.

T15 is design-only and runs in parallel with everything, human-led. **T16 joins
the critical path to v1.0**: it removes client-only asymmetries from
`package smtp`, and removing an exported field after the freeze is not additive.
T17 onward are post-tag and gated on approval.

T06 should start as soon as T03 lands, in parallel with T04 and T05. A matrix
that arrives after the code it was meant to validate has no value.

T08 and T09 are the genuinely parallel phase: two extension agents, one owning
file prefix each, no shared files.

## Two decisions that cannot be deferred to the task that seems to own them

Both are recorded here because the task that *would* naturally own them runs too
late, and both are unfixable after the v1.0 freeze.

1. **The per-recipient result shape belongs to T01 and T05, not T07.** LMTP
   returns one reply per accepted recipient after `DATA`; SMTP returns one for
   the message. The result type is a per-recipient collection from the first
   commit, with SMTP as the single-element case. T07 adds `LHLO` and the command
   surface — it must not be the task that discovers the return type is wrong.
   See `../API-STABILITY.md` §8.
2. **The delivery-layer reservations belong to T03.** Connection injection, a
   dial address separate from the TLS server identity, and a dial hook. T14 is
   post-v1.0 and cannot add them retroactively. Equally: T03 must **not**
   speculatively design a TLS-policy interface, and must not put MX or policy
   vocabulary anywhere. See `../API-STABILITY.md` §9.

## Definition of done

A task is done when its tests pass, `api-guardian` has approved any exported
symbol it added, and its rows in `../RFC-COVERAGE.md` are updated.

## Escalation

| Situation | Do this |
|---|---|
| An extension seems to need a breaking change to a core type | Stop. Record it, flag `api-guardian`. Do not make the change. |
| A server reply the parser rejects | Save the bytes to `internal/smtpwire/testdata/`, note it for T01. |
| An RFC number in `../RFC-COVERAGE.md` looks wrong | Check the IANA registry, fix the doc. Never work from a recalled number — three numbers were already wrong in the source material this repo was built from. |
| Two servers disagree and both look RFC-compliant | Record both; the client accommodates both. Note it for the doc comment. |
| You want to add MX lookup, MTA-STS, DANE or a TLS-policy interface | Stop. That is T14, post-v1.0, and the scope is settled. See `../ARCHITECTURE.md`. |
| You want to write `smtpserver` code | Check `git tag`. The design is approved; the implementation still waits for v1.0. Specs and T16/T17 are unblocked. |
| A server-side need seems to require reshaping a type in `package smtp` | That is exactly T16's job, and T16 is M4. Record it there. After the tag it is a v2. |

## Plan vs state

| | Where | In git | Why |
|---|---|---|---|
| Task specs, dependency graph, ownership | `docs/tasks/` | yes | Documentation. A clone must be self-contained. |
| Current status, progress notes, scratch | `.state/` | no | Mutable coordination state, not project history. |

`.state/` contains a `.gitignore` holding `*` and `!.gitignore`: it ignores its
own contents, while the rule itself is tracked and therefore survives a fresh
clone. Nothing in the repo depends on `.state/` existing: delete it and the plan
is still complete and readable.
