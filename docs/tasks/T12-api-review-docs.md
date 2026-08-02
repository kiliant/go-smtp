# T12 — API review & docs

**Agent:** `docs-release` (with `api-guardian`) · **Milestone:** M4 ·
**Depends on:** T04, T07, T09, T10 — i.e. the **whole** exported surface. A
freeze review that runs before auth or LMTP has landed is reviewing a surface
that is about to change.

**Owns:** doc comments across the tree, `examples/**`, `api_surface_test.go`
(created by T02, yours from M4)

## Goal

The last review before the surface freezes. After v1.0 every mistake here is
permanent.

## Deliverables

### The full-surface audit

Walk the entire exported API of `smtp` and `smtpclient` against all nine rules in
`docs/API-STABILITY.md`. Not a spot check — the sibling repo's freeze audit found
seven distinct issue classes, and the largest (28 methods missing options
structs) had been invisible for months because the rule was prose.

For each rule, the audit produces evidence, not an assertion: which symbols were
checked and what stressed them.

### Extend the mechanical gates

`api_surface_test.go` exists from T02 with four gates. Extend it to cover the
surface as it now stands, and add anything the audit found that can be mechanised.
A rule that only a human enforces is a rule that will be violated after the human
stops looking.

Audit the two exemption maps specifically. `nonBlockingClientMethods` is the
looser one and silences **two** gates at once; every entry needs to still be a
genuine non-blocking accessor.

### Doc comments

Every exported symbol. Each one names the RFC it comes from — this is a library
whose users are reading RFCs alongside it, and it is also how a future agent
avoids inventing an RFC number. Check every cited number against
`docs/RFC-COVERAGE.md`, which is checked against IANA.

Three specific things to say out loud, because callers will otherwise get them
wrong:

- cancelling an in-flight command invalidates the connection;
- `nil` options means defaults, everywhere;
- what this library does **not** do — MX resolution, MTA-STS, DANE, MIME
  composition, DKIM signing — with a pointer to where that belongs.

### Examples (`examples/**`)

Compiled by CI, not just written. The sibling repo had examples that nothing
built. At minimum:

- submission through 587 with STARTTLS and AUTH
- implicit TLS on 465
- multi-recipient with partial rejection, showing the per-recipient result
- large message streaming through `DATA`
- the same through `BDAT`/`CHUNKING`
- LMTP to a local delivery agent
- DSN request with `NOTIFY=`/`ENVID=`
- an extension the library does not model, sent through the `Extra []Param`
  escape hatch — this one is the proof that §1b works, and it belongs in the
  README too

### README and doc.go

Update the status block to reflect reality, including what is *not* done. A
status block that overstates is worse than none.

## Done when

Every exported symbol has a doc comment with a verified RFC citation; the gates
pass; `examples/**` compiles in CI and runs against the interop matrix;
`api-guardian` has issued a written `APPROVED` for the whole surface, naming the
future extensions it was stressed against.
