# T11 — Fuzzing & hardening

**Agent:** `fuzz-hardening` · **Milestone:** M4 · **Depends on:** T01, T06

**Owns:** `**/*_fuzz_test.go`, `internal/smtpwire/testdata/**`,
`interop/harness/adversarial/**`

## Threat model

**The server is untrusted.** A client may connect to an attacker-controlled
server — or merely a buggy one. Neither may crash, hang, or exhaust the memory of
the process using this library.

For a *sending* client the stakes are specific: the process is handing over
message content, often credentials, and often on behalf of other users.

## Invariants to enforce

1. **No panics.** Any byte sequence yields an error; failures crossing the public
   client boundary are `*smtp.Error`. Covers slice bounds, nil derefs, and
   integer overflow in `BDAT` size arithmetic.
2. **No unbounded allocation.** Reply line length, continuation-line count, total
   reply size and announced `BDAT` chunk size are all capped, and the cap is
   checked *before* allocating. `BDAT 4294967295` must be rejected, not
   attempted.
3. **No hangs.** Every production network read observes a deadline. A server that
   replies `354` and then stalls must time out.
4. **No desynchronisation.** This is the highest-value invariant in an SMTP
   client, and it has three distinct sources:
   - **Reply counting under pipelining.** A multiline reply misread as several
     replies, or the reverse, attributes a `550` to the wrong recipient. RFC
     2920 §3.1 names this directly.
   - **LMTP reply counts.** More or fewer post-`DATA` replies than accepted
     recipients. Must error and poison the connection, never resynchronise
     by guessing.
   - **Transparency framing.** Content that terminates the `DATA` stream early
     because a `.` was not stuffed, or a `BDAT` chunk whose byte count does not
     match what was written.

   Silent desync attributes a result to the wrong recipient — a correctness *and*
   a security bug. In a mail sender it can mean reporting a message as delivered
   when it was rejected.

## Work

- `Fuzz*` target per parser entry point, seeded from `testdata/` and from real
  interop captures. Corpus committed; crashers gitignored until minimised into a
  regression test.
- **Adversarial fake server** (`interop/harness/adversarial/`): reply codes that
  are not three digits; continuation lines whose codes disagree with the final
  line; a 10 MiB single reply line; a reply arriving before any command; more
  replies than commands issued; `421` at every point in a transaction; `354`
  followed by silence; `BDAT` accepting fewer bytes than announced; LMTP sending
  N+1 and N−1 replies; bare CR and bare LF; NUL bytes in reply text; an EHLO
  reply with 10 000 keywords; a keyword line of 100 KiB.
- `go test -race` across everything touching the connection.
- Memory-bound test: send a 200 MiB message through both `DATA` and `BDAT`,
  assert peak allocation stays flat. This is the regression test for the
  streaming guarantee.

## Also yours

- Assert wire tracing redacts `AUTH` payloads — every mechanism, including the
  initial response in the `AUTH` command itself.
- Assert TLS verification is on by default.
- Assert credentials are refused over an unencrypted connection unless explicitly
  opted in.
- Assert post-`STARTTLS` extensions are re-fetched and the cleartext list
  **discarded**, not merged. RFC 3207 §4.2; the cleartext list is
  attacker-supplied.

## Escalation

Findings that require an API change go to `api-guardian` with the failing input.
Do not make the change yourself — you do not own those files.

## Done when

All fuzz targets have recorded 10-minute clean runs via `.state/run-fuzz.sh`
(human-approved campaign duration, 2026-08-03)
(which **discovers** targets rather than reading a list — a hand-maintained list
is how the sibling repo shipped three extension groups with no targets at all).
The adversarial suite, module-wide `-race` run, memory-bound streaming
regression, production read deadlines, and the four security assertions all pass.
T13 owns promoting these into CI jobs; CI file ownership is not a completion
dependency here.
