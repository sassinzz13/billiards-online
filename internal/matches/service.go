package matches

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sassinzz13/billiards-online/platform/postgres"
)

// Service is the entry point to the matches feature.
type Service struct {
	db       postgres.DB
	registry *Registry
	rootCtx  context.Context
	logger   *slog.Logger
}

// NewService wires a Service. registry and rootCtx may be nil/zero — a Service constructed that
// way still creates and reads match rows correctly, it just never spawns an actor (see
// StartActor), which is exactly what a test that only exercises persistence wants, matching
// internal/rooms.Service.WithDB's reasoning for the same shape.
func NewService(db postgres.DB, registry *Registry, rootCtx context.Context, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{db: db, registry: registry, rootCtx: rootCtx, logger: logger}
}

// WithDB returns a Service bound to a different DB — see rooms.Service.WithDB for why this exists:
// it lets a test hand a Service its own rolled-back transaction.
func (s *Service) WithDB(db postgres.DB) *Service {
	return &Service{db: db, registry: s.registry, rootCtx: s.rootCtx, logger: s.logger}
}

// CreateInTx inserts a match's rows using tx, without spawning its actor.
//
// Spawning an actor here would be premature: tx has not committed, and a caller building a match
// as part of a larger transaction (rooms.Service.Start closes the originating room in the same
// transaction) may still roll the whole thing back after this returns. Create, below, is the
// common case of doing both steps — insert then spawn — atomically from the matches side alone;
// a cross-feature caller instead calls CreateInTx and then StartActor once its own transaction has
// actually committed.
func (s *Service) CreateInTx(ctx context.Context, tx pgx.Tx, in CreateInput) (Match, error) {
	if err := in.normalize(); err != nil {
		return Match{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Match{}, fmt.Errorf("generate match id: %w", err)
	}
	return insertMatch(ctx, tx, id, in)
}

// Create inserts a match in its own transaction and starts its actor once the insert commits.
func (s *Service) Create(ctx context.Context, in CreateInput) (Match, error) {
	var match Match
	err := postgres.InTx(ctx, s.db, func(tx pgx.Tx) error {
		m, err := s.CreateInTx(ctx, tx, in)
		if err != nil {
			return err
		}
		match = m
		return nil
	})
	if err != nil {
		return Match{}, err
	}
	s.StartActor(match)
	return match, nil
}

// StartActor spawns match's actor, if this Service has a registry to run it in. A Service built
// with a nil registry (every repository-level test in this package) simply does nothing here —
// there is no actor to leak, and no goroutine for a test's rolled-back transaction to race against
// after the test returns.
func (s *Service) StartActor(match Match) {
	if s.registry == nil {
		return
	}
	s.registry.Start(s.rootCtx, match, s.db, s.logger, nil)
}

// Get returns a match's current persisted state. It reads the database rather than messaging the
// actor: the actor persists every transition synchronously as it makes it (actor.go), so this is
// never more than one write behind, and a match with no running actor (not yet started, or already
// ended) is answered exactly the same way as one with an actor mid-transition.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Match, error) {
	return selectMatch(ctx, s.db, id)
}

// ListForUser returns one page of matches userID has participated in, newest first.
func (s *Service) ListForUser(ctx context.Context, userID uuid.UUID, cursorStr string, limit int) (items []Match, nextCursor string, err error) {
	if limit <= 0 || limit > MaxPageSize {
		limit = DefaultPageSize
	}

	cursor, err := decodeCursor(cursorStr)
	if err != nil {
		return nil, "", err
	}

	// One extra row than requested reveals whether a next page exists without a second round trip.
	fetched, err := selectMatchesByUser(ctx, s.db, userID, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}

	if len(fetched) > limit {
		last := fetched[limit-1]
		nextCursor = encodeCursor(last.CreatedAt, last.ID)
		fetched = fetched[:limit]
	}
	return fetched, nextCursor, nil
}
