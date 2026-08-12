# T22 — Server conformance, interop, fuzzing, security tests

**Agent:** fuzz-hardening + interop-harness · **Milestone:** M6 ·
**Depends on:** T20

**Owns:** `interop/servers/gosmtp/**`, `smtpserver/**/*_fuzz_test.go`, and the
server-side security suites.

**Implementation waits for the v1.0 tag.**

Loopback — our client against our server over `net.Pipe` — is the inner loop and
**not** validation. It is fast and hermetic and it catches regressions, and a
shared misreading of an RFC passes both sides silently. That is precisely the
failure the client's interop matrix exists to catch, and the server needs the same
answer.

## Part 1 — point the existing matrix at ourselves

T06 already starts seven servers under podman, provisions accounts, drives real
transactions and reads messages back through sinks. Adding `smtpserver/memory` as
an `interop/servers/gosmtp` entry reuses all of it and reports our coverage **in
the same units as Postfix's** — which is the comparison that means something.

One real cost, called out in the design so it is budgeted rather than discovered:
being in-process, this entry needs no image and no podman, so **the harness must
stop assuming every profile has a container.** That is a change to T06's code, in
`interop/harness/**`, not just a new profile directory. Record the boundary
crossing in `.state/progress/T22.md`.

This is also the one matrix entry where the harness's standing rule — *a server
that fails to advertise a keyword its profile claims is a failure, not a skip* —
catches **our** bug rather than a broken container. Do not weaken it here.

## Part 2 — real MTAs sending to us

The mirror of the client matrix, and nearly free because the containers already
exist:

| Sender | Role |
|---|---|
| Postfix with `relayhost` pointed at our listener | the reference relay client |
| Exim likewise | a second independent implementation |
| `swaks` | the de-facto SMTP testing tool, for scripted edge cases |
| `msmtp`, Python `smtplib` | submission-profile clients |
| **Postfix's `lmtp:` transport** | a real LMTP *client*, the natural counterpart to Dovecot's LMTP server already in the matrix, and the only way to validate LMTP server mode against something that is not us |

**Honest note, from the design:** there is no `imaptest` equivalent for SMTP. The
sibling repository's single highest-value external check has no counterpart here,
and pretending otherwise would set a false expectation. The compensation is that
real MTAs are trivially available as senders, which is not true of IMAP clients.

Interop tests **skip** on absent capabilities and **fail** on a profile that
claims a capability the server does not advertise. Same rule as the client
matrix, same reason: a permanently red matrix is a matrix nobody reads, and an
all-skip matrix looks green while testing nothing.

## Part 3 — server-side fuzzing

The mirror of T11, non-optional. These parsers face **unauthenticated remote
clients** — a larger and more exposed surface than the client's hostile-server
case, on the most-scanned port on the internet.

Targets, at minimum: the command-line decoder, the path parser, the `BDAT`
framer, the transparency reader through the server's `DATA` path, the receive-side
`MAIL`/`RCPT` parameter parsers (one per extension family), the SASL responder
half, and the advertisement formatter.

Bar unchanged: **no panic, no hang, no unbounded allocation.** Corpus seeded from
real client traffic captured against the matrix and from the published
SMTP-smuggling vectors — **not from invention**, which is the rule that took the
client's discovered-target count from 17 to 21 when a coverage audit found four
parsers with no target at all.

Discovery is by enumeration, not by memory: the campaign runs over every `Fuzz*`
target found in both modules, and a target that exists but is not discovered is a
harness bug.

## Part 4 — stateful security tests

Parser fuzzing reaches none of these, and each is a known vulnerability class.
§7's list is the checklist:

- **SMTP smuggling.** The published vectors — `<LF>.<LF>`, `<CR>.<CR><LF>`,
  `<CR><LF>.<CR>` and the rest — asserted to terminate nothing and to be
  rejected. `DotUnstuffReader` is the implementation; this is the test that it is
  actually **reachable** through the server's `DATA` path.
- **STARTTLS plaintext injection (CVE-2011-0411):** bytes buffered after the
  `STARTTLS` command are discarded, the violation is logged, and the session state
  is reset. Assert all three; a test that only checks the commands did not execute
  passes against an implementation that silently swallows the attempt.
- **Pipelining desynchronisation:** a group whose last member is `DATA` followed
  immediately by content; a group spanning `RSET`; a group spanning a rejected
  `RCPT`.
- **`BDAT` size handling — four cases**, and note that revision 1's "larger and
  smaller than the bytes that follow" was wrong about the second half, because
  bytes after an exact chunk are legally the next pipelined command:

  | Case | Required behaviour |
  |---|---|
  | announced size exceeds the bytes that arrive | block until the data deadline, then fail. A short read is never end-of-chunk |
  | bytes after exactly `n` octets form a valid next command | **legal.** Proceed |
  | bytes after exactly `n` octets do not form a valid command | `500`, then the failed-BDAT state |
  | a failed chunk followed by further pipelined chunks | consume, discard, **and answer `503`** — a swallowed chunk with no reply desynchronises a peer counting replies against commands |

  Plus `BDAT 0 LAST` on an empty transaction, `BDAT` before `MAIL`, `BDAT` after
  `DATA`, and an announced size that overflows the parse.
- **Spool exhaustion (§2a):** crossing `MaxSpoolBytes` mid-chunk gets `552 5.3.4`
  *after* the full announced chunk is consumed, not a disconnect; a write failure
  gets `451 4.3.0`; aggregate exhaustion gets `452 4.3.1`; and **the spool file is
  gone from the filesystem** in every case including after a panic.
- Disconnect mid-`DATA`, mid-`BDAT`, and after `354` with no content.
- Slow-loris: one byte per minute against every read deadline.
- Repeated failed authentication; `AUTH` after `AUTH`; `AUTH` mid-transaction.
- Resource exhaustion: maximum recipients, maximum transactions per connection,
  connection floods from one source.
- **Goroutine and file-descriptor leak checks across all of the above.** One
  goroutine per connection makes a leak easy to assert and unforgivable to ship.

## Part 5 — the mutation discipline

Every suite here must be shown to be load-bearing. The client side established
this the hard way twice: a fuzz gate whose self-test used a shape the real code
could not produce, and an LMTP test that passed against a mutated implementation.

For each security case above, break the implementation deliberately and record
that the test failed. A security suite that has never failed is not known to test
anything, and the record belongs in `.state/progress/T22.md`.

## Done when

- `interop/servers/gosmtp` is in the default matrix, green, and the harness runs
  a container-less profile without special-casing at every call site.
- Postfix and Exim both relay a message through our listener and the delivered
  content is byte-identical; Postfix's `lmtp:` transport drives LMTP mode and gets
  N per-recipient replies in issue order.
- A full discovered-target fuzz campaign over both modules is clean at the
  project's standard duration (10 minutes per target — `.github/RELEASING.md` and
  `.github/workflows/fuzz-long.yml` are the operative figures).
- Every case in Part 4 has a test, and every one has a recorded mutation check.
- No goroutine or file-descriptor leaks under any suite.
- `go test ./...` without interop tags stays fast in both modules.
