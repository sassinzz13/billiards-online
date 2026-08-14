# 0002 — pgx/v5 with explicit SQL: no ORM, and no sqlc

**Status:** Accepted · 2026-08-14

## Context

The platform needs correct, fast, inspectable database access for a mix of ordinary CRUD and
genuinely demanding operations: transactional room joins with capacity limits, wager settlement under
concurrency, and leaderboard queries that must not scan match history.

The constitution prohibits ORMs outright (§10) and permits but does not require `sqlc` (§11). So the
ORM question is settled; the open question was whether to add `sqlc` on top of pgx.

## Decision

**pgx/v5 with pgxpool and explicit SQL. No ORM. No sqlc.**

SQL lives as Go string constants inside each feature's `repository.go`, directly next to the code
that runs it. Result mapping uses pgx's own helpers:

```go
const qGetProfile = `
    SELECT id, handle, display_name, created_at
    FROM player_profiles
    WHERE user_id = $1`

rows, err := db.Query(ctx, qGetProfile, userID)
if err != nil {
    return Profile{}, fmt.Errorf("query profile: %w", err)
}
return pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Profile])
```

PostgreSQL features are used deliberately where they earn their place: transactions, foreign keys,
unique and check constraints, partial indexes, CTEs, `RETURNING`, upserts, `SELECT … FOR UPDATE`, and
`SKIP LOCKED` for matchmaking.

## Alternatives considered

**GORM or a similar ORM.** Prohibited by §10, and the prohibition is correct for this workload.
ORMs hide the generated SQL, make N+1 queries easy to write and hard to notice, rely on reflection in
hot paths, and turn `EXPLAIN ANALYZE` into an exercise in archaeology. For wager settlement — where
the exact locking behaviour is the whole point — an ORM actively obstructs correctness.

**sqlc.** Genuinely appealing: it generates typed Go from real SQL, keeps SQL as the source of truth,
and catches column and type drift at compile time. Rejected for three reasons.

First, the marginal benefit over `pgx.RowToStructByName` is small — roughly five lines per query, and
those five lines are readable Go rather than generated code.

Second, it adds a codegen step to every schema change: edit migration, edit `queries.sql`, run
`sqlc generate`, rebuild. That loop is friction on every database change, and stale generated code is
a new failure mode.

Third, and most importantly, sqlc's package model fights the feature ownership this project is built
on (ADR 0001). It wants a query directory and emits a generated package; feature-based architecture
wants SQL to live inside `internal/rooms/` alongside the code that uses it. Working around that means
either a config per feature or generated code sitting awkwardly beside feature-owned code.

The type-drift argument is real but addressable: integration tests run against a real PostgreSQL
instance, so a column rename fails a test rather than reaching production.

**`database/sql` with the stdlib interface.** Rejected because it gives up pgx's native protocol
support, `COPY`, batching, and PostgreSQL-specific type handling, in exchange for a portability this
project does not want. PostgreSQL is the authoritative datastore, not a swappable detail.

**A generic repository abstraction over pgx.** Rejected by §9 and §64. It would add an interface per
entity to enable a substitution that never happens, and obscure exactly the query behaviour that
needs to stay visible.

## Consequences

**Good.** Every query is visible in the file that runs it — no indirection between "this function is
slow" and the SQL responsible. `EXPLAIN ANALYZE` is trivial: copy the const. No codegen step, no
generated packages, no build-order dependency. PostgreSQL-specific features are directly reachable
rather than fought for. SQL stays reviewable in pull requests.

**Costs.** Column or type drift is caught by integration tests rather than the compiler — this is the
real tradeoff, and it makes the Phase 1 test-database setup load-bearing rather than optional. Result
mapping is a few lines per query. Long queries as Go string constants are slightly less pleasant to
edit than `.sql` files, and lose editor SQL syntax highlighting.

**Revisit if.** Repository code becomes genuinely tedious across many features, or type drift causes
a production incident that tests should have caught. Adopting sqlc later is a mechanical, incremental
change — SQL is already the source of truth, so nothing needs rewriting to move it into `queries.sql`.
That reversibility is part of why starting without it is safe.
