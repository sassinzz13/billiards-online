# CLAUDE.md

Realtime 3D multiplayer billiards platform. Angular 22 + Three.js · Go + Gin · PostgreSQL ·
Docker + Traefik. Server-authoritative gameplay, custom physics engine, virtual currency only.

## Read these first, every session

1. **[MEMORY.md](MEMORY.md)** — conventions, physics constants, dependency rules, decision log.
   Everything expensive to re-derive and easy to get subtly wrong lives here.
2. **[PLAN.md](PLAN.md)** — the 21 phases with checklists. Find the current phase before starting.

The governing specification is the engineering constitution supplied by the project owner; its
section numbers (§1–§78) are cited throughout the docs. When a doc and the constitution disagree,
the constitution wins and the doc is wrong — fix the doc.

## Rules

**Stay in the current phase.** Do not implement future phases early, even when it looks cheap.
Implement the smallest complete vertical slice, with its tests, in the same phase.

**Respect the layer table** in MEMORY.md §5. Imports point downward only. `game/**` is stdlib-only.
`platform/**` holds zero business rules. There is no `shared/`, `common/`, or `utils/` package.
`make arch` enforces this — run it.

**The server is authoritative.** The client sends intent; the server decides what happened. No client
claim about position, pocketing, score, turn, or balance is ever believed.

**Physics and rules never mix.** Physics emits facts (`BallHitBall`, `BallPocketed`). Rules decide
meaning (foul, scratch, win). No `if isEightBall` inside collision detection, ever.

**Feature ownership over directory conformity.** When deciding where code goes, ask which product
feature owns the behavior, and put it there.

**Measure before claiming.** No "faster" without a benchmark number. No optimization without a
profile that found it.

## Keeping the docs alive

These files only work if they stay true:

- **Tick a PLAN.md box only when the thing is implemented, tested, and passing.** A box ticked
  because a file exists is worse than no box — it makes the document lie.
- **Add to MEMORY.md** anything a future session could not re-derive: a constant, a gotcha, a
  constraint discovered the hard way. If it is significant, write an ADR in `docs/adr/` with Context,
  Decision, Alternatives Considered, Consequences.
- **Update, don't append.** If a decision changes, edit the existing entry and date it. Never leave
  two contradictory statements in MEMORY.md.

## Commands

`make help` lists everything; MEMORY.md §26 has the full set with URLs.

```bash
make up          # build + start traefik, web, server, postgres
make check       # lint + vet + test — what CI runs
make arch        # import-boundary enforcement
make test-game   # go test ./game/...  — must pass with no DB, no Docker, no network
make web-test    # ng test  (NOT npx vitest)
```

## Pushing

Remote is `https://github.com/sassinzz13/billiards-online` (public), branch `main`. **Push after
every completed phase**, one commit per phase. A phase is pushable only when its PLAN.md checklist
is fully ticked, `make check` is green, and its exit criterion is demonstrably met. The repo is
public — confirm `.env` is untracked before pushing. See MEMORY.md §25a.

## Current state

**Phases 0-5 complete.** Players can sign up, sign in, view/edit their profile, create/browse/join
rooms with ready states, and open an authenticated, versioned, bounded WebSocket at `/ws`.
`internal/realtime` (L6) owns the gateway; `platform/websocket` owns the transport;
`game/protocol` owns the envelope. No `internal/lobby` package exists yet (PLAN.md Phase 4 explains
why). 142 Go tests and 54 Angular tests pass.

**Phase 6 (match lifecycle) is next.** No gameplay message type exists on the wire yet — every
envelope type reaching the router today is "unknown" by design; Phase 6 starts changing that.

Integration tests need a database: `make test-db` once, then `make test-integration`. Without
`TEST_DATABASE_URL` they skip, which is why `make check` alone can look deceptively small.

Before touching realtime code, read MEMORY.md §5 (layer table — note `realtime` and `rooms` may
import `auth` directly, `users` may not) and §21a, which now holds five hard-won gotchas: Gin
middleware chaining, `now()` vs `clock_timestamp()`, a `UNIQUE` index outperforming a row lock,
`Close` racing the write pump (use `CloseAfterDrain` to order a send before a close), and why a
graceful close can itself stall under extreme backlog without that meaning the policy failed.
