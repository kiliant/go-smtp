# T30 — Delivery fuzzing, adversarial fixtures, interop (D06)

**Agent:** fuzz-hardening + interop-harness · **Milestone:** M7 ·
**Depends on:** T26, T27, T28, T29

**Owns:** `smtpdeliver/**/*_fuzz_test.go` and `smtpdeliver/testdata/**`. The
more specific path beats T11's `**/*_fuzz_test.go`, as BOARD.md's precedence
rule states. Also owns `interop/delivery/**`, which beats T06's `interop/**` the
same way. Appends cases to `fakeresolver_test.go` and `fakepeer_test.go`.

This is work package D06 of `docs/DELIVERY-DESIGN.md` §11. T26–T29 already wrote
their unit, table and mutation tests. This task adds the **hostile** inputs and
the **real servers**.

## Part 1 — fuzzing

Targets (design §11 "Acceptance evidence"), one per parser or assembler:

- MTA-STS TXT record and policy file (created by T27, taken over here);
- MX pattern matching;
- resolver-result validation: arbitrary `MXLookup`, `IPLookup`, `TXTLookup` and
  `TLSALookup` values, including unknown `LookupState` and `DNSSECStatus`
  strings, empty and nil slices, and oversized names;
- TLSA matching inputs: arbitrary association bytes against generated
  certificates;
- outcome assembly: an arbitrary sequence of scripted replies and failures must
  keep the "every recipient exactly once, in input order" invariant.

Invariants hold under fuzzing: no panic, no recipient lost or duplicated, and no
`delivered` outcome without a 2yz reply. The discovery-based
`.github/scripts/fuzz.sh` finds new targets automatically. Confirm that it does,
and that the `Fuzz (long)` workflow's target count goes up by the new targets.

## Part 2 — adversarial fixtures

Extend the fakes with hostile behaviour, not just failure:

- DNS: CNAME loops, a CNAME to a null MX, 1000 equal-preference MX records, an MX
  pointing at a local address under another name, and DNSSEC state changing
  between the MX and address lookups;
- MTA-STS: a policy server that redirects, a slowloris body, a body at exactly 64
  KiB and one byte over, a `max_age` above the cap, and two TXT records;
- SMTP: a peer that closes after the terminator, one that sends a 250 for the
  terminator but then garbage, and one that stalls the final reply past the
  timeout.

Where the adversarial server in `interop/harness/adversarial/**` (owned by T11)
already does what is needed, reuse it through its existing API. Record any
change it needs in `.state/progress/T30.md` and hand it to T11's owner. Do not
edit it.

## Part 3 — interop

`interop/delivery/**`, behind `-tags=interop`, follows `docs/INTEROP.md`:

- Route through the real matrix servers. A fake resolver returns the container
  address, and the dial hook maps port 25 to the container's mapped port. DNS
  is faked; SMTP is real.
- Mixed RCPT outcomes and MX failover between two real servers. Read back
  through the existing sinks to prove that **no duplicate was delivered**.
- STARTTLS under MTA-STS `enforce`, against a server whose certificate does and
  does not match the policy MX.
- DANE-EE against a real server's certificate, with a TLSA record computed from
  it, plus a deliberately wrong one.

Absent server capabilities skip. A profile that claims a capability and fails
to show it fails (CLAUDE.md, Testing).

## Done when

- Every fuzz target has run clean for the standing 10 minutes on `Fuzz (long)`,
  and the run ID is recorded.
- The interop suite is green on CI, and the run ID is recorded.
- Leak checks are clean.
- `.state/progress/T30.md` lists the mutation records from T26–T29 and confirms
  that each one still fails when its guard is removed on the final tree.
