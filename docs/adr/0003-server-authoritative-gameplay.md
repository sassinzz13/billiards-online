# 0003 — Server-authoritative gameplay

**Status:** Accepted · 2026-08-14

## Context

This is a competitive game with rankings and virtual wagering. Outcomes have value, which means
someone will eventually try to manipulate them. Browser clients are entirely under the user's
control: JavaScript can be edited, WebSocket frames can be forged, and memory can be patched. No
amount of client-side obfuscation changes that.

The constitution names this as one of its most important rules (§13) and makes server authority the
primary anti-cheat mechanism (§43).

## Decision

**The server is authoritative over every competitive outcome.** The client submits *intent*; the
server determines what happened.

The client may send exactly one thing that affects the game:

```
shot.request { direction, power, tipOffsetX, tipOffsetY, cueElevation }
```

The server then, in order: verifies the sender is the player whose turn it is, validates every
parameter against physical limits, runs the authoritative simulation, applies the rules to the
resulting events, updates match state, and broadcasts the result.

The client is authoritative over nothing: not ball positions, velocities, or spin; not collisions,
pockets, fouls, or scratches; not scores, turns, or winners; not ratings, wagers, or wallet balances.

Client-side prediction is limited to what cannot affect outcomes — cue aiming preview, camera, and
trajectory playback. Where client and server disagree, **the server wins** and the client snaps
without negotiation.

Validation is explicit and rejects rather than clamps:

| Check | Rule |
|---|---|
| Turn ownership | Sender is the current player. Checked first, before anything else. |
| Match state | `InProgress`, no shot currently resolving |
| `power` | `0 ≤ power ≤ 12` m/s |
| `tipOffsetX/Y` | `√(x² + y²) ≤ 0.7 R` |
| `cueElevation` | `0 ≤ elevation ≤ π/2` |
| All floats | Finite — `NaN` and `Inf` rejected explicitly |

## Alternatives considered

**Client-side simulation with server validation.** The client simulates and reports the result; the
server checks plausibility. Tempting because it eliminates simulation cost on the server. Rejected
because meaningful validation would require re-simulating anyway — at which point the client's
simulation is redundant — and anything less than re-simulation reduces to trusting a number. A
cheater reports a plausible-looking made shot and the server has no basis to refuse.

**Deterministic lockstep.** Both clients simulate identically from the same inputs; the server relays
and detects divergence. Standard in RTS games. Rejected because it requires bit-identical
floating-point behaviour across browsers, JavaScript engines, CPU architectures, and Go — which is
not achievable in practice — and because divergence detection tells you something went wrong without
telling you which side is honest.

**Trusted client with statistical anti-cheat.** Let the client be authoritative and detect anomalies
after the fact. Rejected: it is reactive rather than preventive, produces false positives against
skilled players, and cannot work at all when wagers settle immediately on match completion.

**Server authority only for ranked and wagered matches.** Rejected as false economy — it means two
code paths for the same game, and the unranked path becomes the testing ground for exploits against
the ranked one.

## Consequences

**Good.** Cheating requires compromising the server, not the client. Match outcomes are trustworthy
enough to settle wagers against. Every shot is reproducible from stored parameters. Rules exist in
exactly one implementation, so client and server cannot drift. No client-side anti-cheat is needed —
nothing invasive, nothing to bypass.

**Costs.** The server pays the simulation cost for every shot. This is genuinely small — roughly 1 ms
of CPU for a 4-second shot (ADR 0005) — but it is real and scales with concurrent matches. Latency is
visible: the player waits a round trip before balls move. Mitigated by local cue aiming, which is
instant because it affects nothing, and by shipping the whole trajectory at once so there is exactly
one round trip per shot rather than continuous exposure.

**Implication for the client.** Client-side physics for the aim preview line will not exactly match
the server. Keep the preview modest — a short aim ray rather than a full predicted trajectory — so
the discrepancy never becomes visible as a broken promise.

**Non-negotiable.** No future optimization may move authority to the client. If shot resolution
becomes a bottleneck, the answer is a faster simulation or more servers, never a trusted client.
