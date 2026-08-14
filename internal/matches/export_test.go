package matches

// Exposed to matches_test (the external test package) so turn_test.go can exercise the unexported
// sequencing function directly, without needing a full actor and database to do it.
var NextTurnForTest = nextTurn
