package rooms_test

import (
	"errors"
	"testing"

	"github.com/sassinzz13/billiards-online/internal/matches"
	"github.com/sassinzz13/billiards-online/internal/rooms"
	"github.com/sassinzz13/billiards-online/platform/postgres/pgtest"
)

// TestStartCreatesMatchAndClosesRoomInOneTransaction is the Phase 6 "Room -> match creation, in
// one transaction" checklist item, exercised at the rooms side that owns it (Start).
func TestStartCreatesMatchAndClosesRoomInOneTransaction(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)

	host := newUser(t, usersSvc)
	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{
		Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1,
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	opponent := newUser(t, usersSvc)
	if _, err := roomsSvc.Join(ctx, created.Room.ID, opponent.ID); err != nil {
		t.Fatalf("Join() = %v", err)
	}

	// Not ready yet — Start must refuse.
	if _, err := roomsSvc.Start(ctx, created.Room.ID, host.ID); !errors.Is(err, rooms.ErrNotAllReady) {
		t.Fatalf("Start() before anyone is ready = %v, want ErrNotAllReady", err)
	}

	if _, err := roomsSvc.SetReady(ctx, created.Room.ID, host.ID, true); err != nil {
		t.Fatalf("SetReady(host) = %v", err)
	}
	if _, err := roomsSvc.SetReady(ctx, created.Room.ID, opponent.ID, true); err != nil {
		t.Fatalf("SetReady(opponent) = %v", err)
	}

	// Not the host — Start must refuse regardless of readiness.
	if _, err := roomsSvc.Start(ctx, created.Room.ID, opponent.ID); !errors.Is(err, rooms.ErrNotHost) {
		t.Fatalf("Start() by non-host = %v, want ErrNotHost", err)
	}

	match, err := roomsSvc.Start(ctx, created.Room.ID, host.ID)
	if err != nil {
		t.Fatalf("Start() = %v", err)
	}
	if match.RoomID != created.Room.ID {
		t.Errorf("match.RoomID = %v, want %v", match.RoomID, created.Room.ID)
	}
	if match.Mode != matches.Mode1v1 {
		t.Errorf("match.Mode = %v, want 1v1", match.Mode)
	}
	if match.Sides[0].Players[0] != host.ID || match.Sides[1].Players[0] != opponent.ID {
		t.Errorf("match sides = %+v, want host on side A, opponent on side B", match.Sides)
	}

	detail, err := roomsSvc.Detail(ctx, created.Room.ID, host.ID)
	if err != nil {
		t.Fatalf("Detail() after Start = %v", err)
	}
	if detail.Room.State != rooms.StateClosed {
		t.Errorf("room state after Start = %v, want closed — the room has no authority over the match it just created", detail.Room.State)
	}

	// Already closed — a second Start must refuse rather than create a duplicate match.
	if _, err := roomsSvc.Start(ctx, created.Room.ID, host.ID); !errors.Is(err, rooms.ErrRoomClosed) {
		t.Errorf("second Start() = %v, want ErrRoomClosed", err)
	}
}

func TestStartRejectsAnUnfilledRoom(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)

	host := newUser(t, usersSvc)
	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{
		Visibility: rooms.VisibilityPublic, Mode: rooms.Mode2v2,
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if _, err := roomsSvc.SetReady(ctx, created.Room.ID, host.ID, true); err != nil {
		t.Fatalf("SetReady() = %v", err)
	}

	if _, err := roomsSvc.Start(ctx, created.Room.ID, host.ID); !errors.Is(err, rooms.ErrRoomNotFull) {
		t.Errorf("Start() on a 1-of-4 room = %v, want ErrRoomNotFull", err)
	}
}
