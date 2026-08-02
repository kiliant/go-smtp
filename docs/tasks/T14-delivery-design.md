# T14 — Delivery layer design (`smtpdeliver`)

**Agent:** — · **Milestone:** M5 · **Depends on:** v1.0 tagged

**Owns:** `docs/DELIVERY-DESIGN.md`

**Design document first. No code until the document is approved by the human.**

## Why this is post-v1.0

Settled by the human on 2026-08-02 and recorded in `docs/ARCHITECTURE.md`. The
core client speaks to a caller-supplied endpoint; it never decides *which* host
to talk to. Every comparable library draws the line in the same place — Go's
`net/smtp` takes `host:port` and is frozen, `emersion/go-smtp` is
submission-oriented, and Nodemailer moved MX-based sending out of core into a
separate package.

Waiting costs nothing because `API-STABILITY.md` §9 already reserves what this
layer needs. Rushing costs a lot, because an abstraction designed against no
caller lands on a frozen surface.

## In scope

- MX resolution and address selection (RFC 5321 §5), including the implicit-MX
  fallback to A/AAAA and the null-MX convention (RFC 7505).
- Multi-address attempt sequencing with the preference ordering, and the
  distinction between an address that failed to connect and one that rejected the
  message.
- MTA-STS (RFC 8461) — DNS TXT policy discovery plus HTTPS policy fetch, both
  reachable from the standard library, plus policy caching.
- Optional DANE (RFC 7672) via a **caller-supplied DNSSEC-aware resolver
  interface**. The library does not validate DNSSEC itself.
- Structured retryable outcomes: enough information for a caller's queue to
  decide retry versus permanent failure, per destination and per recipient.

## Out of scope — permanently

Durable queues, retry scheduling, bounce message generation, full MTA behaviour.
This is a delivery *attempt* library, not a mail server. If the design starts
growing a scheduler, it has gone wrong.

Also out of scope: implementing DNSSEC validation in-tree. That constraint is
why DANE is a hook rather than a feature, and it is not up for renegotiation
without human approval — it is the zero-dependency rule.

## Questions the document must answer

1. What does the resolver interface look like, such that a caller can supply
   `miekg/dns` + a validating resolver **without this module depending on it**?
   This is the crux; get it wrong and either the zero-dependency rule breaks or
   DANE is unusable.
2. How do MTA-STS policy caching and its failure modes interact with the attempt
   sequence? A stale policy that blocks delivery is a worse outcome than no
   policy.
3. Does this package own connection reuse across recipients on one destination,
   or is that the caller's? It changes the shape of the entry point.
4. What is the outcome type, given that one call may produce per-destination and
   per-recipient results at two different levels?
5. Does the reservation in `API-STABILITY.md` §9 actually suffice? **If it does
   not, that is the single most valuable finding this task can produce** — say so
   loudly rather than working around it, because it means a v1.0 surface needs a
   v2 or a deprecation, and knowing early is worth a great deal.

## Done when

`docs/DELIVERY-DESIGN.md` answers all five, the human has approved it, and it has
its own task breakdown. Only then does implementation start.
