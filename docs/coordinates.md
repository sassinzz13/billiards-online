# Coordinates, table constants, and the asset contract

The server, the client, and every 3D asset agree on one coordinate system. Getting this wrong once
produces sign-error bugs that surface months later as "aiming feels slightly off."

Code source of truth: `game/physics/table.go`. This document explains it; MEMORY.md §8 summarises it.

---

## 1. The convention

**Y-up, right-handed, metres. Origin at table centre, on the cloth plane.**

```
                        +Y (up)
                         │
                         │
        ┌────────────────┼────────────────┐
        │                │                │  ← +Z  (short axis, "north" rail at +Z)
        │                ●────────────────┼──> +X  (long axis)
        │             origin              │
        └─────────────────────────────────┘
     head rail                        foot rail
      x = −1.27                        x = +1.27
```

| Axis | Meaning | Extent of the playing surface |
|---|---|---|
| **X** | Long axis. Head rail at −X, foot rail at +X. | −1.270 … +1.270 |
| **Y** | Up. Cloth plane is `y = 0`; ball centres rest at `y = R`. | — |
| **Z** | Short axis. | −0.635 … +0.635 |

Named rails: `west` = −X (head), `east` = +X (foot), `north` = +Z, `south` = −Z.
Named pockets: `nw`, `n`, `ne`, `sw`, `s`, `se`.

### Why this convention

It is Three.js's native convention **and** glTF's. A server position maps to a scene position with
zero conversion:

```ts
mesh.position.set(ball.x, BALL_RADIUS, ball.z);   // that's the whole conversion
```

Z-up — the convention most physics engines use — was rejected because it requires a conversion at
*both* the renderer boundary and the asset boundary. Every such conversion is a place for a sign
error, and sign errors in a billiards game are subtle: the ball goes somewhere plausible, just wrong.

Full rationale: [ADR 0008](adr/0008-coordinate-system.md).

### The server is 2D

Server physics uses `Vec2{X, Z}`. There is no `y` in the simulation. Balls roll on a plane; the
height coordinate is a rendering concern and a placeholder for future jump shots.

Do not add a third component to the physics vector "for completeness." It costs memory bandwidth in
the hot loop and buys nothing until jump shots exist.

---

## 2. Table and ball constants (WPA 9-foot)

| Quantity | Symbol | Value |
|---|---|---|
| Playing surface | — | 2.540 m × 1.270 m (100″ × 50″) |
| Surface extent | — | x ∈ [−1.270, +1.270], z ∈ [−0.635, +0.635] |
| Ball radius | `R` | 0.028575 m (2.25″ diameter) |
| Ball mass | `m` | 0.170 kg |
| Cushion nose height | — | 1.27 R (63.5% of ball diameter) |
| Sliding friction | `μ_s` | ≈ 0.2 |
| Rolling friction | `μ_r` | ≈ 0.01 |
| Spinning friction | `μ_sp` | ≈ 0.044 · R |
| Ball–ball restitution | `e_bb` | ≈ 0.95 |
| Ball–cushion restitution | `e_bc` | ≈ 0.85 |
| Corner pocket mouth | — | 0.114 m (4.5″) |
| Side pocket mouth | — | 0.127 m (5″) |
| Gravity | `g` | 9.8 m/s² |
| Head string | — | x = −0.635 |
| Foot spot (rack apex) | — | x = +0.635 |
| Max legal cue-ball speed | — | 12 m/s |
| Stop threshold | — | \|v\| < 1e-3 m/s **and** \|ω\| < 1e-2 rad/s |

Friction and restitution values are starting points to be tuned in Phase 10 against published
reference shots. When a value changes, change it in `table.go`, update MEMORY.md §8, and note why.

---

## 3. Simulation timestep

**Fixed `dt = 1/480 s`.** Never coupled to frame rate, refresh rate, or packet rate.

Why 1/480 rather than the more common 1/240: at the 12 m/s speed cap a ball advances 25 mm per step,
against a 57.15 mm diameter. At 1/240 it advances 50 mm — close enough to the diameter that
tunnelling through a thin contact becomes likely on a hard break.

Swept (quadratic time-of-impact) ball–ball and ball–cushion tests are used **in addition** to the
small step, not instead of it. The small step keeps the swept solve well-conditioned.

**Broadphase is O(n²) and that is correct here.** 16 balls = 120 pairs; a 4-second shot is ~1920
steps ≈ 230k pair tests, comfortably sub-millisecond. A spatial hash would add allocation and
indirection to save nothing. Do not add one.

Keyframes are sampled at 60 Hz — every 8th step.

---

## 4. Rack geometry

Standard 8-ball rack: apex ball on the foot spot at `x = +0.635, z = 0`, rows extending toward +X.

```
row 0:            ●                      x = 0.635
row 1:           ● ●                     x = 0.635 + 1·√3·R
row 2:          ● 8 ●                    x = 0.635 + 2·√3·R
row 3:         ● ● ● ●                   x = 0.635 + 3·√3·R
row 4:        ● ● ● ● ●                  x = 0.635 + 4·√3·R
```

Row spacing is `√3 · R` along X; within a row, balls are spaced `2R` along Z and centred on `z = 0`.
The 8-ball sits at the centre of row 2. Corner balls of the back row must be one solid and one
stripe. Cue ball starts behind the head string at `x = −0.635, z = 0`.

Rack generation lives in `game/state`, not in `game/physics` — it is a game setup concern, not a
physical one.

---

## 5. Ball numbering

| Index | Ball |
|---|---|
| 0 | Cue ball |
| 1–7 | Solids |
| 8 | 8-ball |
| 9–15 | Stripes |

Index equals ball number. `[16]Ball` is a fixed-size array, indexed directly — no map, no lookup, no
allocation.

---

## 6. 3D asset contract

**Render meshes are never authoritative.** Server collision geometry is mathematical — planes,
circles, quadratics. A `table.glb` can be swapped for a prettier one with zero gameplay change, and
a beautiful mesh never becomes a physics surface.

Every asset must satisfy:

| Requirement | Value |
|---|---|
| Format | glTF 2.0 binary (`.glb`) |
| Orientation | Y-up, right-handed |
| Units | Metres |
| Origin | Model centre, on the play plane |
| Table playing surface | Exactly 2.540 × 1.270 |
| Ball mesh | Radius 0.028575, centred at origin |
| Textures | KTX2 / Basis, ≤ 2048² |
| Compression | Draco or Meshopt |
| Materials | PBR metallic-roughness only |
| Triangles — table | ≤ 40 000 |
| Triangles — cue | ≤ 8 000 |
| Triangles — ball | ≤ 2 000 |

Validated by `scripts/validate-asset.mjs`, which checks orientation, bounding-box dimensions, origin
placement, triangle count, texture size, and material type. **An asset that fails the script does not
get committed.**

### AI-generated assets

The project expects AI-assisted asset generation for the table, cues, balls, environments, and later
cosmetics. Generated assets are held to exactly the same contract — the generator is a source, not
an exemption.

The recurring failure modes worth knowing about: wrong scale (centimetres instead of metres), Z-up
export, origin at a corner rather than centre, absurd triangle counts, and 4K textures for objects
that occupy 200 px on screen. The validator catches all five, which is the point of having it.

Until an asset passes, **use procedural placeholders** — `BoxGeometry` for the table,
`SphereGeometry` for balls, `CylinderGeometry` for the cue. Gameplay must never be blocked waiting
on art.

---

## 7. Client rendering notes

Server sends 2D positions. The client places meshes at `(x, R, z)`.

**Ball roll orientation is derived on the client from displacement** — a rolling ball rotates about
the axis perpendicular to its travel, by `|Δp| / R` radians. This is visually correct for the
overwhelming majority of ball motion and keeps angular velocity off the wire entirely.

It is a rendering approximation and it is allowed to be one, because ball orientation has **no
gameplay meaning** in 8-ball. If a future ruleset needs authoritative orientation, angular velocity
moves onto the wire — but not before.
