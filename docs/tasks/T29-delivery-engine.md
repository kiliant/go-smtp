# T29 — Attempt engine, message replay, outcome classification (D05)

**Agent:** client-core + api-guardian · **Milestone:** M7 ·
**Depends on:** T24, T26, T28

**Owns:** `smtpdeliver/attempt_*.go`, `smtpdeliver/outcome_*.go`, the `Deliver`
implementation, and colocated tests. Scripted SMTP and LMTP peers for this
package live in `smtpdeliver/fakepeer_test.go`. T29 owns its structure. T30
appends cases.

This is work package D05 of `docs/DELIVERY-DESIGN.md` §11, and the reason the
package exists. Governing text: design §7 (transaction ownership), §8 (outcomes
and retry safety) and §9 (limits and cancellation).

## Why it waits for T24

The engine's central promise is **never to duplicate a message by retrying after
a possibly accepted final reply**. Without `smtpclient.ErrFinalStatusUnknown`,
every content-phase failure would have to be `indeterminate`. With it, the
engine can tell "lost after the terminator" from "failed before acceptance was
possible". Building the engine first and refining it later would mean writing
the hardest classification twice.

## Deliverables

### 1. One destination attempt (design §7)

1. Dial through the caller's hook with T26's `DialRequest`. Hand the `net.Conn`
   to `smtpclient.NewClient` with the MX hostname as `TLSServerName` and T28's
   per-attempt TLS config.
2. Run `MAIL`. Send `RCPT` for every still-pending recipient via `RcptBatch`.
3. A permanent RCPT failure is final for that recipient. A transient one stays
   pending for the next candidate.
4. If at least one recipient was accepted, call `MessageSource.Open` **again**
   for this attempt and send **one** copy for all of them.
5. Apply the final reply. Advance only recipients that are still *safely*
   pending.
6. `QUIT` and close. The package owns the connection from dial to close.

Destinations run sequentially. There is no pooling and no reuse across `Deliver`
calls or Policy Domains (design §7).

### 2. Classification (design §8)

| Event | Disposition of affected recipients |
|---|---|
| authoritative 2yz final reply | `delivered`, final, never retried |
| permanent `MAIL` reply | `permanent-failure` for all pending |
| permanent RCPT reply | `permanent-failure` for that recipient |
| permanent final DATA, BDAT or BURL reply | `permanent-failure` for every recipient accepted in that transaction |
| connection, greeting, DNS, TLS-policy, any 4yz | candidate failure; advance; `temporary-failure` on exhaustion |
| MTA-STS `enforce` exhaustion | `temporary-failure`, never permanent (RFC 8461 §5) |
| `errors.Is(err, smtpclient.ErrFinalStatusUnknown)` | `indeterminate` for the affected recipients; **stop** advancing them |
| LMTP partial prefix | prefix authoritative; suffix `indeterminate` |
| context cancelled or `Open` failed before content | `not-attempted` for untouched recipients, partial `Result` **plus** the call error |

Every valid input recipient appears exactly once, in input order, duplicates
included. `RecipientOutcome.Attempt` indexes the attempt that made the outcome
final. Causes keep their original error chains. SMTP replies stay `*smtp.Error`.

A remote refusal returns a `Result` and a nil error. An invalid request returns
no result and an error. Cancellation and source failure return both.

### 3. Message replay

`Open` is called once per content-transfer attempt. The reader is closed on
every path. The package never buffers the message. If `Open` fails, the result
is `not-attempted` with the source error, not a remote failure.

### 4. Limits and observation (design §9)

DNS, connect and HTTPS bounds come from `Options.Timeouts`. SMTP command bounds
inherit `smtpclient`'s. `MaxDestinations` and the address cap are enforced. The
`Trace` callback fires synchronously and never sees message content,
credentials, policy bodies or private keys. A trace callback that calls back
into the same `Deliverer` is documented as forbidden. Test it if a guard is
cheap.

## Testing

Scripted peers over `net.Pipe`, routed through the dial hook and the fake
resolver.

- One test per classification row above.
- **Mixed RCPT across MX hosts:** MX1 accepts A and transiently rejects B. MX2
  accepts B. Assert that A is not re-sent and that `Open` ran exactly twice.
- **Lost final reply** for DATA, BDAT LAST and BURL LAST. Assert `indeterminate`
  and that **no second MX is contacted for those recipients**.
- **LMTP prefix:** a failure after `k` of `n` per-recipient replies.
- Cancellation at every stage: before dial, mid-RCPT, mid-content, after the
  terminator.
- `Open` returning different errors on the first and second calls.
- Goroutine and connection leak checks at the end of each test.
- **Mutation checks**, recorded in `.state/progress/T29.md`: retry after
  `ErrFinalStatusUnknown`, re-send to a delivered recipient, and drop a
  duplicate recipient. Each must turn a test red.

## Done when

The tests pass under `-race`. Every row has a test. The mutation records exist.
api-guardian has re-reviewed the result types now that they are populated: the
outcome hierarchy is the part of the surface callers' queues build on.
