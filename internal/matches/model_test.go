package matches_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/internal/matches"
)

// allStates lists every State so the exhaustive transition test below can check every ordered pair
// — not just the ones this file happens to think of — against the diagram in MEMORY.md §13.
var allStates = []matches.State{
	matches.StateWaiting, matches.StateStarting, matches.StateInProgress,
	matches.StatePaused, matches.StateCompleted, matches.StateCancelled, matches.StateAbandoned,
}

// legal mirrors the diagram in MEMORY.md §13 and PLAN.md's Phase 6 goal exactly. Any pair not
// listed here must be rejected — TestTransitionExhaustive checks all of them, not just these.
var legal = map[[2]matches.State]bool{
	{matches.StateWaiting, matches.StateStarting}:     true,
	{matches.StateStarting, matches.StateInProgress}:  true,
	{matches.StateStarting, matches.StateCancelled}:   true,
	{matches.StateStarting, matches.StateAbandoned}:   true,
	{matches.StateInProgress, matches.StateCompleted}: true,
	{matches.StateInProgress, matches.StatePaused}:    true,
	{matches.StateInProgress, matches.StateCancelled}: true,
	{matches.StateInProgress, matches.StateAbandoned}: true,
	{matches.StatePaused, matches.StateInProgress}:    true,
	{matches.StatePaused, matches.StateCancelled}:     true,
	{matches.StatePaused, matches.StateAbandoned}:     true,
}

// TestTransitionExhaustive is the Phase 6 exit criterion: every illegal state transition is
// rejected by matches.Transition, checked over the full state x state matrix rather than a
// hand-picked sample, so a new state added later without updating the diagram fails loudly here
// instead of silently permitting an edge nobody intended.
func TestTransitionExhaustive(t *testing.T) {
	for _, from := range allStates {
		for _, to := range allStates {
			wantOK := legal[[2]matches.State{from, to}]
			err := matches.Transition(from, to)
			gotOK := err == nil

			if gotOK != wantOK {
				t.Errorf("Transition(%s, %s) legal = %v, want %v", from, to, gotOK, wantOK)
			}
			if !wantOK && !errors.Is(err, matches.ErrIllegalTransition) {
				t.Errorf("Transition(%s, %s) error = %v, want ErrIllegalTransition", from, to, err)
			}
		}
	}
}

func TestTerminalStatesHaveNoLegalTransition(t *testing.T) {
	for _, terminal := range []matches.State{matches.StateCompleted, matches.StateCancelled, matches.StateAbandoned} {
		if !terminal.Terminal() {
			t.Errorf("%s.Terminal() = false, want true", terminal)
		}
		for _, to := range allStates {
			if err := matches.Transition(terminal, to); err == nil {
				t.Errorf("Transition(%s, %s) succeeded, want every transition out of a terminal state to fail", terminal, to)
			}
		}
	}
}

func player() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// TestSidesSupport1v1And2v2WithoutCodeChange is the other half of the Phase 6 exit criterion: the
// sides model already supports 2v2, exercised here as ordinary input, not a special case.
func TestSidesSupport1v1And2v2WithoutCodeChange(t *testing.T) {
	tests := []struct {
		name string
		mode matches.Mode
		a, b []uuid.UUID
	}{
		{"1v1", matches.Mode1v1, []uuid.UUID{player()}, []uuid.UUID{player()}},
		{"2v2", matches.Mode2v2, []uuid.UUID{player(), player()}, []uuid.UUID{player(), player()}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := matches.CreateInput{
				RoomID: uuid.Must(uuid.NewV7()), Mode: tt.mode, Ruleset: "8ball", ShotTimerSeconds: 30,
				Sides: [2]matches.Side{
					{ID: matches.SideA, Players: tt.a},
					{ID: matches.SideB, Players: tt.b},
				},
			}
			if err := in.Validate(); err != nil {
				t.Errorf("valid %s input rejected: %v", tt.name, err)
			}
		})
	}
}

func TestCreateInputRejectsWrongPlayerCountForMode(t *testing.T) {
	in := matches.CreateInput{
		RoomID: uuid.Must(uuid.NewV7()), Mode: matches.Mode1v1, Ruleset: "8ball", ShotTimerSeconds: 30,
		Sides: [2]matches.Side{
			{ID: matches.SideA, Players: []uuid.UUID{player(), player()}}, // 2 players in a 1v1
			{ID: matches.SideB, Players: []uuid.UUID{player()}},
		},
	}
	if err := in.Validate(); !errors.Is(err, matches.ErrInvalidSides) {
		t.Errorf("error = %v, want ErrInvalidSides", err)
	}
}
