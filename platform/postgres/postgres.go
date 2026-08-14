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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sassinzz13/billiards-online/platform/config"
)

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
//	err := postgres.InTx(ctx, pool, func(tx pgx.Tx) error {
//	    // ... several statements that must succeed or fail together
//	    return nil
//	})
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	return InTxOptions(ctx, pool, pgx.TxOptions{}, fn)
}

// InTxOptions is InTx with explicit transaction options, for the rare operation that needs a
// stricter isolation level than the default.
func InTxOptions(ctx context.Context, pool *pgxpool.Pool, opts pgx.TxOptions, fn func(pgx.Tx) error) (err error) {
	tx, err := pool.BeginTx(ctx, opts)
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
