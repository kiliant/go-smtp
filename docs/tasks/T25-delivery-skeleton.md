# T25 — `smtpdeliver` skeleton, public types, API gates (D01)

**Agent:** client-core + api-guardian · **Milestone:** M7 ·
**Depends on:** T14 (approved design). Runs in parallel with T24.

**Owns:** `smtpdeliver/{doc,deliverer,options,request,result,resolver,event}.go`,
`smtpdeliver/resolver_std.go`, `smtpdeliver/api_surface_test.go`, and the shared
test fake `smtpdeliver/fakeresolver_test.go` (structure owned by T25; T26–T30
append cases, nobody deletes another task's cases). Also holds a **scoped edit
grant** on the root `api_surface_test.go` (owned by T12). The grant covers only
adding `smtpdeliver` to that file's package lists.

This is work package D01 of `docs/DELIVERY-DESIGN.md` §11. It fixes the exported
shape that T26–T29 fill in. A type that is wrong here is wrong for good: the
package ships from the **root v1 module** and is stable from its first tag
(design §1). Unlike `smtpserver`, there is no v0 phase.

## Deliverables

### 1. Every exported type in design §§2, 3, 5 and 8

Declare all of them, with the names the design gives unless api-guardian renames
them:

- `Deliverer`, `New`, `Options`, `MTASTSOptions`, `DANEOptions`, `DANEMode`,
  `DialRequest`, `Timeouts`, `Event`.
- `Request`, `Destination`, `Recipient`, `MessageSource`, `OpenMessageOptions`,
  `DeliverOptions`.
- `Resolver` and every `Lookup*Request` / `*Lookup` type, `LookupState`,
  `DNSSECStatus`, `TLSA`.
- `PolicyCache`, `PolicyCacheLoadRequest`, `PolicyCacheStoreRequest`,
  `PolicyCacheEntry`, `MTASTSPolicy`, `MTASTSMode`, `RefreshPolicyOptions`.
- `Result`, `DestinationResult`, `RecipientOutcome`, `AttemptResult`,
  `PolicyResult`, `Disposition`, `AttemptStage`, `PolicyKind`, `PolicySource`.

Rules, all from `API-STABILITY.md`, all gated mechanically:

- Every caller-constructed struct carries `_ struct{}`.
- Every named string kind is an **open** string type. Unknown values round-trip;
  nothing switches on them exhaustively.
- `Resolver` and `PolicyCache` are **structs of function fields**. The package
  exports **no interface**.
- No `internal/` type appears in any exported signature.
- Every blocking entry point takes `context.Context` first and an options
  pointer, where `nil` means documented defaults.

Fields the design marks as later additions stay out: address-literal routing on
`Destination`, parallel destination execution, connection reuse.

### 2. `New` and request validation

`New` validates configuration, clones caller state (`TLSConfig`, slices, the
HTTP client's policy-relevant settings), and never mutates it. Rejections it
must implement now:

- DANE enabled without `LookupMX`, `LookupIP` and `LookupTLSA` (design §3);
- MTA-STS enabled without a `Cache` (design §5);
- no local identity for Internet MX delivery, unless an explicitly named opt-out
  is set (design §4);
- an address cap below two without the explicit RFC 5321 §5.1 opt-in.

Request validation, before any DNS or wire I/O: duplicate destination domains
(case-insensitive), non-ASCII or relative domains, `MessageSource.Open == nil`,
and `MessageSource.Size` disagreeing with `MailOptions.Transport.Size`.

### 3. The standard-library resolver adapter

A zero `Resolver` uses `net.DefaultResolver` for MX, IP and TXT. It reports
`DNSSECUnvalidated` and maps NXDOMAIN to `LookupNotFound`. It distinguishes
NXDOMAIN from an empty answer wherever the standard library lets it. Where the
standard library cannot tell them apart, document the limitation in the doc
comment rather than guessing. There is no TLSA implementation.

### 4. Method stubs

`Deliver` and `RefreshPolicy` exist with their final signatures. Until T29 and
T27 land, they return an unexported "not implemented" error. That is acceptable
**only** because of the release hazard below.

Put the stubs in `smtpdeliver/attempt_deliver.go` and
`smtpdeliver/mtasts_refresh.go`, not in `deliverer.go`. T25 creates those two
files, and from then on they belong to T29 and T27 by file prefix. This is the
same created-by/owned-from arrangement BOARD.md uses for fuzz targets. It means
filling in a stub never crosses an ownership boundary.

## Release hazard — read before merging anything

From the moment this task merges, the tree holds a partial stable package. **No
root-module tag may be cut while `smtpdeliver` is incomplete.** A tag would
freeze stubs and half-reviewed types under v1. There are two ways to enforce
this. The human chooses one before T25 merges:

1. **Integration branch (recommended):** T25–T30 merge into `feat/smtpdeliver`.
   T31 merges that branch into `main` once. Interop fixes on `main` keep flowing
   and can be tagged.
2. **Land on `main` and freeze root tags** until T31. Simpler, but it blocks any
   root patch release, T24 included, for the duration.

## Testing

- `smtpdeliver/api_surface_test.go`: every exported struct is guarded; no
  exported interface; every exported method that blocks has `ctx` first and an
  options pointer last. Each gate needs a self-test that **fails on a shape the
  real code could actually produce**. This is the M0 lesson recorded in
  `.state/status.md`, where the gate could not see `*smtp.MailOptions`.
- `New` and request-validation table tests, one row per rejection above.
- Resolver adapter tests that need no network: an injected `*net.Resolver` with
  a `Dial` hook serving canned DNS responses from a test-only UDP or TCP stub.
- The zero-dependency check still passes. `go.mod` is unchanged.

## Done when

The package builds. All gates and tests pass. api-guardian has reviewed **the
whole exported surface**, which the later tasks are filling rather than shaping,
and approved it. `CHANGELOG.md` `[Unreleased]` has an entry. The human's
release-hazard choice is recorded in `.state/status.md`.
