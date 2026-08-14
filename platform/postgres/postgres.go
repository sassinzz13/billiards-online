// Package postgres provides the connection pool and transaction plumbing shared by every feature.
//
// It contains no business rules. Pool setup, transaction helpers, and health checks belong here;
// CreateRoom, SettleWager, and CalculateMatchWinner belong to their owning features. See §7 of the
// engineering constitution.
//
// There is deliberately no repository abstraction and no ORM. Features hold a *pgxpool.Pool and
// write explicit SQL. See ADR 0002.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sassinzz13/billiards-online/platform/config"
)

// DB is anything SQL can be run against. Both *pgxpool.Pool and pgx.Tx satisfy it.
//
// Feature repositories take a DB rather than a concrete *pgxpool.Pool for two reasons:
//
//   - An operation that must be atomic can pass the transaction straight down, so the same
//     repository method works inside and outside a transaction with no duplicate code path.
//   - Tests can hand a repository a transaction and roll it back afterwards, which is what keeps
//     integration tests isolated without truncating tables between them.
//
// This is a genuine boundary rather than an interface added out of habit (§64). It is deliberately
// the smallest set of methods that satisfies both: adding to it makes the test harness harder, not
// easier.
//
// Begin is included because pgx.Tx.Begin opens a SAVEPOINT rather than a nested transaction, so a
// service that manages its own transaction still composes correctly under a test-owned one.
type DB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Connect opens the pool and verifies it can actually reach the database.
//
// A pool that has never been used reports no error, so a bad DATABASE_URL would otherwise surface
// at the first query rather than at startup. The ping makes that failure immediate.
func Connect(ctx context.Context, cfg config.Database) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		// The URL contains the password, so it is never included in the error.
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Health reports whether the database is reachable. It backs the readiness endpoint.
func Health(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("no database pool")
	}
	return pool.Ping(ctx)
}

// InTx runs fn inside a transaction, committing on success and rolling back on error or panic.
//
// Transactions must stay short. Never hold one across a match, a shot, or a network round trip:
// see §37 and MEMORY.md §15.
//
//	err := postgres.InTx(ctx, db, func(tx pgx.Tx) error {
//	    // ... several statements that must succeed or fail together
//	    return nil
//	})
//
// Nesting is safe: if db is already a transaction, pgx opens a SAVEPOINT, so a service that manages
// its own transaction still works when a test wraps it in one.
func InTx(ctx context.Context, db DB, fn func(pgx.Tx) error) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			// Roll back before re-panicking, otherwise the connection returns to the pool with an
			// open transaction and poisons the next caller.
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			// context.WithoutCancel matters here: if ctx was cancelled, that is often *why* fn
			// failed, and a cancelled context cannot issue the rollback.
			if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				err = errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
			}
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
