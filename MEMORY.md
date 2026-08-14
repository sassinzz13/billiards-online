# MEMORY.md

Durable facts about this project. **Read this before writing any code.**

This file holds things that are expensive to re-derive and easy to get subtly wrong: conventions,
physical constants, dependency rules, and the log of decisions already made. If you discover
something surprising, add it here. If a decision changes, update the entry and note the date —
do not leave two contradictory statements in this file.

Companion files: [PLAN.md](PLAN.md) (roadmap + checklist), [CLAUDE.md](CLAUDE.md) (session rules),
[docs/adr/](docs/adr/) (full rationale for each decision below).

Last updated: 2026-08-14 (Phase 0)

---

## 1. What this is

A browser-based realtime multiplayer 3D billiards platform. Angular 22 + Three.js front end, Go
backend, PostgreSQL, all behind Traefik in Docker. Server-authoritative gameplay with a custom
billiards physics engine. Virtual currency only — no real money anywhere in the system.

The governing specification is the engineering constitution supplied by the project owner. Its
section numbers (§1–§78) are cited throughout these docs. When this file and the constitution
disagree, the constitution wins and this file is wrong — fix it.

---

## 2. Toolchain (verified 2026-08-14 on this machine)

| Tool | Version |
|---|---|
| Go | 1.26.5 darwin/arm64 |
| Node | 26.5.0 |
| npm | 11.17.0 |
| Angular CLI | 22.1.3 |
| Docker | 29.6.2 |
| Docker Compose | v5.3.1 |

## 3. Pinned dependency versions

Chosen in Phase 0 against the live registries. Do not bump casually; note the reason if you do.

| Dependency | Version | Note |
|---|---|---|
| `github.com/gin-gonic/gin` | v1.12.0 | Mandated by constitution §3 |
| `github.com/jackc/pgx/v5` | v5.10.0 | Requires Go ≥1.25, PostgreSQL ≥14 |
| `github.com/coder/websocket` | v1.8.15 | **`gorilla/websocket` is archived** — do not use it |
| `github.com/golang-migrate/migrate/v4` | v4.19.1 | With the `database/pgx/v5` driver |
| `github.com/google/uuid` | v1.6.0 | `NewV7()` lives here |
| `golang.org/x/crypto` | v0.55.0 | `argon2` |
| `three` | 0.185.1 | `@types/three` 0.185.4 |
| `@angular/*` | 22.1.2 | |
| Traefik | v3.7 | |
| PostgreSQL | 18-alpine | |

**No ORM. Ever.** No GORM, no Ent-as-ORM, no generic repository framework. **No sqlc either** — we
chose plain pgx (ADR 0002). SQL lives as Go string consts inside each feature's `repository.go`.

---

## 4. Repository map

Single Go module, single Angular workspace. Module path: `github.com/sassinzz13/billiards-online`.

```
billiard/
├── CLAUDE.md PLAN.md MEMORY.md README.md Makefile
├── go.mod docker-compose.yml .env.example
├── apps/
│   ├── server/cmd/server/main.go   composition root — the ONLY place features are wired together
│   └── web/                        Angular 22 workspace
├── internal/                       product features (auth users lobby rooms matchmaking
│                                   matches leaderboards wallet wagering realtime)
├── game/                           the billiards engine (physics rules state simulation protocol)
├── platform/                       technical capability (postgres websocket config logging
│                                   telemetry security)
├── migrations/                     000001_x.up.sql / 000001_x.down.sql — ONE global sequence
├── docs/adr/                       architecture decision records
├── infra/{docker,traefik}/
├── scripts/
└── tests/arch/                     import-boundary enforcement test
```

Create directories only when a phase actually needs them (§74). Empty scaffolding is noise.

---

## 5. Dependency rules — THE most important rule in the repo

Features are strictly layered. **Imports may only point downward.** This is what makes circular
feature dependencies structurally impossible rather than merely discouraged (§5).

| Layer | Features | May import |
|---|---|---|
| L6 | `realtime` | L0–L5, `game/*`, `platform/*` |
| L5 | `lobby`, `matchmaking` | L0–L4, `platform/*` |
| L4 | `rooms` | L0–L3, `platform/*` |
| L3 | `matches` | L0–L2, `game/*`, `platform/*` |
| L2 | `wagering` | L0–L1, `platform/*` |
| L1 | `auth`, `leaderboards` | L0, `platform/*` |
| L0 | `users`, `wallet` | `platform/*` only |

**Same-layer imports are forbidden too**, not just upward ones. Two features on one layer are
declared independent, so an import between them is either a mistake or a sign one belongs higher.
(`lobby` moved from L4 to L5 in Phase 1 for exactly this reason: it lists rooms, so it sits above
`rooms` rather than beside it.)

Additional hard rules:

- `game/**` imports **stdlib only**. No Gin, no pgx, no `internal/`, no `platform/`.
- `platform/**` never imports `internal/**` or `game/**`, and contains **zero business rules**.
  `platform/postgres` may hold pool setup and a tx helper; it may never hold `SettleWager()`.
- Nothing imports `apps/server/**`.
- **There is no `shared/`, `common/`, or `utils/` package** (§6). If two features need the same 20
  lines, they each get 20 lines. Wait for the third occurrence before abstracting.

Enforced by `tests/arch/boundaries_test.go`, which reads `go list -json ./...` and fails on violation.

**A feature that needs to know "which user is making this request" but cannot import the feature
that authenticates them defines its own minimal context carrier** (an unexported key type + small
WithX/XFromContext pair) rather than importing upward. `internal/users` (L0) cannot import
`internal/auth` (L1), so it owns `WithUserID`/`UserIDFromContext` in `context.go`; the composition
root bridges `auth.Identity` into that shape with a small adapter middleware. See ADR 0001 and
`internal/users/context.go`.

### How features talk to each other

Consumer-side interfaces. The consumer declares the narrow interface it needs; the provider is a
plain struct that happens to satisfy it; `main.go` wires them. No shared contracts package, no event
bus, no DI container.

```go
// internal/matches/service.go — matches declares what IT needs from wagering
type WagerSettler interface {
    Settle(ctx context.Context, matchID uuid.UUID, winner SideID, idempotencyKey string) error
}

type Service struct {
    db     *pgxpool.Pool
    wagers WagerSettler
}
```

`internal/wagering` never imports `internal/matches`. That is the whole trick.

---

## 6. Game engine internal structure

```
protocol ──> state ──> rules ──> physics ──> (stdlib only)
                └───────────────────┘
simulation ──> state, rules, physics
```

| Package | Owns | Must never know about |
|---|---|---|
| `game/physics` | `Vec2`, `Ball`, `Table`, integrator, collisions, `Event` | rules, Gin, pgx, WebSockets |
| `game/rules` | 8-ball state machine, consumes `[]physics.Event` | how a collision was computed |
| `game/state` | `MatchState` — balls + rules state + turn + sides + scores | transport, persistence |
| `game/simulation` | `ResolveShot(state, shot) → ShotResult`, the one authoritative entry point | Gin, pgx |
| `game/protocol` | wire envelope, codecs, versioning | Gin, pgx |

**Physics and rules never mix (§17).** Physics emits facts: `BallHitBall`, `BallHitCushion`,
`BallPocketed`, `BallHitRail`, `AllStopped`. Rules alone decide legal / foul / scratch / turn
continues / win. There must never be an `if isEightBall` inside collision detection.

`go test ./game/...` must pass with no Postgres, no Docker, and no network. This is a CI gate.

---

## 7. Coordinate system — get this right once

**Y-up, right-handed, metres. Origin at table centre, on the cloth plane.**

- Long axis = **X**. Short axis = **Z**. Up = **Y**.
- Ball centres rest at `y = R`.
- Chosen because it is Three.js's native convention *and* glTF's, so a server position maps to a
  scene position with **zero conversion**. Z-up was rejected: it needs a conversion at both the
  renderer and the asset boundary, and every such conversion is a future sign-error bug.
- Server physics is **2D** — `Vec2{X, Z}`. `y` exists only for rendering and future jump shots.

## 8. Table and ball constants (WPA 9-foot)

Single source of truth in code: `game/physics/table.go`. This table is the reference for that file.

| Quantity | Value |
|---|---|
| Playing surface | 2.540 m × 1.270 m (100″ × 50″) |
| Playing surface extent | x ∈ [−1.270, +1.270], z ∈ [−0.635, +0.635] |
| Ball radius `R` | 0.028575 m (2.25″ diameter) |
| Ball mass | 0.170 kg |
| Cushion nose height | 1.27 R (63.5% of ball diameter) |
| Sliding friction μ_s | ≈ 0.2 |
| Rolling friction μ_r | ≈ 0.01 |
| Spinning friction μ_sp | ≈ 0.044 · R |
| Ball–ball restitution | ≈ 0.95 |
| Ball–cushion restitution | ≈ 0.85 |
| Corner pocket mouth | 0.114 m (4.5″) |
| Side pocket mouth | 0.127 m (5″) |
| Gravity `g` | 9.8 m/s² |
| Head string | x = −0.635 |
| Foot spot (rack apex) | x = +0.635 |
| Max legal cue-ball speed | 12 m/s |
| Stop threshold | \|v\| < 1e-3 m/s **and** \|ω\| < 1e-2 rad/s |

**Fixed timestep: `dt = 1/480 s`.** Never coupled to frame rate, refresh rate, or packet rate (§15).

Why 1/480 and not 1/240: at the 12 m/s cap a ball advances 25 mm per step against a 57.15 mm
diameter. At 1/240 it advances 50 mm — close enough to the diameter that tunnelling becomes likely.
Swept (quadratic time-of-impact) ball–ball and ball–cushion tests are used *in addition*, not
instead.

O(n²) broadphase is correct here. 16 balls = 120 pairs; a 4-second shot is ~1920 steps ≈ 230k pair
tests, comfortably sub-millisecond. **Do not add a spatial hash.** It would be slower.

Physics reference material: Han (2005) cushion model with Evan Kiefl's published corrections,
cross-checked against Dr. Dave's physics resources and the `pooltool` implementation.

---

## 9. Shot transport — the load-bearing design choice

Billiards is turn-based: once the cue strikes, there is no further input until every ball stops. So
the server resolves the **entire shot in one synchronous call** and ships the whole trajectory in
one message (ADR 0005).

```
client --shot.request-->  server
                          validate turn ownership + parameter ranges
                          simulation.ResolveShot()   ~1 ms CPU for ~4 s of table time
                          rules.Apply(events)
                          persist match_event (async, OFF the simulation path)
       <--shot.result---  { events[], keyframes[], finalState, nextTurn }
client: play back at 60 fps, then snap to the authoritative finalState
```

Consequences worth remembering:

- One message per shot instead of 100+.
- The match goroutine is **idle** between shots — no per-match ticker exists anywhere.
- Reconnect resyncs by sending current state plus a playback offset.
- Replay is `(initialState, shotParams)` → deterministic re-run.

This is not a departure from §67: still fixed-timestep authoritative simulation, still snapshots
over WebSocket, still client interpolation. The snapshots just all arrive up front.

**Encoding:** JSON first, measure, then decide (§25). Sizing already done — a 6-ball shot at 60 Hz
over 4 s is ~5.7 KB as `int16` positions quantised to 0.1 mm (table max 25 400 units < 32 767).
The envelope is designed to carry either a JSON payload or a binary frame, but **the binary codec is
not built until a measurement justifies it.**

Client derives visual ball roll from displacement. That is rendering only and never affects gameplay.

---

## 10. Protocol envelope

```jsonc
{ "v": 1, "type": "shot.request", "seq": 123, "requestId": "...", "matchId": "...", "ts": 0, "payload": {} }
```

- Versioned from message #1.
- Server→client `seq` is monotonic per connection, so a reconnecting client can say "I last saw 412".
- Client→server `requestId` makes shot submission idempotent under retry.
- Unknown `type` → `error` envelope. **Never a silent drop.**

Full message catalogue: [docs/protocol.md](docs/protocol.md).

## 10a. Routing — one host, path-based

**Decided in Phase 1, refining the Phase 0 plan.** Traefik routes a single host by path rather than
splitting the API onto a subdomain:

```
${PUBLIC_HOST}/          -> web     (Angular, nginx)
${PUBLIC_HOST}/api/...   -> server
${PUBLIC_HOST}/ws        -> server  (from Phase 5)
```

| | |
|---|---|
| Development | `billiards.localhost` (plain HTTP; `*.localhost` resolves to 127.0.0.1) |
| Production | `billiards-online.duckdns.org` (HTTPS via Let's Encrypt) |

**Why this matters beyond tidiness.** Phase 0 assumed `billiards.localhost` + `api.billiards.localhost`,
which are *different origins*. The auth design in ADR 0009 rests on the cookie being same-origin.
Subdomains would have forced a `Domain=.billiards...` cookie (widening its scope), weakened
`SameSite=Lax`, and required CORS configuration. One host removes all three problems at once, and
also means the client needs no configured API host — every call is relative, so the same container
image runs in development and production unchanged.

Traefik router priorities do the work: the API router matches `PathPrefix(/api) || PathPrefix(/ws)`
at priority 10; the web catch-all matches `Host(...)` at priority 1.

**Root `/health` and `/ready` are deliberately not routed publicly.** They exist for Docker and the
orchestrator, reachable only inside the compose network. Publicly, `/ready` falls through to the
Angular SPA, so database state is never exposed (§42). The public probe is `/api/v1/health`.

## 11. HTTP vs WebSocket

**Anything that changes live match state goes over WebSocket. Everything else is REST** (§52).

REST (`/api/v1/...`): signup, login, logout, session, profile, statistics, leaderboards, match
history, room discovery, room creation, wallet balance and ledger history, health.

WebSocket (`/ws`): auth handshake, room joined/updated/ready, match starting/started, turn started,
shot request/accepted/rejected, shot result, state snapshot/resync, foul, turn completed, match
completed, player disconnected/reconnected.

---

## 12. Match ownership and concurrency

**One goroutine owns one match.** State is never shared, only messaged (§40).

```
Browser ══wss══> Traefik ══> Go server /ws
   platform/websocket          read/write pumps · read limit 32 KB · bounded outbound chan (64)
   internal/realtime/gateway   Origin allowlist → session cookie → bind userID
   internal/realtime/router    decode envelope → dispatch by type
        └──> match actor       owns MatchState exclusively — NO mutex on game state
                               bounded inbound command chan (32) · shot timer via time.Timer
```

Every long-lived goroutine has an owner, a `context.Context` for cancellation, and a bounded queue.
No unbounded channels anywhere.

**Backpressure policy:** if a client's outbound queue is full, **close the connection with a policy
code**. Do not drain, do not drop. Shot results are large and non-idempotent, so dropping one
desyncs the client; close-and-resync is the only correct behaviour.

**Single-instance assumption:** through Phase 12 one server process owns all matches. Multi-instance
would need sticky routing or a match locator. Recorded as risk R2, deliberately deferred.

**Redis is not used** (§39). Do not add it without a measurement that demands it.

## 13. Match lifecycle

```
Waiting ──> Starting ──> InProgress ──> Completed
                │             ├──> Paused ──> InProgress
                └─────────────┴──> Cancelled | Abandoned
```

Transitions live in exactly one function, `matches.Transition(from, to) error`. Illegal transitions
are rejected there and nowhere else (§20). Terminal states are terminal.

## 14. Participants — sides, never "player1/player2"

```go
type Side    struct { ID SideID; Players []uuid.UUID }  // len 1 = 1v1, len 2 = 2v2
type TurnRef struct { Side SideID; PlayerIdx int }
type Match   struct { Sides [2]Side; Turn TurnRef }
```

1v1 and 2v2 differ only in `len(Players)` and the rules layer's turn-advance function. **Nothing
outside `game/rules` may compute whose turn it is.** Modelled in Phase 6, exercised in Phase 14, so
2v2 needs no rewrite.

**Rooms are not matches** (§21). A room is players preparing to play; it holds config and ready
flags, creates a match, and then has no authority over it.

---

## 15. Database conventions

One database, one schema, but **every table has exactly one owning feature**. A feature never writes
another feature's tables (§35) — it calls that feature's service.

| Owner | Tables |
|---|---|
| `users` | `users`, `player_profiles` |
| `auth` | `sessions` |
| `rooms` | `rooms`, `room_members` |
| `matches` | `matches`, `match_sides`, `match_participants`, `match_events` |
| `leaderboards` | `player_ratings`, `player_statistics` |
| `wallet` | `wallets`, `ledger_accounts`, `ledger_transactions`, `ledger_entries` |
| `wagering` | `wagers`, `wager_holds`, `wager_settlements` |

Conventions:

- **IDs are UUIDv7** (`uuid.NewV7()`, stored as native `uuid`). Time-ordered, so B-tree inserts stay
  local and `ORDER BY id` is chronological, while staying non-enumerable in URLs (§57).
  Sole exception: `ledger_entries` uses `bigserial` — never exposed, benefits from tight locality.
- **All timestamps `timestamptz`, always UTC** (§56).
- **Money is `BIGINT` minor units. No floats touch money, ever.**
- Migrations: one global sequence in `migrations/`, `000001_x.up.sql` / `.down.sql`. Never
  auto-migrate. Every migration has a working `down`.
- Transactions are short and explicit (§37). **Never hold one across a match, a shot, or a network
  round trip.**
- No `SELECT *` in a hot path. No unbounded query — everything user-facing is paginated, keyset
  where the dataset grows (§55).

**Live match state never touches Postgres** (§19). Ball positions live only in the match actor's
memory. Persisted per shot: one `match_events` row (shot params + rule events + final-state
checkpoint as JSONB), written *after* the shot resolves, off the simulation path. **No physics ticks
are ever stored.**

## 16. Wallet invariants (Phases 16–17)

Virtual currency, accounted like real money. **Never a mutable `users.balance`** (§31).

- Immutable double-entry ledger. Every `ledger_transaction` has ≥2 `ledger_entries` summing to
  exactly zero, enforced by a DB constraint, not just application code.
- History is append-only. **No `UPDATE`, no `DELETE` on ledger rows, ever.**
- Settlement is idempotent: `UNIQUE (wager_id, idempotency_key)`. A retry hits the constraint and
  returns the original result. It can never pay twice (§33, §58).
- Concurrency: `SELECT ... FOR UPDATE` on the wager row.

```
Reserve:  player wallet −100, wager escrow +100
Settle:   wager escrow  −200, winner wallet +200
Refund:   wager escrow  −100, player wallet +100
```

Payment providers are **not built** (§34). The seam is that `internal/wallet` exposes `Credit`/`Debit`
and knows nothing about where money came from. The game never learns a provider exists.

---

## 17. Security constants

- Passwords: **Argon2id**, `t=3, m=64 MiB, p=4`, 16-byte salt, stored as a PHC string so the
  parameters can be raised later without invalidating existing passwords. 10–1024 characters.
  - The 64 MiB is why login and signup **must** stay rate limited: each attempt allocates it, so
    unbounded concurrency is a memory-exhaustion vector independent of any password guessing.
  - Login runs `security.DummyVerify` when the email is unknown, so a missing account takes the
    same ~50ms as a real one. Without it, response time enumerates registered addresses.
- **Credentials live in an auth-owned `credentials` table, not on `users`.** users owns identity,
  auth owns the secret, so no query in `internal/users` can return a password hash by mistake.
- Session TTL 14 days, sliding: renewed on use when under 7 days remain, so an active player is
  never signed out mid-session while an inactive one still expires.
- Sessions: 32-byte random token, stored **SHA-256 hashed** in `auth.sessions`. Cookie is
  `HttpOnly; Secure; SameSite=Lax`.
- WebSocket upgrade reuses that cookie, guarded by a **strict Origin allowlist** — that is what
  actually blocks cross-site WebSocket hijacking.
- Authorization is checked **per action, in context**: is this user in this match, on this side, is
  it their turn, is this their wallet. Being authenticated authorizes nothing by itself (§36).
- Shot validation (server-side, always): power ∈ [0, 12] m/s · tip offset within the ball ·
  elevation ∈ [0°, 90°] · every float finite (reject `NaN`/`Inf` explicitly).
- WebSocket read limit: 32 KB.
- **Never log or return:** password hashes, session tokens, stack traces, DSNs, any secret.

## 18. Angular conventions

- Angular 22: **standalone components, signals, zoneless.** No NgModules.
- Feature-based only. No `components/`, `services/`, `models/`, or `pages/` dumping grounds (§27).
- **`three` is imported only inside `features/game/rendering/**`**, reached via a lazy route, so
  login/lobby/leaderboard never pay its bundle cost.
- **The rAF loop runs outside Angular and touches no signal.** It mutates `THREE.Mesh.position`
  directly. HUD signals update at ~10 Hz (turn, timer, score), never at 60 Hz. Angular's change
  detection must never see a ball (§28).
- **One WebSocket connection, owned by `core/networking`** (§66). No component ever constructs a
  `WebSocket`.
- Cue aiming is local and instant, but it is *intent* only. If client and server disagree,
  **server state wins** and the client snaps (§26).

## 19. 3D asset contract

Server collision geometry is **mathematical** — planes, circles, quadratics. Render meshes are never
authoritative (§30). Any `table.glb` can be swapped for a prettier one with zero gameplay change.

Every asset must satisfy: glTF 2.0 `.glb` · Y-up right-handed · metres · origin at model centre on
the play plane · table surface exactly 2.540 × 1.270 · ball meshes radius 0.028575 at origin ·
KTX2/Basis textures ≤ 2048² · Draco or Meshopt compressed · triangles: table ≤ 40k, cue ≤ 8k,
ball ≤ 2k · PBR metallic-roughness only.

Validated by `scripts/validate-asset.mjs`. Until an asset passes, use procedural placeholders.

---

## 20. Platform package boundaries

| Package | Contains | Never contains |
|---|---|---|
| `platform/config` | one `Config` struct, env-loaded, validated at startup, fail-fast | business defaults |
| `platform/postgres` | pool init, tx helper, health check | `CreateRoom()`, `SettleWager()` |
| `platform/websocket` | hub, connection, read/write pumps, limits | match logic |
| `platform/logging` | `log/slog` JSON handler, context-carried IDs | — |
| `platform/telemetry` | pprof (Phase 1), Prometheus (Phase 19) | — |
| `platform/security` | argon2id, CSRF, secure headers, rate limiter | authz policy |

- **`os.Getenv` appears in exactly one file** (§46): `platform/config`.
- Logging is stdlib `log/slog`. No zap, no zerolog — §9 says a dependency must justify itself.
- Prometheus is not added until Phase 19. `net/http/pprof` ships in Phase 1 on an internal-only port.

## 21. Testing conventions

- `game/physics`: table-driven + **invariants** (energy never increases, momentum conserved in
  elastic pairs, balls never leave the table, every shot terminates) + golden-file regression.
- `game/simulation`: determinism — identical input produces byte-identical output on one architecture.
- Features: real Postgres via `TEST_DATABASE_URL`, **each test in a transaction that rolls back**
  (`platform/postgres/pgtest`). No testcontainers dependency. Tests **skip** when the variable is
  unset, so `make check` stays runnable with no database.
  - `make test-db` creates and migrates `billiards_test`; `make test-integration` runs with it set.
  - Repositories take `postgres.DB` (Query/QueryRow/Exec/Begin), satisfied by both `*pgxpool.Pool`
    and `pgx.Tx`. That is what lets a test hand a repository a transaction.
  - **Use `pgtest.Attempt` for any operation a test expects to fail.** PostgreSQL aborts the whole
    transaction on the first failed statement (25P02), so an unwrapped constraint violation poisons
    the test's own transaction and every later assertion fails for the wrong reason. `Attempt` runs
    it in a SAVEPOINT.
- WebSocket: `httptest.Server` + `coder/websocket` client. Must cover slow-client backpressure close.
- Angular: Vitest (Angular 22 default); Playwright e2e from Phase 12.
- **The physics hot loop asserts `0 allocs/op` as a test, not an aspiration.**
- **No claim of "faster" without a benchmark number** (§49). Results go in `docs/benchmarks.md` with
  the machine recorded.

---

**Gin middleware must be flat, never nested.** A `gin.HandlerFunc` returned by one middleware
constructor (e.g. `authSvc.RequireAuth()`) calls `c.Next()` itself. `c.Next()` advances a **shared
index across Gin's whole handler chain**, so invoking that returned func as a plain function call
from *inside* another middleware makes its `c.Next()` run every later handler — including the real
route handler — before the outer call resumes. The fix is to register both as separate entries in
one `[]gin.HandlerFunc` passed to Gin, never to call one middleware's handler from within another's
body. This cost real debugging time in Phase 3 (`internal/users`' auth bridge) and is now a
regression test: `TestMultipleAuthMiddlewareChainInOrderNotNested` in
`internal/users/handler_test.go`.

## 21a. Gotchas discovered the hard way

Each of these cost real debugging time. They are here so they cost it once.

**PostgreSQL 18 changed its volume mount point.** Mount `pgdata:/var/lib/postgresql`, *not*
`.../data`. The 18+ images place version-specific subdirectories underneath so `pg_upgrade --link`
works without crossing a mount boundary. Mounting `.../data` makes the container refuse to start
with a long message about `pg_ctlcluster` that does not obviously say "your mount path is wrong."

**`localhost` resolves to `::1` inside Alpine containers.** nginx's `listen 80;` binds IPv4 only, so
a healthcheck using `http://localhost/` gets connection refused from a perfectly healthy server. Fix
both ends: `listen [::]:80;` in nginx, and `127.0.0.1` rather than `localhost` in the probe.

**Traefik does not synthesise the `traefik` entrypoint once you declare entryPoints explicitly.**
`--api.insecure=true` then has nothing to bind to and the dashboard is silently unreachable. The
entrypoint must be named in `traefik.yml`.

**Compose does not recreate a container when only a bind-mounted config file changed.** The service
definition is unchanged, so `docker compose up -d` is a no-op and you debug a config you never
actually loaded. Use `--force-recreate` after editing `traefik.yml`.

**Each Make recipe line runs in its own shell.** An `exit 0` in a guard on one line does not stop
the next line from running. Guard and command must share one shell.

**A failed statement aborts the entire PostgreSQL transaction.** Every subsequent statement returns
25P02 "current transaction is aborted" until rollback. This bites hardest in tests that deliberately
trigger a constraint violation — use `pgtest.Attempt` (SAVEPOINT) around anything expected to fail.

**`\gexec` does not work inside `psql -c`.** It is a psql meta-command and needs stdin or a script;
in a `-c` argument it is silently useless. Use a check-then-create instead.

**`ng test`, not `npx vitest run`.** Angular 22's `@angular/build:unit-test` builder configures the
TestBed environment and Vitest globals. Raw Vitest fails with `describe is not defined`.

## 22. Decision log

Full rationale, alternatives, and consequences for each: [docs/adr/](docs/adr/).

| # | Decision | Date |
|---|---|---|
| 0001 | Feature-based modular monolith, strictly layered | 2026-08-14 |
| 0002 | pgx/v5 with explicit SQL — no ORM **and no sqlc** | 2026-08-14 |
| 0003 | Server-authoritative gameplay | 2026-08-14 |
| 0004 | Custom billiards physics at fixed timestep (not a general rigid-body engine) | 2026-08-14 |
| 0005 | Precomputed shot trajectory transport (not streaming snapshots) | 2026-08-14 |
| 0006 | Versioned WebSocket protocol envelope | 2026-08-14 |
| 0007 | One goroutine owns one match (actor model) | 2026-08-14 |
| 0008 | Y-up right-handed metric coordinates | 2026-08-14 |
| 0009 | Opaque session cookies, not JWT | 2026-08-14 |
| 0010 | Double-entry immutable wallet ledger | 2026-08-14 |
| 0011 | UUIDv7 identifiers | 2026-08-14 |
| 0012 | Single Go module, layered feature dependencies | 2026-08-14 |
| — | `coder/websocket` over `gorilla/websocket` (archived upstream) | 2026-08-14 |
| — | **Path-based routing on one host** rather than an `api.` subdomain — see §10a | 2026-08-14 |
| — | `lobby` moved L4 → L5; same-layer imports forbidden — see §5 | 2026-08-14 |
| — | Production domain: `billiards-online.duckdns.org` | 2026-08-14 |
| — | Credentials in an auth-owned table, never a column on `users` — see §17 | 2026-08-14 |
| — | `postgres.DB` interface + `pgtest` rollback harness for feature tests — see §21 | 2026-08-14 |
| — | Profile split from `users` into its own table, created atomically alongside every account — see §15 | 2026-08-14 |
| — | Cross-layer identity uses a feature-owned context carrier, not an upward import — see §5 | 2026-08-14 |

## 23. Known deviations from the constitution

Called out deliberately rather than applied silently, as §78 requires.

1. **§67 diagram implies streaming snapshots.** We precompute the whole shot and send one trajectory
   message (ADR 0005). Every principle §67 protects is preserved — fixed timestep, server authority,
   snapshots, client interpolation — the snapshots simply arrive as a batch.
2. **§14 lists the `game/` packages as flat siblings.** We impose a strict DAG
   (`protocol → state → rules → physics`, `simulation → all`). Same packages, same isolation, with
   the dependency direction pinned so the §17 physics/rules separation is enforced by the compiler
   rather than by discipline.

## 24. Known risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | **Go permits FMA contraction**, so a replay on arm64 may differ bit-for-bit from amd64 | Persist a final-state checkpoint per shot, not just shot params. Replay is best-effort visual; the checkpoint is authoritative. Pin `simulation_version` on every event. |
| R2 | Single-instance match ownership caps horizontal scaling | Actor model already isolates state. Measure before solving (§8). |
| R3 | Physics realism has a long tail — cushion response and throw are where sims feel wrong | Phase 7 = plausible, Phase 10 = realistic. Validate against published reference shots, not intuition. |
| R4 | Three.js can silently re-enter change detection in a zoneless Angular app | Renderer only under the lazy game route; rAF loop touches no signal; assert the bundle split in CI. |
| R5 | 2v2 turn ordering leaking outside `game/rules` | Sides modelled from Phase 6, before 2v2 exists. |
| R6 | Wallet concurrency bugs are silent and permanent | Row locking + unique idempotency key + sum-to-zero constraint + concurrent-settlement test, all before wagering ships. |
| R7 | Shot payload grows with ball count and shot duration | Sizing done (~5.7 KB raw). Measure JSON in Phase 9. |
| R8 | AI-generated assets arrive at wrong scale/orientation and quietly break aiming | Asset contract + `scripts/validate-asset.mjs` gate. |

## 25. Open questions

- Rating algorithm (Glicko-2 vs Elo) — deliberately deferred to Phase 15.
- Binary trajectory codec — build only if the Phase 9 JSON measurement justifies it.
- Spectator mid-shot join needs a playback seek offset — design in Phase 13.

---

## 25a. Repository and push convention

Remote: **https://github.com/sassinzz13/billiards-online** (public), default branch `main`.
Go module path: `github.com/sassinzz13/billiards-online`.

**Push after every completed phase.** One commit per phase, subject line `Phase N: <what landed>`.
A phase is only pushable when its PLAN.md checklist is fully ticked, `make check` is green, and its
exit criterion is demonstrably met.

Because the repository is **public**, check before every push that `.env` is untracked and no real
credential is staged. `.env.example` contains the placeholder `change-me-in-your-local-env` by
design; anything else matching a credential pattern is a bug.

## 26. Commands

All verified working as of Phase 1. `make help` lists everything.

```bash
make up            # build + start traefik, web, server, postgres
make down          # stop, keeping the database volume
make logs          # follow all services
make check         # lint + vet + test — what CI runs

make test          # go test ./...
make test-game     # go test ./game/...  — must pass with no DB, no Docker, no network
make arch          # import-boundary enforcement
make bench         # go test -bench=. -benchmem ./game/...

make migrate-up    # apply pending migrations
make migrate NAME=add_rooms   # create a migration pair
make psql          # psql shell

make web-dev       # ng serve
make web-test      # ng test  (NOT npx vitest — see §21a)
make pprof         # interactive heap profile
```

URLs once `make up` completes:

| | |
|---|---|
| App | http://billiards.localhost |
| API | http://billiards.localhost/api/v1/health |
| Traefik dashboard | http://127.0.0.1:8081/dashboard/ (loopback only) |
| pprof | http://127.0.0.1:6060/debug/pprof/ (loopback only) |

`.env` is created from `.env.example` by `make env`, which `make up` depends on.
