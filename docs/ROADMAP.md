# Roadmap

Target: **v1.0 with a frozen public API**, not a permanent beta. The exit
criteria below are objective — a milestone is done when they pass, not when it
feels finished.

Task IDs refer to `docs/tasks/BOARD.md`.

## M0 — Foundation (T01, T02)

The wire codec and the core vocabulary. No network/session layer, no client.

This milestone is foundational and blocks everything else: it fixes the types
that appear in every later signature, and those are exactly the types that are
expensive to change later. T01 and T02 can proceed in parallel because their
ownership boundaries do not overlap.

Two shape decisions must be right here or never:

- the **per-recipient result** collection (`API-STABILITY.md` §8), because LMTP
  returns N replies where SMTP returns one;
- the **three open sets** — extension keywords, esmtp-params, enhanced status
  codes — each with the remedy matching its direction of flow
  (`API-STABILITY.md` §1).

**Exit:** fuzz targets run clean for 5 minutes; `package smtp` imports nothing
from the module; the open sets and the per-recipient shape are reviewed and
signed off by `api-guardian`; `api_surface_test.go` exists and its gates pass.

That last clause is deliberate. The sibling repo shipped the options-struct rule
as prose and found 28 violations at its freeze audit. The gate exists from M0
here.

## M1 — A client that works (T03, T04, T05, T06)

Connection, EHLO/HELO negotiation, STARTTLS and implicit TLS, SASL, the mail
transaction, and the interoperability harness.

**Exit:** the Postfix interop suite passes; a real message can be submitted
through a 587 submission service with AUTH and through a 25 relay; the
dot-stuffing fixtures round-trip byte-identically.

## M2 — LMTP and the extensions that matter (T07, T08, T09)

LMTP, then extension groups A (transport core) and B (delivery control).

**Exit:** the M2 acceptance matrix (Postfix, Stalwart, Mailpit, Dovecot LMTP) is
green; per-recipient results are verified against a real LMTP server; `CHUNKING`
and `DATA` are verified to produce byte-identical delivered content.

## M3 — Full coverage (T10)

Extension group C — legacy and niche. Tier-2 servers (maddy, Exim, GreenMail)
join the matrix.

**Exit:** `docs/RFC-COVERAGE.md` has no `planned` rows outside the explicitly
`deferred` set.

## M4 — Hardening and the freeze (T11, T12, T13, T16)

Fuzzing corpus, API surface review, documentation and examples, release
engineering — and the bidirectional vocabulary audit.

**Exit:** `apidiff` gate active in CI; API surface test passes; every exported
symbol has a doc comment; examples compile and run against the matrix; a full
fuzz campaign over every **discovered** target is clean at **10 minutes per
target** (human-approved 2026-08-03; `.github/workflows/fuzz-long.yml` and
`.github/RELEASING.md` are the operative figures, and this line previously said
30 minutes, which was never the duration anything ran); **`package smtp` has been
reviewed from the server direction (T16) and every finding has a recorded
verdict, executed or explicitly declined.**

Per *discovered* target, not per known target, is the load-bearing half of that
sentence: a coverage audit on 2026-08-03 found four parsers and invariants with no
fuzz target at all, taking discovery from 17 to 21, which retroactively voided a
campaign that had reported 17/17 green.

That last criterion is new, and it is not a formality. Adding a type to
`package smtp` after the tag is additive and always allowed; **reshaping one is
not**, and a vocabulary exercised only in the client direction can hold a type a
server can consume but cannot naturally produce. `docs/SERVER-DESIGN.md` §0 ran
that review and found three concrete defects — the RFC 9422 `LIMITS` types are in
`smtpclient` where a server cannot reach them, `AllowUnadvertisedParameters` is
meaningless in the receive direction, and the trace vocabulary is in a
direction-specific package. Each is free to fix now and a v2 otherwise.

## M5 — Design before the freeze (T15)

**T15 — server framework design.** `docs/SERVER-DESIGN.md`. Design only; no
`smtpserver` code. It runs *before* the tag rather than after, because the design
is what tells us the freeze is safe — see the M4 exit criterion above.

**Exit:** the document is approved by the human, in writing; the versioning
question in its §9 is decided; and T18–T23 have specs written against the
approved abstraction.

Two open questions carried by that document, both needing a human decision:

- whether `smtpserver` ships as a **nested v0.x module** rather than inheriting
  the root module's v1 freeze on its first commit (§9);
- whether one options struct serves both directions for `MAIL`/`RCPT`
  parameters, or the receive side gets its own types (§0) — this one has a
  deadline, because it is T16's and T16 is M4.

## v1.0 — API freeze

After this tag, additive changes only. Removals require two minor releases of
deprecation and do not land before v2.

## M6 — After the freeze (T14, T17–T23)

Two separate packages, each design-document first:

- **T14 — `smtpdeliver`.** MX selection, multi-address attempt sequencing,
  MTA-STS, optional DANE via a caller-supplied DNSSEC-aware resolver, structured
  retryable outcomes. Deliberately after v1.0: the three reservations in
  `API-STABILITY.md` §9 already make it additive, so there is no cost to waiting
  and a real cost to designing it against no caller.
- **T17–T23 — `smtpserver`.** The server framework implementation. T17 (the
  server-direction codec, plus the two subsystems that exist in neither
  direction: path parsing and `Received:` generation) is the bulk of the work and
  does not depend on the backend abstraction.

**Exit:** the reference server joins the interop matrix as its own entry, real
MTAs relay through it, and the server-side fuzz campaign is clean.

**M6 evidence (T22/T23, 2026-08-21):**

- `interop/servers/gosmtp/profile.go` registers the container-less `gosmtp`
  target and pins the server capability profile used by the default matrix.
- `interop/servers/gosmtp/external_sender_interop_test.go` relays distinct
  messages through the reference server from Postfix SMTP, Postfix LMTP, and
  Exim SMTP; the LMTP case verifies per-recipient final replies by requiring the
  real Postfix queue to drain.
- `smtpserver/**/*_fuzz_test.go` and the discovery-based
  `.github/scripts/fuzz.sh` cover the server boundary. Hosted run
  [31793174758](https://github.com/kiliant/go-smtp/actions/runs/31793174758)
  discovered and passed all 36 root-plus-server targets at ten minutes each.

The M6 exit criterion is complete. `smtpserver/v0.1.0` is the first nested-module
release; the independently versioned client remains under its v1 compatibility
promise.

Neither may pull scope forward into the client. Durable queues, retry
scheduling, bounce generation, mailbox storage, spam filtering and full MTA
behaviour are out of scope at every milestone.

## Sequencing

```
T01 ──┬── T03 ──┬── T04 ──────────────┐
      │         ├── T05 ──┬── T07 ────┤
T02 ──┘         │         ├── T08 ──┬─┴── T10 ──┐
                │         └── T09 ──┘           ├── T12 ── T13 ── v1.0
                └── T06 ────────────── T11 ─────┘
```

T01 and T02 run in parallel and both must land before anything else starts.

T06 (interop harness) has no dependency on the client beyond T03 and should start
early and in parallel — a matrix that arrives after the code it was meant to
validate has no value.

T08 and T09 are the genuinely parallel phase: two extension agents, one owning
file prefix each, no shared files.

The server work hangs off the same graph:

```
T15 (design, human-led) ──┬── T16 (audit) ── joins the v1.0 critical path
                          └── T17 (codec) ── T18 … T23, all post-tag
```

T15 is design-only and runs in parallel with M2–M4. T16 is the only server-scoped
task inside the freeze.
