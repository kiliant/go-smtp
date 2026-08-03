# EHLO captures from the live interop matrix

The keyword sets in this directory were observed by `interop.TestMatrix`
driving this module's client against the seven default podman servers on
2026-08-03 (darwin/arm64). They are seeds for `FuzzEHLOParse`, which T11
requires be fed "from `testdata/` and from real interop captures" rather than
from invented shapes alone.

Real servers are the useful part: they disagree about `SIZE` with and without a
value, they order keywords differently, and Postfix advertises a bare `SIZE`
where Stalwart advertises `SIZE 104857600`. Those are exactly the variations a
hand-written seed does not think to include.

Refresh them by re-reading a matrix run's transcript; do not edit them to be
tidier than the servers actually were.
