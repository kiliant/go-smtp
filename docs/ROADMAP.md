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

## M4 — Hardening and the freeze (T11, T12, T13)

Fuzzing corpus, API surface review, documentation and examples, release
engineering.

**Exit:** `apidiff` gate active in CI; API surface test passes; every exported
symbol has a doc comment; examples compile and run against the matrix; a full
30-minute fuzz campaign over every discovered target is clean.

## v1.0 — API freeze

After this tag, additive changes only. Removals require two minor releases of
deprecation and do not land before v2.

## M5 — After the freeze (T14, T15)

Two separate packages, each design-document first:

- **T14 — `smtpdeliver`.** MX selection, multi-address attempt sequencing,
  MTA-STS, optional DANE via a caller-supplied DNSSEC-aware resolver, structured
  retryable outcomes. Deliberately after v1.0: the three reservations in
  `API-STABILITY.md` §9 already make it additive, so there is no cost to waiting
  and a real cost to designing it against no caller.
- **T15 — server framework.** The shared `package smtp` vocabulary already makes
  this additive for the same reason.

Neither may pull scope forward into the client. Durable queues, retry
scheduling, bounce generation and full MTA behaviour are out of scope at every
milestone.

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
