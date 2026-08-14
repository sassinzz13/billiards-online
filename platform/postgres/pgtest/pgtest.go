// Package pgtest provides a PostgreSQL harness for integration tests.
//
// Every test runs inside a transaction that is rolled back on cleanup. Tests therefore see a real
// database — real constraints, real locking, real error codes — without any of them observing each
// other's writes and without truncating tables between runs.
//
// Tests SKIP when TEST_DATABASE_URL is unset, so `go test ./...` stays runnable with no database.
// `make test-db` creates and migrates the test database; `make test-integration` runs with it set.
package pgtest

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const envURL = "TEST_DATABASE_URL"

var (
	poolOnce sync.Once
	pool     *pgxpool.Pool
	poolErr  error
)

// Pool returns a process-wide pool against the test database, or skips the test if
// TEST_DATABASE_URL is unset.
//
// Most tests want DB instead — this exists for the few that need to observe committed state.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv(envURL)
	if url == "" {
		t.Skipf("%s not set — run `make test-db` then `make test-integration`", envURL)
	}

	poolOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cfg, err := pgxpool.ParseConfig(url)
		if err != nil {
			poolErr = err
			return
		}
		// Tests run in parallel across packages; a small pool keeps a runaway suite from exhausting
		// PostgreSQL's connection limit.
		cfg.MaxConns = 8

		if pool, poolErr = pgxpool.NewWithConfig(ctx, cfg); poolErr != nil {
			return
		}
		poolErr = pool.Ping(ctx)
	})

	if poolErr != nil {
		t.Fatalf("connect to %s: %v", envURL, poolErr)
	}
	return pool
}

// DB returns a transaction that is rolled back when the test finishes.
//
// Pass it wherever a postgres.DB is expected. Nothing written through it survives the test, so
// tests are order-independent and can run against a database that already has data.
//
// A service that opens its own transaction still works: pgx turns a nested Begin into a SAVEPOINT.
func DB(t *testing.T) pgx.Tx {
	t.Helper()

	p := Pool(t)
	ctx := context.Background()

	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatalf("begin test transaction: %v", err)
	}
	t.Cleanup(func() {
		// The test may have failed mid-transaction, so an error here is expected and not worth
		// reporting; the rollback is best-effort cleanup.
		_ = tx.Rollback(context.Background())
	})
	return tx
}

// Attempt runs fn inside a SAVEPOINT and returns its error, leaving the surrounding transaction
// usable either way.
//
// PostgreSQL aborts an entire transaction as soon as any statement in it fails: every subsequent
// statement returns "current transaction is aborted" (25P02) until rollback. A test that
// deliberately triggers a constraint violation — asserting that a duplicate email is rejected, say
// — would therefore poison its own transaction, and every later assertion in that test would fail
// for the wrong reason.
//
// A savepoint contains the failure. Use this for any operation a test expects to fail:
//
//	err := pgtest.Attempt(t, ctx, tx, func(sp pgx.Tx) error {
//	    _, err := svc.WithDB(sp).Create(ctx, dupeEmail, handle)
//	    return err
//	})
//	// tx is still usable here
//
// Services that manage their own transaction through postgres.InTx already get this for free, since
// pgx turns a nested Begin into a SAVEPOINT.
func Attempt(t *testing.T, ctx context.Context, tx pgx.Tx, fn func(pgx.Tx) error) error {
	t.Helper()

	sp, err := tx.Begin(ctx)
	if err != nil {
		t.Fatalf("begin savepoint: %v", err)
	}

	fnErr := fn(sp)
	if fnErr != nil {
		// Rolls back to the savepoint only, so the outer transaction survives.
		_ = sp.Rollback(ctx)
		return fnErr
	}
	if err := sp.Commit(ctx); err != nil {
		t.Fatalf("release savepoint: %v", err)
	}
	return nil
}

// Context returns a context with a deadline, so a hung query fails the test rather than the suite.
func Context(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}
