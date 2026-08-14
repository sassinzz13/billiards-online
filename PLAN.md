# PLAN.md

The living roadmap. Twenty-one phases, each with a goal, deliverables, an exit criterion, and a
checklist.

**Tick a box only when the thing is implemented, tested, and passing.** A box ticked because the
file exists is worse than no box at all — it makes this document lie.

Companion files: [MEMORY.md](MEMORY.md) (conventions and constants), [CLAUDE.md](CLAUDE.md) (session
rules), [docs/adr/](docs/adr/) (decision rationale).

Last updated: 2026-08-14 (Phase 3 complete)

---

## Status

| Phase | Name | Status |
|---|---|---|
| 0 | Architecture | ✅ Complete |
| 1 | Development infrastructure | ✅ Complete |
| 2 | Authentication | ✅ Complete |
| 3 | Users | ✅ Complete |
| 4 | Lobby and rooms | 🔜 Next |
| 5 | WebSocket foundation | ⬜ |
| 6 | Match lifecycle | ⬜ |
| 7 | Physics prototype | ⬜ |
| 8 | 3D rendering prototype | ⬜ |
| 9 | Authoritative synchronization | ⬜ |
| 10 | Advanced billiards physics | ⬜ |
| 11 | 8-ball rules | ⬜ |
| 12 | Complete 1v1 | ⬜ |
| 13 | Reconnect and recovery | ⬜ |
| 14 | 2v2 | ⬜ |
| 15 | Leaderboards | ⬜ |
| 16 | Virtual wallet | ⬜ |
| 17 | Virtual wagering | ⬜ |
| 18 | Anti-cheat hardening | ⬜ |
| 19 | Performance | ⬜ |
| 20 | Production hardening | ⬜ |

**Phase 12 is the first milestone that is a game.** Everything before it is scaffolding toward a
playable 1v1 match.

---

## Rules for every phase

Work the twelve steps from §71: understand → architecture → feature impact → contracts → database →
protocol → implementation → testing → verification → performance → review → summary.

Non-negotiables that apply to all phases:

- Implement the **smallest complete vertical slice**. A phase that ships a working thin path beats a
  phase that ships three half-finished layers.
- **Stay inside the phase.** Do not implement future phases early, even when it seems cheap (§72).
- Respect the layer table in [MEMORY.md §5](MEMORY.md). `make arch` must pass.
- Write the tests in the same phase as the code. Not later.
- If a decision is made that a future session would not be able to re-derive, add it to
  MEMORY.md — and if it is significant, write an ADR.

---

## Phase 0 — Architecture ✅

**Goal:** a concrete technical blueprint, no application code.

**Exit criterion:** every one of the 25 items in §78 is answered somewhere in the docs, and every
deviation from the constitution is called out explicitly rather than applied silently.

- [x] Repository structure decided
- [x] Backend feature boundaries and layer table
- [x] Angular feature boundaries
- [x] Go package dependency rules
- [x] Game engine boundaries (physics / rules / state / simulation / protocol)
- [x] HTTP vs WebSocket responsibility split
- [x] Authoritative match ownership model (actor per match)
- [x] PostgreSQL ownership model
- [x] Initial database domain map
- [x] Wallet/wager architecture, conceptual level
- [x] Realtime WebSocket topology
- [x] Match lifecycle state machine
- [x] 1v1 / 2v2 participant model (sides)
- [x] Coordinate system strategy
- [x] Three.js rendering architecture
- [x] 3D asset pipeline and contract
- [x] Docker / Traefik topology
- [x] Configuration strategy
- [x] Testing strategy
- [x] Benchmarking strategy
- [x] Observability strategy
- [x] Security boundaries
- [x] ADRs 0001–0012 written
- [x] Architectural risks catalogued (R1–R8)
- [x] Decisions to finalize before Phase 1 listed
- [x] `MEMORY.md`, `PLAN.md`, `CLAUDE.md` created

**Confirm before starting Phase 1:**

- [ ] Go module path — **still assumed** as `github.com/sassinzz13/billiards-online`. Confirm or supply the real remote; changing it later is a repo-wide rewrite of import paths.
- [x] Dev hostnames — `billiards.localhost`; production `billiards-online.duckdns.org`. **Changed to path-based routing on one host** (MEMORY.md §10a).
- [x] `git init` — done, with `.gitignore`.

---

## Phase 1 — Development infrastructure

**Goal:** `make up` gives a running stack; `make test` passes; nothing does anything interesting yet.

**Exit criterion:** a fresh clone reaches a green health check through Traefik in one command.

**Backend**
- [x] `git init` + `.gitignore`
- [x] `go.mod` with the pinned versions from MEMORY.md §3
- [x] `apps/server/cmd/server/main.go` — composition root, Gin router, graceful shutdown
- [x] `platform/config` — one `Config` struct, env-loaded, **validated at startup, fail-fast**
- [x] `platform/logging` — `log/slog` JSON handler, context-carried IDs
- [x] `platform/postgres` — pgxpool init, tx helper, health check
- [x] `GET /health` (liveness) and `GET /ready` (DB reachable)
- [x] `net/http/pprof` on an internal-only port
- [x] Graceful shutdown: stop accepting, drain, close pool, bounded timeout (§62)

**Frontend**
- [x] Angular 22 workspace at `apps/web` — standalone, zoneless, signals
- [x] `core/config` + `core/networking/ApiClient` — API paths are **relative** (`/api/v1`), not
      configured, because path-based routing makes app and API same-origin (MEMORY.md §10a)
- [x] Placeholder shell route that calls `/health` and renders the result

**Infrastructure**
- [x] `migrations/` directory + golang-migrate wiring
- [x] `infra/docker/server.Dockerfile` — multi-stage, `golang:1.26` → `scratch`
- [x] `infra/docker/web.Dockerfile` — multi-stage, `node:26` → `nginx:alpine`
- [x] `infra/traefik/` — routers for web, API, and `/ws`
- [x] `docker-compose.yml` — traefik, web, server, postgres:18; healthchecks; named volume;
      `depends_on: service_healthy`
- [x] `.env.example` committed; **no secrets in images**
- [x] `Makefile` with the targets listed in MEMORY.md §26

**Guardrails**
- [x] `tests/arch/boundaries_test.go` — enforces the layer table via `go list -json ./...`
- [x] CI-equivalent make target running `test` + `arch` + `test-game`

---

## Phase 2 — Authentication

**Goal:** a user can sign up, sign in, and sign out; protected routes actually protect.

**Exit criterion:** an unauthenticated request to a protected endpoint is rejected, and a session can
be revoked server-side and immediately stops working.

**Owns:** `internal/auth` (L1) · tables `credentials`, `sessions`

Also created `users` (owned by `internal/users`, L0) — auth needs an account to attach a session
to, so the minimal vertical slice includes it. Phase 3 adds `player_profiles` and statistics.

**Credentials are a separate table from `users` on purpose:** the users feature owns identity,
auth owns the secret, so no query in `internal/users` can return a password hash even by mistake.

- [x] Migration: `sessions` (id UUIDv7, user_id, token_hash, expires_at, created_at, revoked_at)
- [x] `platform/security/argon2.go` — Argon2id `t=3, m=64MiB, p=4`, 16-byte salt
- [x] `POST /api/v1/auth/signup`
- [x] `POST /api/v1/auth/login` — sets `HttpOnly; Secure; SameSite=Lax` cookie
- [x] `POST /api/v1/auth/logout` — revokes the session row
- [x] `GET  /api/v1/auth/session` — current user
- [x] Auth middleware: cookie → SHA-256 → session lookup → bind userID to `context.Context`
- [x] Rate limiting on login and signup (§59)
- [x] Angular `features/auth` — signup and login forms (Signal Forms), `core/auth` service
- [x] `core/guards` — auth guard on protected routes
- [x] Tests: signup, duplicate email, login, wrong password, expired session, **revoked session**,
      rate limit trip
- [x] Verify: password hashes and tokens appear in **no** log line and **no** API response

---

## Phase 3 — Users

**Goal:** persistent profiles and the statistics shell that Phase 15 will fill.

**Exit criterion:** a signed-in user can view and edit their own profile and cannot edit anyone else's.

**Owns:** `internal/users` (L0) · tables `users`, `player_profiles`

- [x] Migration: `player_profiles` (display name, avatar ref, created_at) — `users` itself landed in Phase 2
- [x] Migration: statistics columns — matches played, wins, losses (structure only, not maintained yet)
- [x] `GET /api/v1/users/me`, `PATCH /api/v1/users/me`
- [x] `GET /api/v1/users/:id` — public projection only
- [x] Unique constraint on handle, case-insensitive
- [x] Angular `features/profile`
- [x] Tests: fetch, update, **authorization — user A cannot modify user B**, handle uniqueness

---

## Phase 4 — Lobby and rooms

**Goal:** players can create and join rooms and mark themselves ready. Still no match, no socket.

**Exit criterion:** concurrent joins on the last open seat produce exactly one winner — proven by a
concurrency test.

**Owns:** `internal/rooms` (L4), `internal/lobby` (L5) · tables `rooms`, `room_members`

- [ ] Migration: `rooms` (visibility, mode 1v1/2v2, ranked, ruleset, shot timer, wager amount,
      spectators allowed, state), `room_members` (side, slot, ready)
- [ ] `POST /api/v1/rooms`, `POST /api/v1/rooms/:id/join`, `POST /api/v1/rooms/:id/leave`
- [ ] `POST /api/v1/rooms/:id/ready`
- [ ] `GET /api/v1/rooms` — public discovery, **paginated**
- [ ] Private rooms via join code
- [ ] **Room join is a single transaction with row locking** — capacity is a DB constraint, not a
      Go `if` (§10, §37)
- [ ] Rate limit on room creation
- [ ] Angular `features/lobby`, `features/rooms`
- [ ] Tests: create, join, full room, private code, leave, **N concurrent joins on one seat**

---

## Phase 5 — WebSocket foundation

**Goal:** an authenticated, versioned, bounded socket. No gameplay yet.

**Exit criterion:** a deliberately slow client is closed with a policy code rather than being allowed
to grow a queue.

**Owns:** `platform/websocket`, `internal/realtime` (L6), `game/protocol`

- [ ] `platform/websocket` — connection, read pump, write pump, **32 KB read limit**,
      **bounded outbound chan (64)**
- [ ] Every goroutine has an owner and a cancellation context (§40)
- [ ] `internal/realtime/gateway` — **Origin allowlist**, session cookie auth, bind userID
- [ ] `internal/realtime/router` — decode envelope, dispatch by type
- [ ] `game/protocol` — envelope `{v, type, seq, requestId, matchId, ts, payload}`, JSON codec
- [ ] Server→client monotonic `seq` per connection
- [ ] Unknown message type → `error` envelope, never a silent drop
- [ ] **Backpressure: outbound queue full → close with policy code** (never drop, never drain)
- [ ] Angular `core/networking` — one `RealtimeService`, lifecycle, reconnect with backoff,
      sequence tracking, typed decode
- [ ] Tests: auth ok, auth rejected, **bad Origin rejected**, malformed frame, **oversized frame**,
      unknown type, disconnect, **slow client → backpressure close**

---

## Phase 6 — Match lifecycle

**Goal:** matches exist as first-class entities with sides and an enforced state machine. Still no balls.

**Exit criterion:** every illegal state transition is rejected by one function, and the sides model
already supports 2v2 without any code change.

**Owns:** `internal/matches` (L3) · tables `matches`, `match_sides`, `match_participants`, `match_events`

- [ ] Migration: `matches` (state, mode, ranked, ruleset, room_id, started_at, completed_at),
      `match_sides`, `match_participants`, `match_events`
- [ ] `matches.Transition(from, to) error` — **the only place transitions are validated** (§20)
- [ ] `Side{ID, Players []uuid.UUID}` / `TurnRef{Side, PlayerIdx}` — **sides, never player1/player2**
- [ ] Room → match creation, in one transaction
- [ ] Match actor: one goroutine per match, bounded inbound chan (32), owns state exclusively
- [ ] Match registry `map[uuid.UUID]*actor` behind `sync.RWMutex`
- [ ] Actor lifecycle: created on start, cancelled on completion, **no leaks** (verified by test)
- [ ] Turn ownership + shot timer via `time.Timer`
- [ ] Protocol: `match.starting`, `match.started`, `turn.started`
- [ ] `GET /api/v1/matches/:id`, `GET /api/v1/users/:id/matches` (paginated)
- [ ] Tests: legal transitions, **every illegal transition rejected**, sides with 1 and 2 players,
      turn advance, actor shutdown leaves no goroutine

---

## Phase 7 — Physics prototype

**Goal:** a real, isolated, deterministic billiards simulation. No spin yet.

**Exit criterion:** `go test ./game/...` passes with no Postgres, no Docker, and no network — and the
hot loop reports `0 allocs/op`.

**Owns:** `game/physics`, `game/state`, `game/simulation`

- [ ] `game/physics/vec.go` — `Vec2`, value semantics
- [ ] `game/physics/table.go` — **all constants from MEMORY.md §8, single source of truth**
- [ ] `game/physics/ball.go` — flat `Ball` struct, `[16]Ball` array, no pointers
- [ ] Fixed timestep `dt = 1/480 s` integrator
- [ ] Cloth friction and rolling resistance
- [ ] **Swept (quadratic TOI)** ball–ball collision — not discrete overlap
- [ ] Cushion collision with restitution
- [ ] Pocket geometry and capture
- [ ] Stop thresholds; every shot provably terminates
- [ ] `physics.Event` — `BallHitBall`, `BallHitCushion`, `BallHitRail`, `BallPocketed`, `AllStopped`
- [ ] `simulation.ResolveShot(state, shot) → ShotResult` — the one authoritative entry point
- [ ] Keyframe sampling at 60 Hz (every 8th step)
- [ ] Tests: stationary balls stay stationary · straight collision · angled collision · cushion
      reflection angle · pocket capture · friction decay · **energy never increases** ·
      **momentum conserved in elastic pairs** · **balls never leave the table** · every shot terminates
- [ ] Determinism test: identical input → byte-identical output
- [ ] Benchmarks: single tick, full shot, keyframe generation
- [ ] **`0 allocs/op` asserted as a test**
- [ ] `docs/benchmarks.md` created with the machine recorded

---

## Phase 8 — 3D rendering prototype

**Goal:** a table you can look at. Placeholder geometry is fine and expected.

**Exit criterion:** the main bundle does not contain Three.js — verified against the build stats.

**Owns:** `apps/web/src/app/features/game/rendering/**`

- [ ] Lazy route for `features/game` — **Three.js reachable only from here**
- [ ] `rendering/scene` — renderer, resize handling, correct disposal on destroy
- [ ] `rendering/table` — procedural placeholder at exact MEMORY.md §8 dimensions
- [ ] `rendering/balls` — 16 spheres at radius 0.028575, correct rack
- [ ] `rendering/cue` — placeholder cylinder
- [ ] `rendering/camera` — orbit + aim modes
- [ ] `rendering/lighting`, `rendering/environment`
- [ ] rAF loop **outside Angular, touching no signal**
- [ ] `features/game/hud` — Angular, signals at ~10 Hz only
- [ ] Verify: **main bundle excludes `three`** (assert in CI)
- [ ] Verify: no WebGL context leak across route changes

---

## Phase 9 — Authoritative synchronization

**Goal:** close the loop. Angular input → WebSocket → Go simulation → trajectory → Three.js playback.

**Exit criterion:** a shot taken in the browser is simulated on the server and played back
identically on both clients, and a forged shot from a player whose turn it is not is rejected.

- [ ] Protocol: `shot.request` (direction, power, tipOffsetX, tipOffsetY, cueElevation)
- [ ] Protocol: `shot.accepted`, `shot.rejected`, `shot.result` (events, keyframes, finalState, nextTurn)
- [ ] **Server-side validation:** turn ownership · power ∈ [0,12] · tip offset within ball ·
      elevation ∈ [0°,90°] · **every float finite, `NaN`/`Inf` explicitly rejected**
- [ ] Match actor: validate → `ResolveShot` → broadcast → persist `match_events` **off the sim path**
- [ ] `match_events` row: shot params + rule events + **final-state checkpoint (JSONB)** + `simulation_version`
- [ ] Rate limit on shot submission
- [ ] Client: `features/game/interpolation` — trajectory playback at 60 fps
- [ ] Client: snap to authoritative `finalState` on playback end
- [ ] Client: local cue aiming as *intent only*
- [ ] Client: visual ball roll derived from displacement (rendering only)
- [ ] **Measure JSON payload size, encode time, decode time** → record in `docs/benchmarks.md`
- [ ] **Decide** on the binary codec from that measurement — build it only if justified (§25)
- [ ] Tests: valid shot end-to-end · **shot from the wrong player rejected** · out-of-range params ·
      `NaN`/`Inf` params · replayed `requestId` is idempotent

---

## Phase 10 — Advanced billiards physics

**Goal:** shots feel real. Spin is the difference between a toy and a billiards game.

**Exit criterion:** draw, follow, and English produce visibly and measurably correct cue-ball
behaviour, validated against published reference shots rather than intuition.

- [ ] Angular velocity `ω` on `Ball`
- [ ] Cue impulse model: tip offset → linear + angular velocity
- [ ] Sliding phase and the **sliding → rolling transition**
- [ ] Topspin (follow), backspin (draw), side spin (English)
- [ ] Spin transfer on ball–ball collision (throw)
- [ ] Improved cushion response — **Han (2005) with Kiefl's corrections**
- [ ] Spinning (drill) friction decay
- [ ] Tests: draw pulls back · follow pushes through · English changes cushion rebound angle ·
      throw deflects the object ball · stun shot leaves the cue ball dead
- [ ] Regression: **all Phase 7 invariants still hold**
- [ ] Re-benchmark; confirm `0 allocs/op` survived

---

## Phase 11 — 8-ball rules

**Goal:** a complete, isolated 8-ball rule engine.

**Exit criterion:** `game/rules` decides every outcome from `[]physics.Event` alone, and contains no
physics maths whatsoever.

**Owns:** `game/rules`

- [ ] Break rules and legal break
- [ ] Open table, group assignment (solids/stripes)
- [ ] Legal shot: correct first contact, ball pocketed or a rail after contact
- [ ] Fouls: wrong first contact, no rail, no contact, cue ball off table
- [ ] Scratch (cue ball pocketed) and ball-in-hand
- [ ] 8-ball early = loss; 8-ball on a called pocket = win; wrong pocket = loss
- [ ] Turn continues / turn changes
- [ ] Win conditions
- [ ] **Zero physics maths in `game/rules`** — verified by review and by the arch test
- [ ] Tests: exhaustive table-driven — legal, each foul type, scratch, break variants, 8-ball
      early/late/wrong-pocket, turn transitions

---

## Phase 12 — Complete 1v1 🎯

**Goal:** a real, playable, finishable 1v1 match. **This is the first phase that is a game.**

**Exit criterion:** two browsers play a full match from lobby to result screen without a manual step.

- [ ] Lobby → room → match → play → result, end to end
- [ ] Shot clock enforced, with a visible HUD countdown
- [ ] Ball-in-hand placement UI, validated server-side
- [ ] Match completion persisted; result screen
- [ ] Match history shows the finished match
- [ ] `match.completed`, `foul`, `turn.completed` protocol messages
- [ ] Playwright e2e: two browser contexts play a full match
- [ ] Manual playtest; feel notes recorded in `docs/`

---

## Phase 13 — Reconnect and recovery

**Goal:** losing your connection mid-match is an inconvenience, not a loss.

**Exit criterion:** killing a client mid-shot and reconnecting restores the exact authoritative state
plus the correct playback offset.

- [ ] Client reconnect with backoff and the last-seen `seq`
- [ ] `state.snapshot` — full authoritative resync (balls, turn, scores, rule state, timer, participants)
- [ ] **Mid-shot rejoin with a playback seek offset**
- [ ] Grace period on disconnect; match pauses rather than ending
- [ ] `player.disconnected` / `player.reconnected`
- [ ] **Networking reports the disconnect; `game/rules` decides what it means** (§61)
- [ ] Forfeit on permanent disconnect
- [ ] Tests: reconnect between shots · **reconnect mid-shot** · grace expiry → forfeit ·
      duplicate connection for one user

---

## Phase 14 — 2v2

**Goal:** team play. Should be small, because the sides model landed in Phase 6.

**Exit criterion:** 2v2 required no change outside `game/rules` and the room configuration.

- [ ] Room config for 2v2, side and slot assignment
- [ ] Team turn ordering in **`game/rules` only** (A1 → B1 → A2 → B2)
- [ ] Team-aware win conditions
- [ ] Teammate disconnect policy
- [ ] HUD shows sides and teammates
- [ ] Tests: turn rotation, team win, teammate disconnect
- [ ] **Verify: no change was needed outside `game/rules` and room config** — if there was, the
      Phase 6 model was wrong; record why in MEMORY.md

---

## Phase 15 — Leaderboards

**Goal:** rankings that do not scan history on every request.

**Exit criterion:** the leaderboard query has an `EXPLAIN ANALYZE` plan using an index, recorded in
the docs.

**Owns:** `internal/leaderboards` (L1) · tables `player_ratings`, `player_statistics`

- [ ] **Decide the rating algorithm** (Glicko-2 vs Elo) → write an ADR
- [ ] Migration: `player_ratings`, `player_statistics`
- [ ] Statistics maintained **incrementally on match completion**, in the same transaction
- [ ] `GET /api/v1/leaderboards` — **keyset pagination** (§55)
- [ ] Composite index matching the actual query; **no leaderboard query scans match history** (§38)
- [ ] `EXPLAIN ANALYZE` inspected and recorded in `docs/benchmarks.md`
- [ ] Angular `features/leaderboard`
- [ ] **No Redis** (§39)
- [ ] Tests: rating update, win/loss counting, pagination boundaries, ranked vs casual separation

---

## Phase 16 — Virtual wallet

**Goal:** an immutable double-entry ledger. Virtual money, real accounting discipline.

**Exit criterion:** a test asserts every ledger transaction sums to exactly zero, and the DB refuses
one that does not.

**Owns:** `internal/wallet` (L0) · tables `wallets`, `ledger_accounts`, `ledger_transactions`, `ledger_entries`

- [ ] Migration: the four tables. **Amounts `BIGINT` minor units — no floats** (§32)
- [ ] **DB constraint: entries per transaction sum to zero** (deferrable + trigger)
- [ ] Append-only: **no `UPDATE`, no `DELETE` on ledger rows, ever**
- [ ] `Credit` / `Debit` — provider-agnostic, the seam for future payments (§34)
- [ ] Materialised balance on `wallets`, reconciled against entry sums by test
- [ ] `GET /api/v1/wallet`, `GET /api/v1/wallet/transactions` (paginated)
- [ ] Angular `features/wallet`
- [ ] Tests: credit, debit, **insufficient funds**, **sum-to-zero invariant**,
      **balance reconciliation**, concurrent credits

---

## Phase 17 — Virtual wagering

**Goal:** stake, escrow, settle, refund — and never pay twice.

**Exit criterion:** N concurrent settlements of the same wager produce exactly one payout. Proven by
a test, not by reasoning.

**Owns:** `internal/wagering` (L2) · tables `wagers`, `wager_holds`, `wager_settlements`

- [ ] Migration: the three tables + **`UNIQUE (wager_id, idempotency_key)`**
- [ ] Stake reservation → escrow account
- [ ] Settlement on match completion, in one transaction with `SELECT ... FOR UPDATE`
- [ ] Refund on cancellation/abandonment
- [ ] `matches` calls wagering through a **consumer-side interface** — wagering never imports matches
- [ ] Tests: reserve, settle, refund, insufficient funds at reserve,
      **duplicate settlement pays once**, **N concurrent settlements pay once**,
      ledger balances after every path

---

## Phase 18 — Anti-cheat hardening

**Goal:** audit the authoritative boundary. Find the places the server trusts the client.

**Exit criterion:** a written audit lists every client input and where it is validated.

- [ ] Audit every client→server message for server-side validation
- [ ] Confirm **no** client claim about position, pocketing, score, turn, or balance is ever believed
- [ ] Rate limits reviewed on every realtime action (§59)
- [ ] Impossible-input detection and logging
- [ ] Authorization re-checked per action, not per connection
- [ ] `docs/security-audit.md` written
- [ ] **No invasive client-side anti-cheat** (§43)

---

## Phase 19 — Performance

**Goal:** measure, then optimize the things measurement actually found.

**Exit criterion:** every optimization in this phase cites a before/after number.

- [ ] CPU profile under load
- [ ] Heap and allocation profile
- [ ] Goroutine and mutex contention profile
- [ ] WebSocket load test — many concurrent connections
- [ ] Physics load test — many concurrent shot resolutions
- [ ] `EXPLAIN ANALYZE` on every important query
- [ ] Index review: which query, what cardinality, what write cost (§54)
- [ ] Prometheus metrics + `/metrics` (§47)
- [ ] **Optimize only what profiling identified** (§50)
- [ ] `docs/benchmarks.md` updated with before/after for each change

---

## Phase 20 — Production hardening

**Goal:** deployable.

**Exit criterion:** a deploy does not corrupt an in-progress match.

- [ ] Production Dockerfiles and compose overlay
- [ ] Traefik TLS via Let's Encrypt — **no application change required**
- [ ] Graceful shutdown: stop accepting → close listeners → terminate sockets deliberately →
      stop match loops safely → flush → close pool (§62)
- [ ] **Verify a deploy does not corrupt an in-progress match**
- [ ] Structured logs shipped somewhere useful
- [ ] Alerting on the metrics from Phase 19
- [ ] Security review: headers, cookies, rate limits, secrets, dependency audit
- [ ] Backup and restore procedure, **tested by actually restoring**
- [ ] `docs/deployment.md`

---

## Future — payment providers

**Only after an explicit request.** Not planned, not scaffolded, not designed beyond the seam that
already exists (§34): `internal/wallet` exposes `Credit`/`Debit` and knows nothing about where money
came from. A future `payments/` feature calls those. The game never learns a provider exists.

---

## Deliberately not doing

Recorded so nobody re-litigates these, and so the reasons stay attached:

| Not doing | Why |
|---|---|
| Redis | PostgreSQL + process memory is sufficient. Add only on a measured need (§39). |
| Microservices | Modular monolith with clean seams. Extract only if scaling demands it (§8). |
| An ORM | Explicit SQL stays visible (§10). |
| sqlc | pgx + `CollectRows` is enough; codegen would fight feature ownership (ADR 0002). |
| A shared `utils/` package | Small duplication beats the wrong abstraction (§6). |
| Event bus / DI container | Plain Go function calls; boundaries come from packages (§5). |
| A general 3D rigid-body engine on the server | Billiards-specific maths is faster and more correct (§15). |
| Client-side authority of any kind | The server decides everything (§13). |
| Tournaments, cosmetics, clans, achievements, replays UI | Architecture leaves room; do not build early (§1). |
