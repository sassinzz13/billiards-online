# Architecture

System-level view. Conventions and constants live in [MEMORY.md](../MEMORY.md); per-decision
rationale lives in [adr/](adr/).

---

## 1. System topology

```
                                :80 / :443
                                    │
                            ┌───────────────┐
                            │  Traefik v3.7 │
                            └───────┬───────┘
                          ONE HOST, routed by PATH
                 ┌──────────────────┴──────────────────┐
              /                              /api/…   and   /ws
        priority 1                                 priority 10
        ┌────────▼────────┐                  ┌──────────▼──────────┐
        │  web            │                  │  server             │
        │  Angular 22     │                  │  Go 1.26 / Gin      │
        │  nginx:alpine   │                  │  scratch            │
        └─────────────────┘                  └──────────┬──────────┘
                                                        │
                                             ┌──────────▼──────────┐
                                             │  postgres:18-alpine │
                                             │  volume: pgdata     │
                                             └─────────────────────┘

   dev:   billiards.localhost
   prod:  billiards-online.duckdns.org
```

**Path-based on a single host, not split across subdomains.** This is load-bearing rather than
cosmetic: one host means one origin, which is what lets the session cookie work without a `Domain`
attribute, keeps `SameSite=Lax` fully effective, and removes CORS from the system entirely
(ADR 0009). It also means the client needs no configured API host — every call is relative, so one
container image runs in both environments unchanged.

Traefik handles WebSocket upgrades natively — no middleware required. Development is plain HTTP on
`*.localhost`; production TLS is a Let's Encrypt resolver block that needs no application change.

Root `/health` and `/ready` are deliberately **not** routed publicly. They serve Docker and the
orchestrator from inside the compose network; publicly they fall through to the Angular SPA, so
database state is never exposed (§42). The public probe is `/api/v1/health`.

---

## 2. Backend layering

Features are organized by **product capability**, never by technical layer. There is no
`controllers/`, `services/`, `repositories/`, or `models/` directory anywhere in this repository.

```
apps/server/cmd/server/main.go        composition root — the ONLY place features are wired
        │
        ▼
┌───────────────────────────────────────────────────────────────────┐
│ L6  realtime                                                      │
│ L5  matchmaking                                                   │
│ L4  lobby        rooms                                            │
│ L3  matches ──────────────────────────────────────> game/*        │
│ L2  wagering                                                      │
│ L1  auth         leaderboards                                     │
│ L0  users        wallet                                           │
└───────────────────────────────────────────────────────────────────┘
        │
        ▼
   platform/*      postgres · websocket · config · logging · telemetry · security
```

**Imports point downward only.** This is what makes circular feature dependencies structurally
impossible rather than merely discouraged. Enforced by `tests/arch/boundaries_test.go`, which reads
`go list -json ./...` and fails the build on violation.

Each feature owns its own `handler.go`, `service.go`, `repository.go`, `model.go`, `routes.go`, and
tests. SQL lives as Go consts inside `repository.go`, next to the code that runs it.

### Cross-feature communication

Consumer-side interfaces. The consumer declares the narrow interface it needs; the provider is a
plain struct that happens to satisfy it; `main.go` wires them together.

```go
// internal/matches/service.go — matches declares what IT needs from wagering
type WagerSettler interface {
    Settle(ctx context.Context, matchID uuid.UUID, winner SideID, idempotencyKey string) error
}
```

`internal/wagering` never imports `internal/matches`. That is the entire mechanism. No shared
contracts package, no event bus, no DI container, no internal HTTP, no reflection.

---

## 3. The game engine

`game/` imports the standard library and nothing else. No Gin, no pgx, no WebSockets, no Angular.
`go test ./game/...` must pass with no database, no Docker, and no network — this is a CI gate.

```
protocol ──> state ──> rules ──> physics ──> (stdlib only)
                └───────────────────┘
simulation ──> state, rules, physics
```

| Package | Owns | Must never know about |
|---|---|---|
| `physics` | `Vec2`, `Ball`, `Table`, integrator, collisions, `Event` | rules, Gin, pgx, WebSockets |
| `rules` | 8-ball state machine, consumes `[]physics.Event` | how a collision was computed |
| `state` | `MatchState` — balls, rules state, turn, sides, scores | transport, persistence |
| `simulation` | `ResolveShot(state, shot) → ShotResult` | Gin, pgx |
| `protocol` | wire envelope, codecs, versioning | Gin, pgx |

**Physics and rules never mix.** Physics emits facts:

```
BallHitBall · BallHitCushion · BallHitRail · BallPocketed · AllStopped
```

Rules alone decide meaning: legal · foul · scratch · turn continues · turn changes · win. There is
never an `if isEightBall` inside collision detection.

The dependency direction above is stricter than the constitution's §14, which lists these packages
as flat siblings. Pinning the direction means the compiler enforces the §17 separation instead of
discipline having to.

---

## 4. Realtime path

```
Browser ══wss══> Traefik ══> Go server /ws
    │
    ├─ platform/websocket        connection, read/write pumps
    │                            read limit 32 KB · bounded outbound chan (64)
    │
    ├─ internal/realtime/gateway Origin allowlist → session cookie → bind userID
    │
    ├─ internal/realtime/router  decode envelope → dispatch by type
    │
    └─ match actor               ONE GOROUTINE PER MATCH
                                 owns MatchState exclusively — no mutex on game state
                                 bounded inbound command chan (32)
                                 shot timer via time.Timer
                                 broadcasts through each connection's outbound chan
```

Every long-lived goroutine has an owner, a `context.Context` for cancellation, and a bounded queue.
There are no unbounded channels in this system.

**Backpressure:** if a client's outbound queue fills, the connection is closed with a policy code.
Not drained, not dropped. Shot results are large and non-idempotent, so dropping one desyncs the
client; close-and-resync is the only correct policy. A slow client can never block the simulation.

**Single-instance assumption** through Phase 12: one server process owns all matches. Multi-instance
would need sticky routing or a match locator. Tracked as risk R2 and deliberately deferred — measure
before solving.

---

## 5. Shot resolution — the load-bearing flow

Billiards is turn-based: once the cue strikes, there is no further input until every ball stops. So
the server resolves the entire shot in one synchronous call.

```
client  ──shot.request──>  match actor
                              │  1. authorize: is it this player's turn?
                              │  2. validate: power, tip offset, elevation, all floats finite
                              │  3. simulation.ResolveShot()      ~1 ms CPU / ~4 s table time
                              │  4. rules.Apply(events)
                              │  5. broadcast
                              │  6. persist match_event  ← async, OFF the simulation path
        <──shot.result───     │
        { events[], keyframes[], finalState, nextTurn }

client: play back at 60 fps → snap to authoritative finalState
```

Consequences: one message per shot instead of 100+ · the match goroutine is idle between shots ·
reconnect resyncs with a playback offset · replay is `(initialState, shotParams)` → deterministic
re-run. Full rationale in [ADR 0005](adr/0005-precomputed-shot-trajectory.md).

---

## 6. HTTP vs WebSocket

**Anything that changes live match state goes over WebSocket. Everything else is REST.**

| REST `/api/v1/...` | WebSocket `/ws` |
|---|---|
| signup · login · logout · session | auth handshake |
| profile · statistics | room joined / updated / ready |
| leaderboards (paginated) | match starting / started |
| match history · replay fetch | turn started · shot timer |
| room discovery · create room | shot request / accepted / rejected |
| wallet balance · ledger history | shot result (trajectory) |
| health · readiness | state snapshot / resync |
| | foul · turn completed · match completed |
| | player disconnected / reconnected |

No gameplay is forced through REST, and no CRUD is smuggled into the socket.

---

## 7. Match lifecycle

```
Waiting ──> Starting ──> InProgress ──> Completed
                │             ├──> Paused ──> InProgress
                └─────────────┴──> Cancelled | Abandoned
```

Transitions are validated in exactly one function, `matches.Transition(from, to) error`. Illegal
transitions are rejected there and nowhere else. Terminal states are terminal.

**Rooms are not matches.** A room is players preparing to play — it holds configuration
(public/private, 1v1/2v2, ranked/casual, ruleset, shot timer, wager, spectators) and ready flags. It
creates a match and then has no authority over it.

### Participants

Sides, never `player1`/`player2`:

```go
type Side    struct { ID SideID; Players []uuid.UUID }  // len 1 = 1v1, len 2 = 2v2
type TurnRef struct { Side SideID; PlayerIdx int }
type Match   struct { Sides [2]Side; Turn TurnRef }
```

1v1 and 2v2 differ only in `len(Players)` and the rules layer's turn-advance function. Nothing
outside `game/rules` may compute whose turn it is. Modelled in Phase 6, exercised in Phase 14 — so
2v2 requires no rewrite.

---

## 8. Data ownership

One database, one schema, but every table has exactly one owning feature. A feature never writes
another feature's tables — it calls that feature's service.

| Owner | Tables |
|---|---|
| `users` | `users`, `player_profiles` |
| `auth` | `sessions` |
| `rooms` | `rooms`, `room_members` |
| `matches` | `matches`, `match_sides`, `match_participants`, `match_events` |
| `leaderboards` | `player_ratings`, `player_statistics` |
| `wallet` | `wallets`, `ledger_accounts`, `ledger_transactions`, `ledger_entries` |
| `wagering` | `wagers`, `wager_holds`, `wager_settlements` |

**Live match state never touches PostgreSQL.** Ball positions exist only in the match actor's memory.
Persisted per shot: one `match_events` row containing shot parameters, resulting rule events, and a
final-state checkpoint as JSONB — written after the shot resolves, off the simulation path. No
physics ticks are ever stored.

Transactions are short and explicit. One is never held across a match, a shot, or a network round
trip.

---

## 9. Frontend

```
apps/web/src/app/
├── core/                       singletons, provided at root
│   ├── auth/  networking/  config/  guards/
├── features/
│   ├── auth/  lobby/  rooms/  matchmaking/  profile/  leaderboard/  wallet/
│   └── game/                   LAZY-LOADED — Three.js lives only here
│       ├── rendering/          scene · table · balls · cue · camera · lighting · environment
│       ├── input/  networking/  interpolation/  audio/  hud/  state/
│       └── game.component.ts
└── shared/
    └── ui/  utils/
```

Angular 22 standalone components, signals, zoneless. No NgModules, and no `components/`,
`services/`, `models/`, or `pages/` dumping grounds.

**Angular's change detection must never see a ball.** `three` is imported only inside
`features/game/rendering/**` and reached through a lazy route, so login, lobby, and leaderboard pages
never pay its bundle cost. The `requestAnimationFrame` loop runs entirely outside Angular and
mutates `THREE.Mesh.position` directly, touching no signal. HUD signals (turn, timer, score) update
at ~10 Hz, never at 60 Hz.

**One WebSocket connection, owned by `core/networking`** — lifecycle, reconnect with backoff,
sequence tracking, typed decoding. No component ever constructs a `WebSocket`.

Cue aiming is local and instant, but it is *intent* only. If client and server disagree about
anything, server state wins and the client snaps.

---

## 10. Security boundaries

Authentication and authorization are separate concerns. Being authenticated authorizes nothing by
itself.

```
Argon2id password  ──>  session row (token stored SHA-256 hashed)
                        cookie: HttpOnly; Secure; SameSite=Lax
                            │
              ┌─────────────┴─────────────┐
        HTTP middleware              WS upgrade
        cookie → session             cookie → session
                                     + strict Origin allowlist
                            │
                            ▼
              per-action authorization, in context:
              is this user in this match? on this side? is it their turn?
              is this their wallet?
```

Every shot is validated server-side: power ∈ [0, 12] m/s, tip offset within the ball, elevation
∈ [0°, 90°], and every float finite — `NaN` and `Inf` are rejected explicitly.

**Server authority is the anti-cheat mechanism.** No client claim about position, pocketing, score,
turn, or balance is ever believed. Rate limits apply to login, signup, room creation, matchmaking,
and shot submission. Password hashes, session tokens, stack traces, and DSNs are never logged or
returned.

---

## 11. Platform packages

Technical capability only. Zero business rules.

| Package | Contains | Never contains |
|---|---|---|
| `platform/config` | one `Config` struct, env-loaded, validated at startup, fail-fast | business defaults |
| `platform/postgres` | pool init, tx helper, health check | `CreateRoom()`, `SettleWager()` |
| `platform/websocket` | hub, connection, read/write pumps, limits | match logic |
| `platform/logging` | `log/slog` JSON handler, context-carried IDs | — |
| `platform/telemetry` | pprof (Phase 1), Prometheus (Phase 19) | — |
| `platform/security` | argon2id, CSRF, secure headers, rate limiter | authz policy |

`os.Getenv` appears in exactly one file. Logging is stdlib `log/slog` — no zap, no zerolog.
Prometheus is not added until Phase 19 has something worth measuring; `net/http/pprof` ships in
Phase 1 on an internal-only port because it is free and invaluable.

---

## Appendix — Phase 0 coverage map

Where each of the 25 items required by §78 is answered.

| # | Required item | Where |
|---|---|---|
| 1 | Repository structure | [MEMORY §4](../MEMORY.md) · §1 above · [ADR 0012](adr/0012-single-go-module.md) |
| 2 | Backend feature boundaries | [MEMORY §5](../MEMORY.md) · §2 above · [ADR 0001](adr/0001-feature-based-modular-monolith.md) |
| 3 | Angular feature boundaries | [MEMORY §18](../MEMORY.md) · §9 above |
| 4 | Go package dependency rules | [MEMORY §5](../MEMORY.md) · §2 above · [ADR 0001](adr/0001-feature-based-modular-monolith.md), [0012](adr/0012-single-go-module.md) |
| 5 | Game-engine boundaries | [MEMORY §6](../MEMORY.md) · §3 above · [ADR 0004](adr/0004-custom-billiards-physics.md) |
| 6 | HTTP vs WebSocket split | [MEMORY §11](../MEMORY.md) · §6 above |
| 7 | Authoritative match ownership | [MEMORY §12](../MEMORY.md) · §4 above · [ADR 0007](adr/0007-match-actor-ownership.md) |
| 8 | PostgreSQL ownership model | [MEMORY §15](../MEMORY.md) · §8 above |
| 9 | Initial database domain map | [MEMORY §15](../MEMORY.md) · §8 above |
| 10 | Wallet / wager architecture | [MEMORY §16](../MEMORY.md) · [ADR 0010](adr/0010-wallet-double-entry-ledger.md) |
| 11 | Realtime WebSocket topology | [MEMORY §12](../MEMORY.md) · §4 above · [protocol.md](protocol.md) |
| 12 | Match lifecycle state machine | [MEMORY §13](../MEMORY.md) · §7 above |
| 13 | 1v1 / 2v2 participant model | [MEMORY §14](../MEMORY.md) · §7 above |
| 14 | Coordinate-system strategy | [coordinates.md](coordinates.md) · [MEMORY §7–8](../MEMORY.md) · [ADR 0008](adr/0008-coordinate-system.md) |
| 15 | Three.js rendering architecture | [MEMORY §18](../MEMORY.md) · §9 above |
| 16 | AI-generated 3D asset pipeline | [coordinates.md §6](coordinates.md) · [MEMORY §19](../MEMORY.md) |
| 17 | Docker / Traefik topology | §1 above · [PLAN Phase 1](../PLAN.md) |
| 18 | Configuration strategy | [MEMORY §20](../MEMORY.md) · §11 above |
| 19 | Testing strategy | [MEMORY §21](../MEMORY.md) · per-phase checklists in [PLAN.md](../PLAN.md) |
| 20 | Benchmarking strategy | [MEMORY §21](../MEMORY.md) · [PLAN Phases 7, 9, 19](../PLAN.md) |
| 21 | Observability strategy | [MEMORY §20](../MEMORY.md) · §11 above · [PLAN Phase 19](../PLAN.md) |
| 22 | Security boundaries | [MEMORY §17](../MEMORY.md) · §10 above · [ADR 0009](adr/0009-opaque-session-cookies.md) |
| 23 | Recommended ADRs | [adr/](adr/) — twelve, all written |
| 24 | Major architectural risks | [MEMORY §24](../MEMORY.md) — R1–R8 |
| 25 | Decisions to finalize before Phase 1 | [MEMORY §25](../MEMORY.md) · [PLAN Phase 0](../PLAN.md) confirm block |

Deviations from the constitution are listed in [MEMORY §23](../MEMORY.md) and argued in
[ADR 0005](adr/0005-precomputed-shot-trajectory.md) — flagged deliberately, as §78 requires.
