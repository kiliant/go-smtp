# T24 — `ErrFinalStatusUnknown`: the lost-final-reply signal (D00)

**Agent:** client-core + api-guardian · **Milestone:** M7 ·
**Depends on:** T14 (approved design)

**Owns:** `smtpclient/finalstatus.go` (new), the final-completion paths in
`smtpclient/{data,ext_a_transport,ext_b_burl,lmtp}.go`, the `queuedCommand`
completion marker and its classification in `smtpclient/pipeline.go`, the
`Client` cancellation-contract doc comment in `smtpclient/client.go`, the
`DataResult` doc comment in `result.go`, the `Error.Err` doc comment in
`error.go`, and colocated tests. Appends cases to
`smtpclient/fakeserver_test.go` under its append-only rule. The `pipeline.go`,
`client.go` and `error.go` items were added after api-guardian's first review
(R1, R4, R5 and A1).

This is work package D00 of `docs/DELIVERY-DESIGN.md` §11. It is the one root
client change the delivery layer needs, and it is **additive**: one new exported
sentinel, no changed signature, no new error type.

## Why it comes first

RFC 5321 §4.2.5 transfers responsibility only when the client *receives* the
positive reply after end-of-data. If the terminator went out and the reply was
lost, the server may have accepted the message. Retrying it on another MX may
deliver it twice.

Today `Data`, the BDAT path and `BURL` return `*smtp.Error` for transport
failures. None of them says whether the failure happened before or after the
completing operation may have reached the peer. Without that signal, T29 has to
mark every failure after content transfer starts as `indeterminate`. That is
safe, but much broader than it needs to be. T29 depends on this task because
retry correctness is T29's central contract (design §11).

## Deliverables

### 1. The sentinel

```go
// in smtpclient/finalstatus.go
var ErrFinalStatusUnknown = errors.New("smtpclient: final status unknown ...")
```

The public error stays `*smtp.Error` (rule 5). The sentinel is reachable through
`smtp.Error.Err` with `errors.Is`. **The original cause must stay reachable as
well:** `errors.Is(err, context.Canceled)`, `errors.As(err, &netErr)` and every
existing `errors.Is` that works today must keep working on the same failure.
Use an unexported wrapper with `Unwrap() []error`, or `errors.Join`. Do not
replace the cause.

### 2. Where it applies — exactly these points

| Path | Carries the sentinel | Does **not** carry it |
|---|---|---|
| `Data` (`data.go`) | failure in `dw.Close()` (writing the terminator); context cancellation after `Close` returned; failure reading the final reply, including a malformed or missing reply code | failure writing ordinary content, reader errors, cancellation before `Close` |
| BDAT (`ext_a_transport.go`) | failure writing the `BDAT … LAST` frame (command line or chunk); failure reading its reply | any non-LAST chunk and its reply |
| `BURL` (`ext_b_burl.go`) | failure after sending a `BURL … LAST` command, until its reply is read | non-LAST `BURL` |
| LMTP `DATA` and LMTP BDAT LAST (`lmtp.go`, `ext_a_transport.go`) | stream failure after `k` of `n` per-recipient replies | — |

A **received** final reply, positive or negative, is authoritative and never
carries the sentinel. That also holds when the reply is a 4yz or 5yz code.

When in doubt, a write failure counts as "may have reached the peer". A buffered
writer cannot prove that zero bytes left the host.

**Context cancellation after the terminator is the subtle case.** Today it
returns a bare `ctx.Err()`. After this task it returns an `*smtp.Error` that
wraps both `ErrFinalStatusUnknown` and the context error. That is an error-*type*
change on one already-failing path. api-guardian must approve it explicitly, and
the commit body must say so. `errors.Is(err, context.Canceled)` must stay true.

### 3. LMTP partial results

If the per-recipient reply stream fails after `k` replies, `Data` (and the LMTP
BDAT LAST path) returns:

- the `k` authoritative `smtp.RecipientResult` values, as a prefix in RCPT order;
- **plus** an outer `*smtp.Error` wrapping `ErrFinalStatusUnknown`.

The missing suffix is indeterminate. Do not synthesise code-zero entries for it,
and do not drop statuses already received. Failure before the first reply
returns an empty result with the same classification. Whether "empty" means
`nil` or a zero-length slice is a choice; make it once, document it, and test
it.

This is the first time `Data` returns a non-empty result together with a
non-nil error. Update the `DataResult` doc comment (`result.go`) and the `Data`
and `BURL` doc comments to state the partial-result-on-error contract.

### 4. Extra LMTP final replies (decided, api-guardian approved)

`rejectExtraLMTPFinalReply` fails *after* all `n` replies arrived, because the
peer sent too many. **Decision:** `ErrFinalStatusUnknown` with a **nil**
result. Once the reply count is wrong, no reply can be matched to a recipient
with confidence: one extra reply at the front shifts every status. The
sentinel's doc reserves a later refinement. Loosening this later, from
"unknown" to "known", is safe. Tightening it later would turn a status callers
trusted into an unknown, which is the unsafe direction.

The same conservative result applies when the probe fails because the peer
closed the connection cleanly after the last expected reply. A later release
may return the full result in that case.

### 5. A session-level 421 inside an LMTP reply stream

A 421 (RFC 5321 §3.8) answers no recipient. Inside the per-recipient stream it
ends the stream early: the prefix before it is authoritative, and the error
keeps the 421 reply and also wraps the sentinel. In SMTP mode a 421 *is* the
single final reply and stays authoritative.

## Testing

All tests use scripted peers in `fakeserver_test.go` or `net.Pipe`. No network.

- For every "carries" cell in the table: inject the failure and assert
  `errors.Is(err, ErrFinalStatusUnknown)`, `errors.As(err, **smtp.Error)`, and
  that the original cause is still reachable.
- For every "does not carry" cell: assert the sentinel is **absent**. A guard
  that fires everywhere is as useless as one that fires nowhere.
- Short write of the terminator, short write of the `BDAT LAST` header, a
  connection closed before the final reply, a timeout on the final reply, a
  malformed final reply.
- LMTP: a failure after 0, 1 and `n-1` of `n` replies. Assert the exact prefix
  and the sentinel.
- **Mutation checks**, recorded in `.state/progress/T24.md`: remove each wrapping
  site and show a test failing. Do the same for the LMTP prefix and the absence
  assertions.

## Done when

- The tests above pass under `-race`. `go vet`, staticcheck and gofmt are clean.
- `apidiff` against `v1.1.0` reports only the additive sentinel.
- api-guardian has approved the sentinel, the post-terminator cancellation
  change, and the partial-result contract.
- `CHANGELOG.md` `[Unreleased]` has an entry (append-only; T31 owns the release
  section).

T24 is independently releasable as a root minor version. Whether it ships alone
or together with T31 is a release decision for the human, not for this task.
