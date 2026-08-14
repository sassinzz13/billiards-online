package matches

// firstTurn is whoever the match starts with: side A, the first player on it. Simple and
// arbitrary — nothing in the constitution assigns the break by any other rule yet (that is
// game/rules' job once 8-ball's break rules exist, Phase 11).
func firstTurn() TurnRef {
	return TurnRef{Side: SideA, PlayerIdx: 0}
}

// nextTurn advances a timed-out turn to the next player. Sides strictly alternate every turn — A0,
// B0, A1, B1, A0, ... — with each side's own players cycling in slot order underneath that
// alternation, matching how 2v2 doubles actually plays.
//
// This is deliberately not "whoever's turn it is after a foul or a potted ball" — that decision
// needs physics and rule state that do not exist until Phase 9/11, and MEMORY.md §14 reserves it
// for game/rules once it does. What this function computes is purely mechanical sequencing: given
// nobody shot before the clock ran out, whose clock starts next. It never decides legality, a foul,
// or a win — only order.
//
// The turn number n implied by (side, playerIdx) is n = playerIdx*2 + side (both sides always have
// the same PlayersPerSide, so this is invertible): incrementing n and decomposing it back into
// (side, playerIdx) is what produces the alternation without needing to remember any history beyond
// the current TurnRef.
func nextTurn(sides [2]Side, current TurnRef) TurnRef {
	n := current.PlayerIdx*2 + int(current.Side)
	n++

	side := SideID(n % 2)
	playerIdx := (n / 2) % len(sides[side].Players)
	return TurnRef{Side: side, PlayerIdx: playerIdx}
}
