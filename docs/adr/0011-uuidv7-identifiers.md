# 0011 — UUIDv7 identifiers

**Status:** Accepted · 2026-08-14

## Context

Every entity in the system needs an identifier: users, sessions, rooms, matches, wagers, ledger
records. Identifiers appear in URLs, WebSocket messages, and log lines, and they are the primary key
and foreign key of nearly every table.

The constitution asks for a deliberate choice, warns against exposing sequential identifiers without
considering enumeration, and prohibits mixing strategies at random (§57).

## Decision

**UUIDv7 for every externally visible entity**, generated with `uuid.NewV7()` and stored as
PostgreSQL's native `uuid` type.

UUIDv7 encodes a millisecond Unix timestamp in its high bits followed by random bits. That makes it
**time-ordered**: generated values increase monotonically, so index inserts land at the right-hand
edge of the B-tree rather than scattering across it, and `ORDER BY id` is chronological.

**One deliberate exception:** `ledger_entries` uses `bigserial`. Those rows are never exposed in an
API or a URL, they are always accessed through their parent transaction, and they are the
highest-volume table in the system — so an 8-byte key with perfect locality is the right trade.
This exception is recorded here and in MEMORY.md so it does not look like an inconsistency.

Generated in the application, not by the database, so an entity has its identity before it is
persisted and the same value can be used across a multi-statement transaction without a round trip.

## Alternatives considered

**Sequential `bigserial` for everything.** Smallest and fastest — 8 bytes, perfect index locality.
Rejected for externally visible entities because it leaks information and enables enumeration. A user
ID of `1041` tells anyone how many users exist and invites walking the range. `/api/v1/matches/1..n`
is an obvious probe. Authorization must of course reject those requests regardless, but not handing
out the map is basic hygiene. Sequential IDs also make identity dependent on the database, which
complicates generating an entity before persisting it.

**UUIDv4 (fully random).** The common default, and non-enumerable. Rejected because of index
behaviour: random values scatter inserts across the entire B-tree, causing page splits, poor cache
locality, and index bloat that worsens as tables grow. This is a well-documented problem with
UUIDv4 primary keys, and UUIDv7 was standardised specifically to fix it while keeping
unpredictability.

**ULID.** Functionally very close to UUIDv7 — time-ordered and non-enumerable — with a nicer
26-character Crockford base32 text form. Rejected because it requires a third-party library on both
the Go and TypeScript sides, and PostgreSQL has no native ULID type, so it would be stored as
`bytea` or `text` and lose the native `uuid` type's compactness and comparison performance. UUIDv7
gets the same ordering properties using types both Go and PostgreSQL support natively. §9 asks
whether a dependency justifies itself; here it does not.

**Snowflake IDs.** Designed for distributed generation with coordinated worker IDs. Rejected as
solving a problem this system does not have — there is one server process (ADR 0007) — at the cost
of worker-ID coordination, and they are sequential enough to leak volume information.

**Hashids or another opaque encoding over sequential IDs.** Rejected as security through obscurity:
the mapping is reversible, so it hides enumeration from casual inspection but not from anyone who
looks. It also adds an encode/decode step at every boundary.

## Consequences

**Good.** No enumeration and no volume leakage from public identifiers. Index inserts stay local, so
write performance does not degrade the way UUIDv4 does as tables grow. `ORDER BY id` gives creation
order for free, which is useful for pagination and debugging. Identity exists before persistence,
which simplifies multi-statement transactions. Native `uuid` in PostgreSQL and `uuid.UUID` in Go —
no custom types, no serialization layer, one dependency already needed for other reasons.

**Costs.** 16 bytes rather than 8, which doubles the size of every primary key and foreign key. Real,
but acceptable — the tables that would suffer most from this are exactly the ones where the
`bigserial` exception applies. The 36-character text form is bulkier in URLs and logs than a short
integer, and less pleasant to read aloud or type by hand during debugging.

**Note on the timestamp.** UUIDv7 encodes creation time in plaintext. That is intentional and
harmless here — creation time is not sensitive for any entity in this system. Worth remembering if a
future entity ever has a confidential creation time.

**Consistency matters more than the specific choice.** The one exception is documented. Do not add a
second one without updating this ADR and MEMORY.md — mixing identifier strategies at random is
precisely what §57 warns against.
