# 0008 — Y-up, right-handed, metric coordinates

**Status:** Accepted · 2026-08-14

## Context

Four things must agree on a coordinate system: the Go physics simulation, the wire protocol, the
Three.js scene, and every glTF asset.

They disagree by default. Three.js and glTF are Y-up right-handed. Most physics engines and CAD tools
are Z-up. Blender is Z-up in the viewport but exports Y-up glTF. Asset generators — including AI ones
— emit whatever they feel like, in whatever units they feel like.

Coordinate bugs in a billiards game are particularly nasty: a sign error does not crash anything, it
just makes the ball go somewhere plausible but wrong, and the symptom reads as "aiming feels a bit
off" rather than as a bug.

## Decision

**Y-up, right-handed, metres. Origin at table centre, on the cloth plane.**

| Axis | Meaning | Playing-surface extent |
|---|---|---|
| **X** | Long axis — head rail at −X, foot rail at +X | −1.270 … +1.270 |
| **Y** | Up. Cloth plane is `y = 0`; ball centres rest at `y = R` | — |
| **Z** | Short axis | −0.635 … +0.635 |

Named rails: `west` = −X (head), `east` = +X (foot), `north` = +Z, `south` = −Z.
Named pockets: `nw`, `n`, `ne`, `sw`, `s`, `se`.

**The server simulation is 2D.** Physics uses `Vec2{X, Z}`. There is no `y` in the simulation — it is
a rendering concern and a placeholder for future jump shots.

The client conversion is the entire conversion:

```ts
mesh.position.set(ball.x, BALL_RADIUS, ball.z);
```

Units are SI throughout: metres, seconds, kilograms, radians. Every constant lives in
`game/physics/table.go` as the single source of truth, mirrored for reference in MEMORY.md §8 and
docs/coordinates.md.

Assets must conform to the same convention — glTF 2.0, Y-up, metres, origin at model centre on the
play plane — validated by `scripts/validate-asset.mjs`.

## Alternatives considered

**Z-up right-handed**, the convention most physics engines and CAD tools use. Rejected because
Three.js and glTF are both Y-up, so choosing Z-up means a conversion at the renderer boundary *and* a
conversion at the asset boundary. Two conversions, applied in two places, maintained by two different
parts of the codebase — and each is a place for a sign error. The physics engine here is written from
scratch (ADR 0004), so it has no inherited preference. Matching the renderer and the asset format is
free; matching the physics-engine convention costs two conversion layers forever.

**Y-up left-handed** (Unity's convention). Rejected: it disagrees with glTF, so every imported asset
needs mirroring, and mirrored assets break normals and winding order in ways that surface as lighting
artifacts.

**Origin at a table corner.** Rejected. Centre origin makes the table symmetric about both axes,
which simplifies rack geometry, mirroring for the second player's camera, and reasoning about pocket
positions. Corner origin makes every constant an offset.

**Origin at the floor rather than the cloth plane.** Rejected: it makes every ball position carry the
table height as a constant offset for no benefit. Table height is a rendering concern; the scene can
place the whole table wherever it likes.

**Full 3D physics with a `Vec3`.** Rejected. Billiards is planar except for jump shots, which are out
of scope. A third component costs memory bandwidth in the hot loop and adds cases to every collision
test, to support a feature that does not exist. Adding it later is a real change, but a bounded and
deliberate one.

**Imperial units**, matching how billiards equipment is actually specified (2.25″ balls, 9-foot
tables). Rejected: SI keeps the physics equations clean — `g = 9.8`, no conversion factors buried in
friction terms — and matches glTF's metre convention. Imperial values are recorded in the constants
table as documentation, and converted once.

## Consequences

**Good.** Zero coordinate conversion between server, protocol, renderer, and assets. The client
mapping is one line. Assets from any pipeline are validated against one unambiguous specification.
The 2D simulation is smaller, faster, and simpler to test. SI units keep the physics readable.

**Costs.** Anyone arriving from a Z-up background will trip once. Documenting it in three places —
this ADR, MEMORY.md §8, docs/coordinates.md — is the mitigation. Assets generated with default
settings will frequently be wrong (wrong scale, Z-up, corner origin), which is precisely why the
validator exists rather than being optional.

**Future jump shots** would require adding a Y component to the simulation. This is a real change,
not a parameter — but it is bounded, and the alternative (carrying an unused dimension through every
hot-loop operation from day one) is a permanent cost for a speculative feature.

**Non-negotiable.** No part of this system may introduce a coordinate conversion. If something needs
a different convention, it converts at its own boundary and the rest of the system never learns
about it.
