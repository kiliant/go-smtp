# Interoperability testing

Correctness against the RFC is necessary but not sufficient — real servers
disagree, and the disagreements are where client bugs live. Every extension is
verified against at least two independent implementations before its coverage
status becomes `verified`.

## What is different about testing a *sending* client

The sibling IMAP harness seeds mailbox state and asserts on what the client
reads. Here the client **writes**, so the harness needs the opposite: a way to
read back what each server actually received. That asymmetry drives the design.

Every server profile provides a **sink** — a way to retrieve delivered messages —
implemented however that server allows: an HTTP API, IMAP, a server management
CLI, or `podman exec` reading a maildir. The harness exposes one interface over
them.

This matters more than it sounds. The transparency layer (dot-stuffing, RFC 5321
§4.5.2) is only genuinely provable by byte-comparing what arrived against what
was sent. A message whose body contains a line consisting of a single `.`, or one
that does not end in CRLF, will round-trip through a unit test happily and be
silently truncated by a real server. Those fixtures exist for that reason.

## Runtime

`podman` (the dev host has no Docker). The harness shells out to the CLI rather
than using a container SDK, to keep the dependency count at zero.

```bash
podman machine start                       # once
go test -count=1 -race -tags=interop ./smtpclient
go test -count=1 -race -tags=interop ./interop/...
```

Set `GO_SMTP_INTEROP_LARGE=1` when running `./smtpclient` to include the
200 MiB real-server streaming fixture. The normal test suite always runs the
flat-allocation streaming regression without requiring a container transfer.

The first command names `./smtpclient` explicitly so interop-tagged
production-client tests run. The commands must remain separate and sequential:
Go builds and runs one test process per package, and every process with a
`TestMain` using the harness owns an independent container lifecycle. Combining
the package lists starts those lifecycles concurrently, which means several
copies of every server image competing for the same host resources.

Container names must embed the process ID as well as a timestamp and a
per-process counter. This is load-bearing, not decorative: in the sibling repo,
two packages starting the same profile within the same wall-clock second
generated identical names and `podman run` failed outright. Build it in from the
start rather than rediscovering it.

Restrict a local run to a comma-separated subset when iterating:

```bash
export GO_SMTP_INTEROP_SERVERS=postfix,mailpit
go test -count=1 -race -tags=interop ./smtpclient
```

## Server matrix

Architectures probed on darwin/arm64, 2026-08-02, with
`podman manifest inspect`.

| Server | Image | Arch | Tier | Why it is in the matrix |
|---|---|---|---|---|
| Postfix | `docker.io/boky/postfix` (digest-pinned) | arm64 native ✓ | 1 | The most deployed MTA. The ESMTP baseline — if it disagrees with us, we are wrong |
| Stalwart | `docker.io/stalwartlabs/mail-server:v0.11.8` | arm64 native ✓ | 1 | Modern, aggressive coverage: SMTPUTF8, REQUIRETLS, CHUNKING |
| Mailpit | `docker.io/axllent/mailpit` (digest-pinned) | arm64 native ✓ | 1 | Deliberately minimal sink — catches assumptions about optional extensions. HTTP API makes it the easiest sink to assert against |
| Dovecot LMTP | `docker.io/dovecot/dovecot:2.4.3` | arm64 native ✓ | 1 | The LMTP reference (RFC 2033). The only way to exercise per-recipient DATA replies against a real implementation |
| maddy | `docker.io/foxcpp/maddy` (digest-pinned) | arm64 native ✓ | 2 | Independent Go implementation, different bug class from the C servers |
| Exim | local build: `interop/servers/exim/Containerfile` | arm64 native (by build) | 2 | Second-most deployed; quirky, the compatibility canary |
| GreenMail | `docker.io/greenmail/standalone:2.1.9` | arm64 native ✓ | 2 | JVM implementation; minimal extension set |
| Apache James | `docker.io/apache/james:demo-3.8.2` | **amd64 only** | 3 | JVM MTA, different bug class again |

Two entries carry caveats rather than probe results, and neither may be quietly
promoted to a bare assertion:

- **Exim** publishes no usable multi-arch image under a maintained name; the
  probe found nothing at `docker.io/exim/exim4`. It is built from Debian packages
  in a local `Containerfile`, which is both arm64-native and reproducible. This
  mirrors how the sibling repo handles Cyrus.
- **Apache James** returned no manifest list, so its architecture was not
  confirmed here. It is listed amd64-only on the strength of the sibling repo's
  2026-07-31 probe. [T06](tasks/T06-interop-harness.md) must re-confirm before
  the row is treated as fact.

Tiers 1 and 2 run by default. Tier 3 requires emulation and is opt-in:

```bash
go test -count=1 -race -tags='interop interop_emulated' ./smtpclient
go test -count=1 -race -tags='interop interop_emulated' ./interop/...
```

## Extension profiles and skipping

Each server declares a profile in `interop/servers/<name>/profile.go`: the EHLO
keywords it is *expected* to advertise, and which port speaks what (25, 465
implicit TLS, 587 submission, 24 LMTP). The harness enforces two different
things, and the distinction matters:

- A test needing an extension the server does not advertise → **skip**.
  This is normal. Mailpit has no `DELIVERBY` and never will.
- A server not advertising an extension its profile *claims* → **fail**.
  This catches a broken container or a misconfiguration, which would otherwise
  silently turn the whole suite into skips and look green.

A permanently red matrix is a matrix nobody reads, so the default is to skip. A
silently-all-skipping matrix is worse, so profiles are asserted.

## Fixtures

Fixtures here are **messages to send**, chosen so that each one fails loudly
against a specific bug class rather than merely exercising the happy path:

| Fixture | What it catches |
|---|---|
| Plain 7-bit ASCII, CRLF throughout | baseline |
| Body containing a line that is exactly `.` | dot-stuffing on send |
| Body containing a line beginning `..` | un-stuffing symmetry |
| Content not ending in CRLF | the terminator boundary case |
| A line of exactly 1000 octets, and one of 1001 | RFC 5321 §4.5.3.1.6 text-line limit |
| 8-bit UTF-8 body | `8BITMIME` vs downgrade behaviour |
| UTF-8 RCPT local-part and domain after SMTPUTF8 MAIL | RFC 6531 MAIL/RCPT coupling |
| Binary content with embedded NUL | `BINARYMIME` + `CHUNKING`, which is the only legal way to send it |
| 200 MiB message | the streaming guarantee; peak allocation must stay flat |
| Multi-recipient, one invalid | per-recipient results, and the LMTP N-replies path |

Fixtures live in `interop/harness/fixtures.go`. Assertions compare the bytes the
sink returns against the bytes submitted, modulo the trace headers a server is
required to prepend.

## Accounts and provisioning

Every server provisions the same account and accepts the same local domain, so
assertions are shared:

| Item | Value |
|---|---|
| Account | `interop@example.test` / `interop-pw` |
| Local (accepted) domain | `example.test` |
| Rejected recipient, for negative tests | `nobody@example.invalid` |

Optionally provision the two SASLprep diagnostic accounts, which store the same
password in the two forms that discriminate Unicode normalisation:

| Account | Stored password | Bytes |
|---|---|---|
| `interop-prep@example.test` | `interop-pw-µ` (U+00B5 MICRO SIGN) | `…c2 b5` |
| `interop-prep-nfkc@example.test` | `interop-pw-μ` (U+03BC GREEK SMALL MU) | `…ce bc` |

Tests covering them skip cleanly on a server that lacks them, so they are not a
prerequisite for adding a server. Verify the bytes with `xxd` after editing —
this is the one fixture in the tree where an editor silently normalising a source
file would make the test assert nothing while still passing.

## Adding a server

1. `interop/servers/<name>/Containerfile` (or a pinned image reference) plus
   config provisioning the account and domain above.
2. `profile.go` with the expected EHLO keyword list and the ports it serves.
3. A sink implementation — HTTP API or maildir read — registered with the
   harness.
4. Register in `interop/harness/registry.go`.
5. Confirm the arch: `podman manifest inspect <image> | grep architecture`.
   If amd64-only, mark it Tier 3. **Do not record an architecture you have not
   run that command against.**

## Testing our own server — planned, milestone M6

No `smtpserver` code exists yet; this section is the runbook the server work
(T22) implements against, recorded now so `docs/SERVER-DESIGN.md` §7 is
committed to something concrete rather than a good intention.

The client's rule — a capability is `verified` only when exercised against at
least two independent implementations — has an obvious problem in the other
direction: our client testing our server is one implementation talking to
itself. A shared misreading of an RFC passes both sides and neither notices. So
loopback is the inner loop, and the validation is external.

### 1. Loopback — fast, hermetic, not validation

Our client against our server over `net.Pipe`, in-process, no containers. Runs in
the default `go test ./...` because it needs no runtime. Catches regressions;
proves nothing about RFC conformance.

### 2. The matrix, pointed at ourselves

`smtpserver` plus the in-memory backend becomes an entry in
`interop/servers/gosmtp/`, exactly like Postfix and Stalwart: a `profile.go`
declaring expected EHLO keywords and ports, registered in
`interop/harness/registry.go`.

Everything in this document then applies unchanged — same fixtures, same
skip/assert distinction, same per-capability table. The result is our server's
coverage reported in the same units as Postfix's, which is the comparison that
means something.

It differs from every other entry in one way worth noting: the profile assertion
("a server not advertising a keyword its profile claims is a failure") becomes a
real regression test rather than a container-health check, because we control
both halves. That is a feature — it is the one entry where the assertion catches
our own bug.

Being in-process rather than containerised, it needs no image and no podman, so
**the harness must stop assuming every profile has a container.** That is a real
change to `interop/harness/`, not a configuration tweak, and T22 budgets for it.

### 3. Real MTAs sending *to* us — the external check that matters

This is the mirror of the client matrix and it is nearly free, because the
containers already exist:

| Sender | Configuration | Exercises |
|---|---|---|
| Postfix | `relayhost` pointing at our listener | ESMTP relay, pipelining, TLS, large messages |
| Postfix | `lmtp:` transport | **LMTP server mode**, the natural counterpart to Dovecot's LMTP server already in the matrix |
| Exim | `smtp` transport with a route to us | a second independent ESMTP implementation |
| `swaks` | scripted | the de-facto SMTP testing tool; edge cases, malformed input, `BDAT` |
| `msmtp`, Python `smtplib` | submission | the RFC 6409 profile with AUTH and STARTTLS |

Honest note: **there is no `imaptest` equivalent for SMTP.** The sibling
repository's single highest-value external conformance tool has no counterpart
here, and pretending otherwise would set a false expectation. The compensation is
that real MTAs are trivially available as senders, which is not true of IMAP
clients.

### 4. Server-side fuzzing and stateful security tests

The mirror of T11, non-optional, and a larger exposure: the command parser, the
path parser and the `BDAT` framer face **unauthenticated remote clients**.

Parser fuzzing reaches none of the stateful cases, and each of these is a known
vulnerability class. They are listed in full in `docs/SERVER-DESIGN.md` §7; the
two that must never regress are **SMTP smuggling** (published vectors asserted to
terminate nothing) and **STARTTLS plaintext injection** (CVE-2011-0411: bytes
buffered after the `STARTTLS` command discarded, session state reset).
