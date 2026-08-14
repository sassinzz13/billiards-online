# Realtime protocol

WebSocket contract between the Angular client and the Go server. Owned by `game/protocol`.

**Version 1.** Implemented incrementally: envelope and auth in Phase 5, match messages in Phase 6,
shot messages in Phase 9, resync and reconnect in Phase 13.

---

## 1. Transport

Single endpoint: `wss://api.billiards.localhost/ws`.

Authentication happens at the HTTP upgrade, not in a message. The session cookie is sent
automatically because Traefik puts the web app and the API on one origin. The upgrade is guarded by
a **strict Origin allowlist**, which is what actually blocks cross-site WebSocket hijacking.

| Limit | Value |
|---|---|
| Max inbound frame | 32 KB |
| Outbound queue per connection | 64 messages |
| Inbound command queue per match | 32 |
| Idle ping interval | 30 s |
| Pong deadline | 10 s |

**Backpressure:** if a connection's outbound queue fills, the server closes it with a policy code.
Messages are never dropped and never drained. Shot results are large and non-idempotent, so losing
one desyncs the client — close-and-resync is the only correct behaviour. The client reconnects and
requests a full snapshot.

---

## 2. Envelope

Every message, both directions, is wrapped:

```jsonc
{
  "v": 1,                    // protocol version — present from message #1
  "type": "shot.request",    // dotted namespace
  "seq": 123,                // server→client: monotonic per connection
  "requestId": "01J...",     // client→server: makes retries idempotent
  "matchId": "01J...",       // optional; present for match-scoped messages
  "ts": 1755100000000,       // unix ms, UTC, informational only
  "payload": { }             // type-specific
}
```

Rules:

- `v` mismatch → `error` with code `protocol.version`, then close.
- Unknown `type` → `error` with code `protocol.unknown_type`. **Never a silent drop.**
- Payload exceeding the frame limit → `error`, then close.
- `seq` is per-connection and monotonic, so a reconnecting client can say "I last saw 412".
- `requestId` is client-generated. The server deduplicates by it, so a retried `shot.request` after
  a flaky connection cannot produce two shots.

Timestamps are informational. **No gameplay decision depends on a client-supplied `ts`.**

---

## 3. Message catalogue

### Connection

| Type | Dir | Payload | Notes |
|---|---|---|---|
| `auth.success` | S→C | `{ userId, connectionId }` | First message after a successful upgrade |
| `error` | S→C | `{ code, message, requestId? }` | Never carries internal detail — no stack traces, no SQL |
| `ping` / `pong` | both | — | Protocol-level liveness |

### Rooms

| Type | Dir | Payload |
|---|---|---|
| `room.joined` | S→C | `{ roomId, config, members[] }` |
| `room.updated` | S→C | `{ roomId, members[], readyState }` |
| `room.left` | S→C | `{ roomId, userId }` |

### Match lifecycle

| Type | Dir | Payload |
|---|---|---|
| `match.starting` | S→C | `{ matchId, sides, startsAt }` |
| `match.started` | S→C | `{ matchId, state }` — full initial `MatchState` |
| `turn.started` | S→C | `{ matchId, turn: {side, playerIdx}, deadline, ballInHand }` |
| `turn.completed` | S→C | `{ matchId, turn, outcome }` |
| `foul` | S→C | `{ matchId, kind, byPlayer }` |
| `match.completed` | S→C | `{ matchId, winnerSide, reason }` |

### Shots

| Type | Dir | Payload |
|---|---|---|
| `shot.request` | C→S | `{ direction, power, tipOffsetX, tipOffsetY, cueElevation }` |
| `shot.accepted` | S→C | `{ requestId }` — acknowledgement only |
| `shot.rejected` | S→C | `{ requestId, code, message }` |
| `shot.result` | S→C | `{ events[], keyframes, finalState, nextTurn, duration }` |

### Presence and resync

| Type | Dir | Payload |
|---|---|---|
| `state.snapshot` | S→C | Full authoritative state — see §6 |
| `state.resync` | C→S | `{ matchId, lastSeq }` |
| `player.disconnected` | S→C | `{ matchId, userId, graceEndsAt }` |
| `player.reconnected` | S→C | `{ matchId, userId }` |
| `spectator.joined` | S→C | `{ matchId, count }` |

---

## 4. Shot request and validation

```jsonc
{
  "v": 1, "type": "shot.request", "requestId": "01J...", "matchId": "01J...",
  "payload": {
    "direction":   1.5708,   // radians, aim heading in the XZ plane
    "power":       6.5,      // m/s, cue ball speed
    "tipOffsetX":  0.0,      // metres from ball centre, side spin (English)
    "tipOffsetY":  0.012,    // metres from ball centre, top/back spin
    "cueElevation": 0.0      // radians above horizontal
  }
}
```

The server validates **every** field before simulating. Rejection is a `shot.rejected`, never a
best-effort clamp:

| Check | Rule |
|---|---|
| Turn ownership | Sender is the player whose turn it is. Checked first. |
| Match state | Match is `InProgress`, no shot currently resolving. |
| `power` | `0 ≤ power ≤ 12` m/s |
| `tipOffsetX/Y` | `√(x² + y²) ≤ 0.7 R` — beyond that is a miscue |
| `cueElevation` | `0 ≤ elevation ≤ π/2` |
| `direction` | finite |
| **All floats** | **finite — `NaN` and `Inf` rejected explicitly** |
| Rate limit | Per-connection shot rate |

Ranges live in `game/physics/table.go` and MEMORY.md §8. The client applies the same limits for UI
feedback, but that is convenience only — **the server never trusts it**.

---

## 5. Shot result

The load-bearing design choice: the server resolves the **entire shot** in one synchronous call and
ships the whole trajectory in one message. See [ADR 0005](adr/0005-precomputed-shot-trajectory.md).

```jsonc
{
  "v": 1, "type": "shot.result", "seq": 413, "matchId": "01J...",
  "payload": {
    "duration": 3.85,              // seconds of table time
    "events": [                    // physics facts, in time order
      { "t": 0.412, "kind": "ball_hit_ball",    "a": 0,  "b": 9 },
      { "t": 0.783, "kind": "ball_hit_cushion", "ball": 9, "rail": "north" },
      { "t": 1.204, "kind": "ball_pocketed",    "ball": 9, "pocket": "ne" },
      { "t": 3.850, "kind": "all_stopped" }
    ],
    "keyframes": { "rate": 60, "balls": [ /* see below */ ] },
    "finalState": { /* authoritative ball positions and rule state */ },
    "nextTurn":   { "side": "A", "playerIdx": 0, "ballInHand": false }
  }
}
```

`events` are **physics facts only**. The rule interpretation (foul, scratch, turn change, win)
arrives as separate `foul` / `turn.completed` / `match.completed` messages, because physics and rules
are separate layers and the protocol reflects that.

### Keyframes

Sampled at 60 Hz — every 8th step of the 1/480 s simulation. **Only balls that moved are included.**

Phase 9 ships JSON and measures. Sizing is already known: a 6-ball shot over 4 s is ~5.7 KB as
`int16` positions quantised to 0.1 mm (table maximum 25 400 units, comfortably inside `int16`).
Equivalent JSON is roughly 10× that.

The envelope is therefore designed to carry either a JSON payload **or** a binary WebSocket frame
keyed by `requestId` — but **the binary codec is not built until a measurement justifies it**. Do not
add it speculatively.

The client derives visual ball roll from displacement. That is rendering only and never affects
gameplay, so angular velocity does not need to be on the wire.

### Playback contract

1. Client receives `shot.result` and plays keyframes at wall-clock rate with interpolation.
2. On playback end, the client **snaps to `finalState`**.
3. If the client is behind (tab backgrounded, slow device), it may skip ahead — but it always ends
   at `finalState`.

If the client's rendered state and the server's `finalState` ever disagree, **the server wins**
without negotiation.

---

## 6. Reconnect and resync

Designed from the start, not bolted on (§60). Connections will not survive a whole match.

```
client reconnects
    ├─ HTTP upgrade with the session cookie (unchanged)
    ├─ auth.success
    └─ state.resync { matchId, lastSeq }
            │
            ▼
       server compares lastSeq
            │
            ├─ close behind  →  replay the missed messages
            └─ far behind or mid-shot  →  state.snapshot (full)
```

`state.snapshot` carries everything needed to resume with no inference (§60):

```jsonc
{
  "matchId": "01J...",
  "matchState": "in_progress",
  "balls": [ /* authoritative positions */ ],
  "sides": [ /* players per side */ ],
  "scores": { },
  "ruleState": { "openTable": false, "groups": { "A": "solids", "B": "stripes" } },
  "turn": { "side": "A", "playerIdx": 0, "deadline": 1755100030000, "ballInHand": false },
  "participants": [ { "userId": "...", "connected": true } ],
  "shotInProgress": { "requestId": "...", "elapsed": 1.24, "result": { } }
}
```

**Mid-shot rejoin** is why `shotInProgress.elapsed` exists: the client receives the full trajectory
plus a seek offset and starts playback partway through. This falls out naturally from precomputed
shots — with streamed snapshots it would require replaying history.

### Disconnection

Networking reports a fact; rules decide what it means (§61).

```
transport: "player disconnected"
              │
              ▼
game/rules: pause? grace period? forfeit? team continues?
```

`platform/websocket` and `internal/realtime` never decide match outcomes.

---

## 7. Errors

```jsonc
{ "v": 1, "type": "error", "seq": 99, "payload": {
    "code": "shot.not_your_turn",
    "message": "It is not your turn.",
    "requestId": "01J..."
} }
```

Codes are stable, dotted, and safe to switch on. Messages are human-readable and **never contain
internal detail** — no stack traces, no SQL, no infrastructure hints (§42, §51).

| Code | Meaning |
|---|---|
| `protocol.version` | Unsupported `v` |
| `protocol.unknown_type` | Unrecognised `type` |
| `protocol.too_large` | Frame exceeded the read limit |
| `auth.required` | Not authenticated |
| `auth.forbidden` | Authenticated but not authorized for this action |
| `shot.not_your_turn` | Turn ownership check failed |
| `shot.invalid_params` | Range or finiteness check failed |
| `shot.rate_limited` | Too many shot requests |
| `match.not_found` | Unknown or inaccessible match |
| `match.invalid_state` | Action illegal in the current match state |
| `internal` | Anything else. Logged with full detail server-side, opaque to the client. |

---

## 8. Versioning

`v` is present from message #1 so a breaking change never requires a flag day.

- **Additive** — new message type, or a new optional payload field: no version bump. Clients ignore
  unknown fields; the server answers unknown types with `error`.
- **Breaking** — changed field semantics, removed field, changed encoding: bump `v`. The server may
  support `v: 1` and `v: 2` concurrently during a rollout.

The client sends the highest version it supports. The server replies at that version or rejects with
`protocol.version`.
