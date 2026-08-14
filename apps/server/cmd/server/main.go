// Command server is the billiards API and realtime gateway.
//
// This file is the composition root: the only place where configuration, infrastructure, and
// features are wired together. Features never construct each other's dependencies — they receive
// them here. Keeping construction in one place is what lets the layer rules in MEMORY.md §5 hold,
// and it makes the whole dependency graph readable in a single file.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sassinzz13/billiards-online/internal/auth"
	"github.com/sassinzz13/billiards-online/internal/matches"
	"github.com/sassinzz13/billiards-online/internal/realtime"
	"github.com/sassinzz13/billiards-online/internal/rooms"
	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/config"
	"github.com/sassinzz13/billiards-online/platform/logging"
	"github.com/sassinzz13/billiards-online/platform/postgres"
	"github.com/sassinzz13/billiards-online/platform/security"
)

func main() {
	// The container HEALTHCHECK re-executes this binary because the image is FROM scratch and has
	// no shell to run curl in. See healthcheck.go.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(runHealthcheck())
	}

	if err := run(); err != nil {
		// The logger may not exist yet if configuration failed, so report to stderr directly.
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Configuration is validated before anything else starts. A misconfigured process must fail
	// here, loudly, rather than half-start and fail at the first request.
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := logging.New(os.Stdout, cfg.Log.Level, cfg.Log.Format)
	slog.SetDefault(logger)

	// Signal handling is installed before any resource is acquired, so a Ctrl-C during startup is
	// honoured rather than ignored until the server is fully up.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting server",
		"env", cfg.Env,
		"addr", cfg.HTTP.Addr,
		"internalAddr", cfg.HTTP.InternalAddr,
	)

	pool, err := postgres.Connect(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()
	logger.Info("database connected", "maxConns", cfg.Database.MaxConns)

	// ---- feature wiring ------------------------------------------------------------------
	// This is the only place features learn about each other. auth (L1) receives users (L0);
	// users knows nothing about auth. The direction is enforced by tests/arch.

	// 5 attempts, refilling at one per 12s. Generous enough that a real person mistyping a password
	// never notices, tight enough that credential stuffing is not viable — and each attempt costs
	// 64 MiB of Argon2, so the limit protects memory as much as it protects accounts (§59).
	authLimiter := security.NewRateLimiter(1.0/12.0, 5, 15*time.Minute)
	defer authLimiter.Close()

	usersSvc := users.NewService(pool)
	authSvc := auth.NewService(pool, usersSvc)
	authHandler := auth.NewHandler(authSvc, cfg.Env.IsProduction(), authLimiter)

	// users (L0) cannot import auth (L1) to read its Identity type, so this bridge does the
	// translation here in the composition root — the one place allowed to know about both. It runs
	// as a second, ordinary link in Gin's middleware chain, after auth.RequireAuth — not nested
	// inside it. RequireAuth's handler calls c.Next() itself; invoking it as a plain function call
	// from inside another middleware would let that c.Next() run the rest of the chain, including
	// the route handler, before this bridge had a chance to attach the ID. See
	// internal/users/routes.go for the longer version of this warning.
	bridgeIdentityToUsers := func(c *gin.Context) {
		if identity, ok := auth.IdentityFrom(c.Request.Context()); ok {
			c.Request = c.Request.WithContext(users.WithUserID(c.Request.Context(), identity.UserID))
		}
		c.Next()
	}
	usersHandler := users.NewHandler(usersSvc, authSvc.RequireAuth(), bridgeIdentityToUsers)

	// matches sits at L3, below rooms' L4 — rooms imports it directly to turn a full, ready room
	// into a match (internal/rooms/service.go's Start). ctx, not a per-request context, is what
	// every match actor's lifetime derives from, same reasoning as the realtime gateway below: an
	// actor must stop the instant the process starts shutting down, not linger on an unrelated
	// per-request deadline.
	matchesRegistry := matches.NewRegistry()
	matchesSvc := matches.NewService(pool, matchesRegistry, ctx, logger)
	matchesHandler := matches.NewHandler(matchesSvc, authSvc)

	// rooms sits at L4, above auth's L1, so it can depend on auth directly — no bridge needed here
	// (see internal/rooms/handler.go for the contrast with users).
	//
	// 3 rooms per 5 minutes. A real host rarely creates more than one room in a sitting; this is
	// wide enough not to be felt, tight enough that spamming empty rooms is not a viable way to
	// pollute public discovery (§59).
	roomCreateLimiter := security.NewRateLimiter(3.0/300.0, 3, 15*time.Minute)
	defer roomCreateLimiter.Close()

	roomsSvc := rooms.NewService(pool, matchesSvc)
	roomsHandler := rooms.NewHandler(roomsSvc, authSvc, roomCreateLimiter)

	// realtime sits at L6, the top of the stack, so it may depend on any feature below it — auth
	// directly, same reasoning as rooms. ctx (not c.Request.Context()) is what every open
	// connection's lifetime derives from; see NewGateway's doc comment for why that distinction
	// matters at shutdown.
	gateway := realtime.NewGateway(authSvc, cfg, ctx)

	public := &http.Server{
		Handler:           newRouter(cfg, logger, pool, authHandler, usersHandler, roomsHandler, matchesHandler, gateway),
		Addr:              cfg.HTTP.Addr,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: it would cap the lifetime of every WebSocket connection at /ws, which
		// is meant to stay open far longer than any ordinary HTTP request. Per-request deadlines
		// are enforced by context instead (platform/websocket.WriteTimeout bounds each individual
		// frame write).
	}

	// pprof lives on a separate listener so it is never routed publicly. Traefik only ever sees
	// the public one. See §47 and the HTTP_INTERNAL_ADDR check in platform/config.
	internal := &http.Server{
		Handler:           newInternalRouter(),
		Addr:              cfg.HTTP.InternalAddr,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 2)
	go serve(public, "public", logger, errc)
	go serve(internal, "internal", logger, errc)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	return shutdown(public, internal, matchesRegistry, cfg.HTTP.ShutdownTimeout, logger)
}

func serve(srv *http.Server, name string, logger *slog.Logger, errc chan<- error) {
	logger.Info("listening", "listener", name, "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errc <- fmt.Errorf("%s listener: %w", name, err)
	}
}

// shutdown stops both listeners gracefully within a single shared budget.
//
// Both are given the same deadline rather than a sequential timeout each, so a slow public listener
// cannot make total shutdown take twice as long as configured. Exceeding the budget drops in-flight
// requests, which is preferable to a deploy that hangs indefinitely.
//
// Match actors are given the same shared budget: they already stop on their own the instant ctx
// (the process's shutdown context, not this function's timeout-bound one) is cancelled — see
// matches.NewService and Actor.run — so registry.Wait here is just making sure the deploy doesn't
// proceed while one is still mid-persist, never what triggers them to stop. See §62.
func shutdown(public, internal *http.Server, registry *matches.Registry, timeout time.Duration, logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	done := make(chan error, 2)
	go func() { done <- public.Shutdown(ctx) }()
	go func() { done <- internal.Shutdown(ctx) }()

	registry.Wait(ctx)

	var errs []error
	for range 2 {
		if err := <-done; err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		logger.Error("graceful shutdown incomplete", "error", err, "timeout", timeout)
		return fmt.Errorf("shutdown: %w", err)
	}

	logger.Info("shutdown complete")
	return nil
}

// newRouter builds the public HTTP surface.
//
// Routes are grouped under /api/v1 so versioning can be introduced without moving anything (§52).
// Each feature registers its own routes through its handler, so route ownership stays with the
// feature that implements them rather than accumulating in a central router file.
func newRouter(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool, authHandler *auth.Handler, usersHandler *users.Handler, roomsHandler *rooms.Handler, matchesHandler *matches.Handler, gateway *realtime.Gateway) http.Handler {
	if cfg.Env.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	// gin.New rather than gin.Default: Default installs Gin's own text logger, which would bypass
	// the structured logging every other line in this system uses.
	r := gin.New()
	r.Use(
		requestID(),
		requestLogger(logger),
		recovery(logger),
		secureHeaders(),
	)

	// Liveness: is the process up? Deliberately checks nothing else, so a database outage does not
	// cause the orchestrator to kill an otherwise healthy process.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Readiness: should this instance receive traffic? A failing dependency means no.
	r.GET("/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if err := postgres.Health(ctx, pool); err != nil {
			// The detail goes to the log, not to the response: a readiness probe must not leak
			// infrastructure state to whoever can reach it (§42).
			logging.Logger(c.Request.Context()).Error("readiness check failed", "error", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "unavailable",
				"checks": gin.H{"database": "down"},
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status": "ready",
			"checks": gin.H{"database": "up"},
		})
	})

	// Realtime gateway. At root, not under /api/v1 — matching the routing table in MEMORY.md §11:
	// anything that changes live match state goes over WebSocket, everything else is REST. Traefik
	// already routes PathPrefix(/ws) to this service alongside /api (docker-compose.yml).
	r.GET("/ws", gateway.ServeWS)

	v1 := r.Group("/api/v1")
	v1.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "version": 1})
	})

	authHandler.RegisterRoutes(v1)
	usersHandler.RegisterRoutes(v1)
	roomsHandler.RegisterRoutes(v1)
	matchesHandler.RegisterRoutes(v1)

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": gin.H{
			"code":    "not_found",
			"message": "The requested resource does not exist.",
		}})
	})

	return r
}
