package matches

import (
	"context"
	"log/slog"
	"sync"

	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/platform/postgres"
)

// Registry holds every active match's actor, `map[uuid.UUID]*Actor` behind a `sync.RWMutex` — the
// exact shape MEMORY.md §12 specifies. It assumes a single server instance owns every match
// (recorded as risk R2); multi-instance would need sticky routing or a match locator.
type Registry struct {
	mu     sync.RWMutex
	actors map[uuid.UUID]*Actor
	wg     sync.WaitGroup
}

func NewRegistry() *Registry {
	return &Registry{actors: make(map[uuid.UUID]*Actor)}
}

// Start spawns the one goroutine that will own match's state for its entire life, registers its
// actor, and returns it. ctx should be the process's own shutdown context (see
// internal/realtime.NewGateway's doc comment for the same reasoning): a match actor's lifetime
// must end the instant the server starts shutting down, not linger until an unrelated timeout.
//
// The actor removes itself from the registry when run returns, so Get and Len never see a stale
// entry for a match that has already ended (verified by TestActorRemovesItselfFromRegistryOnExit).
func (r *Registry) Start(ctx context.Context, match Match, db postgres.DB, logger *slog.Logger, onEvent func(Event)) *Actor {
	a := newActor(match, db, logger, onEvent)

	r.mu.Lock()
	r.actors[match.ID] = a
	r.mu.Unlock()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer r.remove(match.ID)
		a.run(ctx)
	}()

	return a
}

func (r *Registry) remove(id uuid.UUID) {
	r.mu.Lock()
	delete(r.actors, id)
	r.mu.Unlock()
}

// Get returns the running actor for a match, if it currently has one. A match with no running
// actor is not necessarily an error — it may not have started yet, or may already have ended; both
// are ordinary states a caller finds out about from Service.Get's persisted row instead.
func (r *Registry) Get(id uuid.UUID) (*Actor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.actors[id]
	return a, ok
}

// Len reports how many actors are currently running. Exists for tests and observability, not for
// any decision this package itself makes.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.actors)
}

// Wait blocks until every actor has exited or ctx ends, whichever comes first — used at shutdown
// (apps/server/cmd/server/main.go) to give in-flight actors the same shared shutdown budget the two
// HTTP listeners get, rather than leaving them to be cut off mid-persist by process exit.
func (r *Registry) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
}
