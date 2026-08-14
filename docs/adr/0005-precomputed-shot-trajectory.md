# 0005 — Precomputed shot trajectory transport

**Status:** Accepted · 2026-08-14
**Note:** This is a deliberate, documented divergence from the constitution's §67 diagram. See below.

## Context

The server is authoritative (ADR 0003) and simulates shots at a fixed 1/480 s timestep (ADR 0004).
The open question was how the authoritative result reaches clients.

The default answer, inherited from FPS netcode and sketched in §67 of the constitution, is a
continuous loop: the server ticks in real time and broadcasts snapshots at 20–30 Hz while balls move;
clients buffer and interpolate.

Billiards, though, is not an FPS. It is **turn-based with a bounded, input-free resolution phase**.
Once the cue strikes, no player input exists until every ball stops. Nothing that arrives during
those ~4 seconds can change the outcome. That property is unusual and worth exploiting.

## Decision

**The server resolves the entire shot in one synchronous call and ships the whole trajectory in a
single message.**

```
client  ──shot.request──>  match actor
                              1. authorize: is it this player's turn?
                              2. validate: ranges, finiteness
                              3. simulation.ResolveShot()   ~1 ms CPU / ~4 s table time
                              4. rules.Apply(events)
                              5. broadcast
                              6. persist match_event   ← async, OFF the simulation path
        <──shot.result───
        { events[], keyframes[], finalState, nextTurn }

client: play back at 60 fps → snap to authoritative finalState
```

Keyframes are sampled at 60 Hz — every 8th simulation step — and only for balls that actually moved.

The simulation is not real-time. A 4-second shot costs roughly 1 ms of CPU: 1920 steps × 120 ball
pairs ≈ 230k pair tests over flat structs in a contiguous array, with zero allocations.

## Alternatives considered

**Streaming snapshots at 20–30 Hz** — the §67 model. Rejected after weighing it against the
turn-based structure:

- It requires a live ticker goroutine per active match. With precomputation the match actor is
  blocked in `select` between shots, costing kilobytes rather than CPU (ADR 0007).
- It sends 80–120 messages per shot instead of one, each with envelope overhead.
- The client must interpolate between snapshots, which introduces jitter under variable latency — for
  motion that is entirely deterministic and already known.
- Mid-shot reconnect is genuinely hard: the client needs the history it missed.
- Packet loss or a stalled client during ball motion produces visible artifacts.

None of these buy anything, because there is no input to respond to during the shot.

**Send only the shot parameters and let the client re-simulate.** This would be the smallest possible
payload — a few dozen bytes. Rejected outright: it requires bit-identical floating-point behaviour
between Go and JavaScript across architectures, which is not achievable. Clients would diverge
visibly, and reconciling divergence would need a snapshot anyway.

**Send only physics events, and let the client interpolate between them.** Rejected: ball paths
between collisions are curved under friction and spin, so events alone under-determine the motion.
The client would have to re-simulate to fill the gaps, which is the previous alternative.

**A hybrid — precompute, then drip-feed keyframes in real time.** Rejected as combining the costs of
both: it needs the ticker back, and gains nothing over sending a payload measured in kilobytes.

## Consequences

**Good.**

- One message per shot instead of 100+.
- No per-match ticker anywhere in the system. The match actor is idle between shots.
- No interpolation jitter — the client has the complete, exact motion.
- Mid-shot reconnect is trivial: send the trajectory plus a playback offset (`shotInProgress.elapsed`).
  What would be the hardest case in a streaming model becomes the easy one here.
- Replay is `(initialState, shotParams)` → deterministic re-run.
- Latency is exposed exactly once per shot, not continuously throughout it.

**Costs.**

- One larger payload rather than many small ones. Sizing: a 6-ball shot over 4 s is ~5.7 KB as
  `int16` positions quantised to 0.1 mm (the table spans 25 400 units at that resolution, comfortably
  within `int16`). JSON of the same is roughly 10×.
- A shot cannot be interrupted once resolved. Acceptable — there is no mechanic that would interrupt
  one.
- Spectators joining mid-shot need a seek offset. Handled by the same mechanism as reconnect.
- The client must handle playback drift when a tab is backgrounded. The contract is simple: it may
  skip ahead, but it always ends at `finalState`.

**Encoding follows measurement, not intuition.** Phase 9 ships JSON and measures payload size, encode
time, and decode time. The envelope is designed to carry either a JSON payload or a binary frame, but
**the binary codec is not built until a measurement justifies it** (§25).

## On the divergence from §67

§67 diagrams `fixed simulation → authoritative snapshots → WebSocket → snapshot buffer →
interpolation → rAF → render`, which reads as continuous streaming.

Every principle that diagram protects is preserved here. The simulation is fixed-timestep. The server
is authoritative. Snapshots go over the WebSocket. The client buffers and interpolates and renders on
`requestAnimationFrame`. The only difference is *when* the snapshots arrive: as a batch at shot
resolution rather than spread across the shot.

Flagged explicitly rather than applied quietly, as §78 requires.
