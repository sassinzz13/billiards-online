# 0006 — Versioned WebSocket protocol envelope

**Status:** Accepted · 2026-08-14

## Context

Realtime gameplay runs over WebSockets between an Angular client and a Go server that ship
independently. Browsers cache aggressively, users keep tabs open for hours, and a deploy can leave an
old client talking to a new server. The protocol will also grow: Phase 5 has auth and rooms,
Phase 9 adds shots, Phase 13 adds resync, Phase 14 adds teams.

Two things need designing up front because retrofitting them is painful: **versioning** and
**reconnect**. §24 requires both, and §60 requires that reconnection be designed from the beginning
rather than bolted on.

## Decision

**A single versioned envelope wraps every message in both directions.**

```jsonc
{
  "v": 1,                    // protocol version — present from message #1
  "type": "shot.request",    // dotted namespace
  "seq": 123,                // server→client: monotonic per connection
  "requestId": "01J...",     // client→server: makes retries idempotent
  "matchId": "01J...",       // optional; present for match-scoped messages
  "ts": 1755100000000,       // unix ms UTC, informational only
  "payload": { }             // type-specific
}
```

Handling rules:

- `v` mismatch → `error` with code `protocol.version`, then close.
- Unknown `type` → `error` with code `protocol.unknown_type`. **Never a silent drop.**
- Oversized frame → `error`, then close. Read limit is 32 KB.
- `seq` is monotonic per connection, so a reconnecting client can say "I last saw 412" and the server
  decides between replaying missed messages and sending a full `state.snapshot`.
- `requestId` is client-generated, and the server deduplicates by it — a retried `shot.request` after
  a flaky connection cannot produce two shots.
- `ts` is informational. **No gameplay decision depends on a client-supplied timestamp.**

Message types are namespaced by domain: `auth.*`, `room.*`, `match.*`, `turn.*`, `shot.*`, `state.*`,
`player.*`, plus `error`.

Versioning policy: additive changes (new message type, new optional field) do not bump `v` — clients
ignore unknown fields, the server rejects unknown types. Breaking changes (changed semantics, removed
field, changed encoding) bump `v`, and the server may serve two versions concurrently during a
rollout.

Owned by `game/protocol`, which imports the standard library only — no Gin, no pgx. The protocol is
testable without a server.

## Alternatives considered

**Unversioned messages, add versioning when needed.** Rejected. Adding a version field later requires
a flag day, because old clients do not know to send it and the server cannot tell an old client from
a malformed one. A single integer costs nothing now and is impossible to retrofit cleanly.

**Version negotiated at the HTTP upgrade** — via a path (`/ws/v1`) or a subprotocol header. Rejected
as less flexible: it fixes the version for the connection's lifetime, and it separates the version
from the messages it describes, so a logged message is no longer self-describing.

**No sequence numbers; full state on every reconnect.** Simpler, and it would work. Rejected because
a full snapshot on every brief network blip is wasteful, and because `seq` costs one integer while
enabling the server to choose the cheaper path. Reconnects will be common — mobile networks, laptop
sleep, tab throttling.

**Separate endpoints per concern** — `/ws/lobby`, `/ws/match`. Rejected. It multiplies connections
per user, complicates auth and presence, and conflicts with §66's single-connection requirement on
the client. One connection with typed routing is simpler on both ends.

**Protocol Buffers or another schema-driven format from the start.** Rejected for now under §25 and
§9: it adds a codegen step and a dependency before any measurement shows JSON is insufficient. The
envelope is deliberately designed so a binary payload can slot in later without changing the
envelope's shape.

**Trusting client timestamps for shot timing.** Rejected on security grounds. A client that controls
`ts` controls the shot clock. The server's own clock is authoritative for every deadline.

## Consequences

**Good.** Old clients fail with a clear, actionable error instead of behaving strangely. Reconnect and
resync are designed in from Phase 5, so Phase 13 implements a plan rather than inventing one under
pressure. Retried shot requests are idempotent by construction. Every message is self-describing,
which makes logs and packet captures readable. The protocol is unit-testable without a running
server.

**Costs.** Envelope overhead of roughly 80–120 bytes per message as JSON. Negligible here —
precomputed shots (ADR 0005) mean only a handful of messages per turn, so the overhead that would
matter in a streaming design does not arise. Both sides must maintain the version policy, and
supporting two versions during a rollout means a real branch in the handler.

**Watch for.** The temptation to let one message type mean slightly different things depending on
context. It ends in a protocol nobody can reason about. If the meaning differs, the type differs.
