package matches

import (
	"context"
	"log/slog"
	"time"

	"github.com/sassinzz13/billiards-online/game/protocol"
	"github.com/sassinzz13/billiards-online/platform/postgres"
)

// Event is what an actor reports as it moves through its lifecycle. Nothing consumes this over the
// wire yet — internal/realtime gains a connection registry and starts broadcasting these once
// Phase 9 gives clients something to react to (shot.request/shot.result). Until then, tests are the
// only observer, via the onEvent hook actors are constructed with.
type Event struct {
	Type  string // "match.starting" | "match.started" | "turn.started" | "match.ended"
	Match Match
}

// command is sent on an actor's inbox. There is exactly one today — cmdStop, an early, advisory
// end-of-match request. The channel exists now, ahead of a second command, because "one goroutine,
// one bounded inbox" is the ownership model every actor must follow from the start (MEMORY.md §12);
// a channel of capacity 1 added later would just be this same channel.
type command int

const cmdStop command = iota

// inboxCapacity bounds every actor's command queue, the same discipline platform/websocket's
// outbound queue follows: no unbounded channel anywhere (MEMORY.md §12).
const inboxCapacity = 32

// Actor owns one match's mutable state exclusively — no mutex on game state (MEMORY.md §12). It
// persists every transition synchronously as it happens, so a concurrent read (Service.Get, over
// HTTP) always sees the database rather than needing to message the actor for a snapshot.
type Actor struct {
	db      postgres.DB
	logger  *slog.Logger
	onEvent func(Event)
	inbox   chan command

	// Owned exclusively by run's goroutine after construction. Nothing else may read or write it.
	match Match
}

func newActor(match Match, db postgres.DB, logger *slog.Logger, onEvent func(Event)) *Actor {
	if onEvent == nil {
		onEvent = func(Event) {}
	}
	return &Actor{match: match, db: db, logger: logger, onEvent: onEvent, inbox: make(chan command, inboxCapacity)}
}

// Stop requests an early, advisory end to the match (e.g. every participant left). It is
// non-blocking and not a guarantee: ctx cancellation, not Stop, is the authoritative way an actor's
// run ends — Stop merely asks it to end sooner, as a Cancelled match rather than a background
// shutdown's Abandoned.
func (a *Actor) Stop() {
	select {
	case a.inbox <- cmdStop:
	default:
	}
}

// run is the actor's entire goroutine body: Waiting through Starting to InProgress, then the shot
// timer loop until ctx is cancelled or a command ends it early. Called only from Registry.Start.
func (a *Actor) run(ctx context.Context) {
	a.transition(ctx, StateStarting)

	turn := firstTurn()
	a.setTurn(ctx, turn)
	a.transition(ctx, StateInProgress)

	timer := time.NewTimer(shotTimerDuration(a.match))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			// The process is shutting down or the connection driving this match is gone with
			// nobody left to finish it. context.WithoutCancel: ctx is already done, so the final
			// persistence write needs its own, short-lived deadline rather than the cancelled one.
			bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			a.transition(bg, StateAbandoned)
			cancel()
			return

		case cmd := <-a.inbox:
			if cmd == cmdStop {
				a.transition(ctx, StateCancelled)
				return
			}

		case <-timer.C:
			next := nextTurn(a.match.Sides, *a.match.Turn)
			a.setTurn(ctx, next)
			timer.Reset(shotTimerDuration(a.match))
		}
	}
}

func shotTimerDuration(m Match) time.Duration {
	return time.Duration(m.ShotTimerSeconds) * time.Second
}

// transition validates and applies a state change, persists it, and emits the corresponding event.
// A transition Transition itself rejects is logged and otherwise ignored — it means run's own logic
// asked for something illegal, a bug in this file, not a runtime condition callers can trigger.
func (a *Actor) transition(ctx context.Context, to State) {
	if err := Transition(a.match.State, to); err != nil {
		a.logger.Error("actor requested illegal transition", "matchId", a.match.ID, "from", a.match.State, "to", to)
		return
	}
	a.match.State = to

	now := time.Now().UTC()
	var startedAt, completedAt *time.Time
	switch to {
	case StateInProgress:
		if a.match.StartedAt == nil {
			a.match.StartedAt = &now
		}
		startedAt = a.match.StartedAt
	case StateCompleted, StateCancelled, StateAbandoned:
		a.match.CompletedAt = &now
		completedAt = a.match.CompletedAt
		startedAt = a.match.StartedAt
	default:
		startedAt = a.match.StartedAt
	}

	if err := updateMatchState(ctx, a.db, a.match.ID, to, startedAt, completedAt); err != nil {
		a.logger.Error("persist match transition failed", "matchId", a.match.ID, "state", to, "error", err)
	}

	var eventType string
	switch to {
	case StateStarting:
		eventType = protocol.TypeMatchStarting
	case StateInProgress:
		eventType = protocol.TypeMatchStarted
	case StateCompleted, StateCancelled, StateAbandoned:
		eventType = protocol.TypeMatchEnded
	}
	a.onEvent(Event{Type: eventType, Match: a.match})
}

func (a *Actor) setTurn(ctx context.Context, turn TurnRef) {
	a.match.Turn = &turn
	now := time.Now().UTC()
	a.match.TurnStartedAt = &now

	if err := updateMatchTurn(ctx, a.db, a.match.ID, turn, now); err != nil {
		a.logger.Error("persist match turn failed", "matchId", a.match.ID, "error", err)
	}
	a.onEvent(Event{Type: protocol.TypeTurnStarted, Match: a.match})
}
