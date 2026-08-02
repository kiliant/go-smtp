# T06 — Interop harness

**Agent:** `interop-harness` · **Milestone:** M1 · **Depends on:** T03

**Owns:** `interop/**`

Start as soon as T03 lands, in parallel with T04 and T05. A matrix that arrives
after the code it was meant to validate has no value.

## Goal

Run real SMTP servers under podman and prove the client against them. Read
`docs/INTEROP.md` first — it holds the matrix, the probed architectures, and the
account/fixture contract.

## Deliverables

### The sink abstraction — the thing that makes this different from an IMAP harness

The client **sends**, so the harness must read back what each server received.
Every server profile provides a sink, implemented however that server allows: an
HTTP API (Mailpit, GreenMail) or `podman exec` reading a maildir (Postfix, Exim,
Dovecot, maddy). One interface over all of them.

Assertions compare bytes submitted against bytes retrieved, modulo the trace
headers a server is required to prepend. This is the only way to prove the
transparency layer: a body line of exactly `.` round-trips happily through a unit
test and is silently truncated by a real server.

### Harness lifecycle

- Container names embed **process ID, timestamp and a per-process counter**. In
  the sibling repo two packages starting the same profile within one wall-clock
  second produced identical names and `podman run` failed outright. Build this in
  now.
- `GO_SMTP_INTEROP_SERVERS` restricts a run to a comma-separated subset.
- Build tags: `interop` for the native matrix, `interop_emulated` for tier 3.
- Health-gate startup on a real EHLO, not a sleep.

### Server profiles (`interop/servers/<name>/`)

`Containerfile` or pinned image, config provisioning
`interop@example.test` / `interop-pw` and the `example.test` local domain, plus
`profile.go` declaring the expected EHLO keywords and which ports serve what
(25, 465 implicit TLS, 587 submission, 24 LMTP).

Enforce both directions, and keep them distinct:

- test needs an extension the server lacks → **skip**
- server fails to advertise what its profile claims → **fail**

A permanently red matrix is a matrix nobody reads; a silently all-skipping one is
worse.

### Two matrix rows that are not yet facts

`docs/INTEROP.md` records these honestly and this task resolves them:

- **Exim** has no usable published multi-arch image; the setup probe found
  nothing at `docker.io/exim/exim4`. Build from Debian packages in a local
  `Containerfile`, as the sibling repo does for Cyrus.
- **Apache James** returned no manifest list, so its architecture is unconfirmed
  here. Re-probe with `podman manifest inspect` before treating the amd64-only
  claim as fact.

Do not record an architecture you have not run that command against.

### Fixtures (`interop/harness/fixtures.go`)

The table in `docs/INTEROP.md`. Each fixture targets a specific bug class rather
than the happy path — the `.`-only line, the 1000/1001-octet lines, content not
ending in CRLF, 8-bit and UTF-8 bodies, binary content with an embedded NUL, the
200 MiB streaming case, and the multi-recipient partial-failure case.

Verify the SASLprep account password bytes with `xxd` after editing. An editor
silently normalising that source file makes the test assert nothing while still
passing.

## Escalation

A server reply the parser rejects: save the bytes to
`internal/smtpwire/testdata/`, record it for T01, do not patch the parser — you
do not own it.

## Done when

Tier 1 servers start, seed and pass a smoke transaction under
`go test -count=1 -race -tags=interop ./interop/...`; profile assertion fails
loudly on a downgraded server; the sink interface works for both API-backed and
maildir-backed servers; Exim builds arm64-native; James's architecture is
confirmed or the row is corrected.
