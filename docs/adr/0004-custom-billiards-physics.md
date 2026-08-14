# 0004 — Custom billiards physics at fixed timestep

**Status:** Accepted · 2026-08-14

## Context

The server must simulate billiards authoritatively (ADR 0003). Billiards physics is unusual: it is a
narrow domain with well-understood analytical models, and its realism depends almost entirely on
effects that general-purpose rigid-body engines model poorly — the sliding-to-rolling transition,
spin transfer between balls, and cushion response to a ball with English.

Players notice when these are wrong. A draw shot that does not draw, or a cushion that rebounds at
the wrong angle with side spin, reads immediately as fake even to casual players.

The constitution requires a billiards-specific simulation and warns against reaching for a
general-purpose 3D engine on the server (§15).

## Decision

**A purpose-built 2D billiards simulation in `game/physics`, stdlib only, at a fixed timestep of
`dt = 1/480 s`.**

The model is planar: balls roll on a plane, so state is `Vec2{X, Z}` plus angular velocity. There is
no `y` in the simulation — height is a rendering concern and a placeholder for future jump shots.

Phase 7 delivers linear motion, cloth friction, rolling resistance, ball–ball and cushion collisions,
pocket capture, and stop thresholds. Phase 10 adds the spin model: sliding and rolling phases, the
transition between them, topspin, backspin, English, throw, and spinning friction decay.

Reference material: Han (2005), *Dynamics in Carom and Three Cushion Billiards*, for the cushion
model, with Evan Kiefl's published corrections; cross-checked against Dr. Dave's physics resources
and the `pooltool` implementation.

Ball state is a flat value struct in a fixed `[16]Ball` array — no pointers, no interfaces, no maps.
The hot loop targets and asserts **zero allocations per shot**.

Collision detection uses **swept quadratic time-of-impact** tests in addition to the small step.
Broadphase is O(n²): 16 balls is 120 pairs, and a 4-second shot is ~1920 steps ≈ 230k pair tests,
comfortably sub-millisecond.

`game/physics` imports the standard library and nothing else. `go test ./game/...` runs with no
database, no Docker, and no network.

## Alternatives considered

**A general-purpose 3D rigid-body engine** (Bullet via cgo, or a Go port). Rejected on several
grounds. Generic engines use iterative constraint solvers tuned for stacking and joints, which model
billiards' dominant effects — sliding-to-rolling transition and spin-dependent cushion response —
poorly or not at all. They allocate, they are hard to make deterministic, cgo complicates builds and
adds latency at the boundary, and they bring a large dependency to solve a problem whose analytical
solution is well documented. §9 asks whether a dependency justifies itself; here it does not.

**3D rigid-body simulation, written in-house.** Rejected as unnecessary. Billiards is planar except
for jump shots, which are out of scope. Carrying a third dimension through every vector operation
costs memory bandwidth in the hot loop and adds cases to every collision test for a feature that does
not exist yet.

**Event-driven continuous simulation** — analytically solving for the next collision time and
advancing directly to it, as `pooltool` does. Genuinely elegant, and exact between events. Rejected
for this project because it complicates the friction model considerably (each segment needs a closed
form, and the sliding/rolling transition becomes another event to solve for), and because keyframe
generation for the wire needs uniform sampling anyway. Fixed stepping gives that for free. The
constitution also explicitly requires a fixed timestep (§15).

**`dt = 1/240 s`**, the more conventional choice. Rejected because at the 12 m/s speed cap a ball
advances 50 mm per step against a 57.15 mm diameter — close enough to the diameter that tunnelling
through a thin contact becomes likely on a hard break. 1/480 halves that to 25 mm and keeps the swept
solve well-conditioned. The cost is trivial: shots are batch-simulated, not ticked in real time
(ADR 0005).

**A spatial hash or sweep-and-prune broadphase.** Rejected as a pessimization at this scale. 120
pairs of flat structs in a contiguous array is faster than any acceleration structure, which would
add allocation and pointer chasing to avoid work that is already cheap.

## Consequences

**Good.** The simulation is fully understood and fully owned — when a shot feels wrong, the maths is
right there. Zero dependencies in the hottest path. Testable in complete isolation, which makes the
physics test suite fast and unconditional. Deterministic within an architecture, enabling replay and
regression golden files. Fast enough that resolving a whole shot synchronously is viable, which is
what makes ADR 0005 possible.

**Costs.** We own the physics, including the parts that are hard. Phase 10 is the real work — spin
and cushion response are where billiards simulations are usually wrong, and tuning is empirical.
Adding jump shots later means introducing a third dimension, which is a real change rather than a
parameter. There is no community to inherit bug fixes from.

**Risks.** Realism has a long tail (risk R3). Phase 7 targets plausible, Phase 10 targets realistic,
and validation must be against published reference shots rather than intuition — "it looks about
right" is exactly how these simulations end up subtly wrong.

Go permits fusing floating-point operations, so results may differ bit-for-bit between arm64 and
amd64 (risk R1). This means replay determinism is guaranteed per-architecture, not universally.
Mitigated by persisting a final-state checkpoint with every shot rather than relying on re-simulation,
and by stamping `simulation_version` on every stored event.
