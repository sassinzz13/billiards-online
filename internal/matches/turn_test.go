package matches_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/internal/matches"
)

func twoPlayerSides() [2]matches.Side {
	return [2]matches.Side{
		{ID: matches.SideA, Players: []uuid.UUID{player(), player()}},
		{ID: matches.SideB, Players: []uuid.UUID{player(), player()}},
	}
}

func onePlayerSides() [2]matches.Side {
	return [2]matches.Side{
		{ID: matches.SideA, Players: []uuid.UUID{player()}},
		{ID: matches.SideB, Players: []uuid.UUID{player()}},
	}
}

// TestTurnAdvance1v1 covers the simplest case: two sides of one, so a timeout always hands the
// turn to the other side.
func TestTurnAdvance1v1(t *testing.T) {
	sides := onePlayerSides()

	got := matches.NextTurnForTest(sides, matches.TurnRef{Side: matches.SideA, PlayerIdx: 0})
	want := matches.TurnRef{Side: matches.SideB, PlayerIdx: 0}
	if got != want {
		t.Errorf("nextTurn = %+v, want %+v", got, want)
	}

	got = matches.NextTurnForTest(sides, want)
	want = matches.TurnRef{Side: matches.SideA, PlayerIdx: 0}
	if got != want {
		t.Errorf("nextTurn = %+v, want %+v", got, want)
	}
}

// TestTurnAdvance2v2 walks all four seats in order: A0, B0, A1, B1, back to A0 — this is the
// sides model supporting 2v2 without any special-cased code path (MEMORY.md §14).
func TestTurnAdvance2v2(t *testing.T) {
	sides := twoPlayerSides()

	sequence := []matches.TurnRef{
		{Side: matches.SideA, PlayerIdx: 0},
		{Side: matches.SideB, PlayerIdx: 0},
		{Side: matches.SideA, PlayerIdx: 1},
		{Side: matches.SideB, PlayerIdx: 1},
		{Side: matches.SideA, PlayerIdx: 0}, // wraps back to the start
	}

	current := sequence[0]
	for i := 1; i < len(sequence); i++ {
		current = matches.NextTurnForTest(sides, current)
		if current != sequence[i] {
			t.Fatalf("step %d: nextTurn = %+v, want %+v", i, current, sequence[i])
		}
	}
}
