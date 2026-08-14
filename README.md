# Billiards

A browser-based realtime multiplayer 3D billiards platform.

Angular 22 + Three.js on the front end. Go + Gin + PostgreSQL on the back end. Everything behind
Traefik in Docker. Gameplay is **server-authoritative** — the client sends intent, a custom
billiards physics engine on the server decides what actually happened.

Virtual currency only. There is no real-money processing anywhere in this system.

> **Status: Phase 1 (development infrastructure) complete.** `make up` brings up a working stack.
> There is no gameplay yet — Phase 2 adds authentication. See [PLAN.md](PLAN.md).

## Documentation

| File | What's in it |
|---|---|
| [PLAN.md](PLAN.md) | The 21-phase roadmap with a running checklist. Start here to find the current phase. |
| [MEMORY.md](MEMORY.md) | Conventions, physics constants, dependency rules, decision log. Read before writing code. |
| [CLAUDE.md](CLAUDE.md) | Working rules for contributors and agents. |
| [docs/architecture.md](docs/architecture.md) | System diagrams and boundaries. |
| [docs/protocol.md](docs/protocol.md) | WebSocket envelope, message catalogue, reconnect. |
| [docs/coordinates.md](docs/coordinates.md) | Coordinate system, table constants, 3D asset contract. |
| [docs/adr/](docs/adr/) | Architecture decision records, with alternatives and tradeoffs. |

## Design in one page

**Feature-based modular monolith.** The backend is organized by product capability
(`auth`, `rooms`, `matches`, `wallet`, …), not by technical layer. Features are strictly layered so
circular dependencies are structurally impossible, and they talk to each other through
consumer-side interfaces wired in a single composition root. No event bus, no DI container, no
internal HTTP.

**The billiards engine is isolated.** `game/` imports the standard library and nothing else — no
Gin, no pgx, no WebSockets. `go test ./game/...` runs with no database, no Docker, and no network.
Physics emits facts (`BallHitBall`, `BallPocketed`); rules alone decide what they mean (foul,
scratch, win). The two never mix.

**Shots are precomputed, not streamed.** Billiards is turn-based, so the server resolves an entire
shot in one synchronous call (~1 ms of CPU for ~4 s of table time) and ships the whole trajectory in
a single message. The client plays it back and snaps to the authoritative final state. One message
per shot instead of a hundred, no per-match ticker, and replay is deterministic.
See [ADR 0005](docs/adr/0005-precomputed-shot-trajectory.md).

**One goroutine owns one match.** Match state is never shared, only messaged. Every long-lived
goroutine has an owner, a cancellation context, and a bounded queue. A slow client is closed with a
policy code rather than being allowed to grow a queue — it can never block the simulation.

**Explicit SQL, no ORM.** pgx/v5 with SQL written as Go consts inside each feature's repository,
right next to its caller. Schema changes are reviewable migrations. Money is a double-entry
immutable ledger in `BIGINT` minor units — no floats touch money, and settlement is idempotent.

## Stack

| | |
|---|---|
| Frontend | Angular 22 (standalone, signals, zoneless), TypeScript, Three.js, WebGL |
| Backend | Go 1.26, Gin, `coder/websocket`, pgx/v5 |
| Database | PostgreSQL 18, `golang-migrate`, explicit SQL |
| Infra | Docker, Docker Compose, Traefik v3 |

Exact pinned versions are in [MEMORY.md §3](MEMORY.md).

## Getting started

Prerequisites: Docker with Compose v2+. Go 1.26+ and Node 26+ only if you want to run things
outside containers.

```bash
make up
```

That creates `.env` from `.env.example` if needed, builds both images, and starts everything.
Change `POSTGRES_PASSWORD` in `.env` before doing anything real.

| | |
|---|---|
| App | http://billiards.localhost |
| API | http://billiards.localhost/api/v1/health |
| Traefik dashboard | http://127.0.0.1:8081/dashboard/ (loopback only) |
| pprof | http://127.0.0.1:6060/debug/pprof/ (loopback only) |

```bash
make check    # lint + vet + test — what CI runs
make arch     # import-boundary enforcement
make help     # every target
```

**Routing is path-based on one host** — `/` to the Angular container, `/api` and `/ws` to the Go
server. That keeps the app and API same-origin, which is what makes the session cookie work without
a `Domain` attribute and removes CORS entirely. Production uses the same layout on
`billiards-online.duckdns.org` with TLS from Let's Encrypt:

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d
```

## Contributing

Read [CLAUDE.md](CLAUDE.md) first. In short: stay inside the current phase, respect the layer table,
write tests in the same phase as the code, and tick a PLAN.md box only when the thing is
implemented, tested, and passing.
