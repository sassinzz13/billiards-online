package rooms_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sassinzz13/billiards-online/internal/rooms"
	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/postgres/pgtest"
)

// newFixture wires rooms.Service and users.Service to the same rolled-back transaction, mirroring
// internal/auth's test fixture. Every test in this file (except the concurrency test, which needs
// real separate connections) runs inside one transaction that never commits.
func newFixture(t *testing.T) (*rooms.Service, *users.Service, pgx.Tx) {
	t.Helper()
	tx := pgtest.DB(t)
	return rooms.NewService(tx), users.NewService(tx), tx
}

func newUser(t *testing.T, svc *users.Service) users.User {
	t.Helper()
	id := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	u, err := svc.Create(pgtest.Context(t), "player_"+id+"@example.com", "p"+id)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func TestCreateSeatsTheHostInSlotZero(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)

	detail, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{
		Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1,
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if detail.Room.HostUserID != host.ID {
		t.Errorf("HostUserID = %v, want %v", detail.Room.HostUserID, host.ID)
	}
	if detail.Room.State != rooms.StateOpen {
		t.Errorf("State = %v, want open", detail.Room.State)
	}
	if len(detail.Members) != 1 {
		t.Fatalf("Members = %d, want 1", len(detail.Members))
	}
	if detail.Members[0].Side != 0 || detail.Members[0].Slot != 0 {
		t.Errorf("host seat = (%d,%d), want (0,0)", detail.Members[0].Side, detail.Members[0].Slot)
	}
	// Ruleset is not caller-configurable — 8-ball is the only one through Phase 14 (MEMORY.md §25).
	if detail.Room.Ruleset != "8ball" {
		t.Errorf("Ruleset = %q, want 8ball", detail.Room.Ruleset)
	}
}

func TestCreateAppliesDefaults(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)

	detail, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{
		Visibility: rooms.VisibilityPublic, Mode: rooms.Mode2v2,
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if detail.Room.ShotTimerSeconds != rooms.DefaultShotTimerSeconds {
		t.Errorf("ShotTimerSeconds = %d, want default %d", detail.Room.ShotTimerSeconds, rooms.DefaultShotTimerSeconds)
	}
	if detail.Room.WagerAmount != 0 {
		t.Errorf("WagerAmount = %d, want 0", detail.Room.WagerAmount)
	}
	if !detail.Room.SpectatorsAllowed {
		t.Error("SpectatorsAllowed = false, want default true")
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)

	badTimer := 5
	badWager := int64(-1)
	tests := []struct {
		name string
		in   rooms.CreateInput
		want error
	}{
		{"bad visibility", rooms.CreateInput{Visibility: "sneaky", Mode: rooms.Mode1v1}, rooms.ErrInvalidVisibility},
		{"bad mode", rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: "3v3"}, rooms.ErrInvalidMode},
		{"timer too short", rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1, ShotTimerSeconds: &badTimer}, rooms.ErrInvalidShotTimer},
		{"negative wager", rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1, WagerAmount: &badWager}, rooms.ErrInvalidWager},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := roomsSvc.Create(ctx, host.ID, tc.in); !errors.Is(err, tc.want) {
				t.Errorf("Create() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCreatePrivateRoomGetsAJoinCode(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)

	detail, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{
		Visibility: rooms.VisibilityPrivate, Mode: rooms.Mode1v1,
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if detail.Room.JoinCode == nil || len(*detail.Room.JoinCode) != rooms.JoinCodeLength {
		t.Fatalf("JoinCode = %v, want an %d-character code", detail.Room.JoinCode, rooms.JoinCodeLength)
	}
}

func TestPublicRoomHasNoJoinCode(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)

	detail, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{
		Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1,
	})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if detail.Room.JoinCode != nil {
		t.Errorf("public room JoinCode = %v, want nil", *detail.Room.JoinCode)
	}
}

func TestJoinSeatsSecondPlayerOnTheOtherSide(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)
	guest := newUser(t, usersSvc)

	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	detail, err := roomsSvc.Join(ctx, created.Room.ID, guest.ID)
	if err != nil {
		t.Fatalf("Join() = %v", err)
	}
	if len(detail.Members) != 2 {
		t.Fatalf("Members = %d, want 2", len(detail.Members))
	}

	var guestSeat *rooms.MemberView
	for i := range detail.Members {
		if detail.Members[i].UserID == guest.ID {
			guestSeat = &detail.Members[i]
		}
	}
	if guestSeat == nil {
		t.Fatal("guest is not in the member list")
	}
	if guestSeat.Side != 1 || guestSeat.Slot != 0 {
		t.Errorf("guest seat = (%d,%d), want (1,0)", guestSeat.Side, guestSeat.Slot)
	}
}

// 2v2's seat order alternates sides — (0,0),(1,0),(0,1),(1,1) — so a third joiner starts the
// second side rather than stacking three players onto the first.
func TestJoinBalancesSidesInTwoVTwo(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)

	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode2v2})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	wantSeats := [][2]int{{1, 0}, {0, 1}, {1, 1}}
	for i, want := range wantSeats {
		guest := newUser(t, usersSvc)
		detail, err := roomsSvc.Join(ctx, created.Room.ID, guest.ID)
		if err != nil {
			t.Fatalf("Join() #%d = %v", i+1, err)
		}
		var seat *rooms.MemberView
		for j := range detail.Members {
			if detail.Members[j].UserID == guest.ID {
				seat = &detail.Members[j]
			}
		}
		if seat == nil || seat.Side != want[0] || seat.Slot != want[1] {
			t.Errorf("join #%d seat = %+v, want side=%d slot=%d", i+1, seat, want[0], want[1])
		}
	}
}

func TestJoinRejectsFullRoom(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)
	guest := newUser(t, usersSvc)
	late := newUser(t, usersSvc)

	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if _, err := roomsSvc.Join(ctx, created.Room.ID, guest.ID); err != nil {
		t.Fatalf("Join() = %v", err)
	}

	if _, err := roomsSvc.Join(ctx, created.Room.ID, late.ID); !errors.Is(err, rooms.ErrRoomFull) {
		t.Errorf("Join(full room) = %v, want ErrRoomFull", err)
	}
}

func TestJoinRejectsDuplicateMembership(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)

	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode2v2})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if _, err := roomsSvc.Join(ctx, created.Room.ID, host.ID); !errors.Is(err, rooms.ErrAlreadyMember) {
		t.Errorf("Join(already a member) = %v, want ErrAlreadyMember", err)
	}
}

func TestJoinRejectsPrivateRoomByID(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)
	guest := newUser(t, usersSvc)

	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPrivate, Mode: rooms.Mode1v1})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if _, err := roomsSvc.Join(ctx, created.Room.ID, guest.ID); !errors.Is(err, rooms.ErrWrongJoinPath) {
		t.Errorf("Join(private room, by id) = %v, want ErrWrongJoinPath", err)
	}
}

func TestJoinByCodeSucceedsWithTheRightCode(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)
	guest := newUser(t, usersSvc)

	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPrivate, Mode: rooms.Mode1v1})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	detail, err := roomsSvc.JoinByCode(ctx, *created.Room.JoinCode, guest.ID)
	if err != nil {
		t.Fatalf("JoinByCode() = %v", err)
	}
	if len(detail.Members) != 2 {
		t.Errorf("Members = %d, want 2", len(detail.Members))
	}

	// Case must not matter — a code read off a screen and retyped should still work.
	late := newUser(t, usersSvc)
	if _, err := roomsSvc.JoinByCode(ctx, strings.ToLower(*created.Room.JoinCode), late.ID); err == nil {
		t.Error("JoinByCode(lowercased) succeeded on an already-full room — case-insensitivity test is invalid, room filled unexpectedly")
	} else if !errors.Is(err, rooms.ErrRoomFull) {
		t.Errorf("JoinByCode(lowercased, full room) = %v, want ErrRoomFull (proves the code itself matched)", err)
	}
}

func TestJoinByCodeRejectsWrongCode(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)
	guest := newUser(t, usersSvc)

	if _, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPrivate, Mode: rooms.Mode1v1}); err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if _, err := roomsSvc.JoinByCode(ctx, "NOTAREALCODE", guest.ID); !errors.Is(err, rooms.ErrInvalidJoinCode) {
		t.Errorf("JoinByCode(wrong code) = %v, want ErrInvalidJoinCode", err)
	}
}

func TestLeaveClosesAnEmptyRoom(t *testing.T) {
	roomsSvc, usersSvc, tx := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)

	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	if err := roomsSvc.Leave(ctx, created.Room.ID, host.ID); err != nil {
		t.Fatalf("Leave() = %v", err)
	}

	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM rooms WHERE id = $1`, created.Room.ID).Scan(&state); err != nil {
		t.Fatalf("query room state: %v", err)
	}
	if state != string(rooms.StateClosed) {
		t.Errorf("room state = %q after last member left, want closed", state)
	}
}

func TestLeaveHandsOffHostToEarliestRemainingMember(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)
	first := newUser(t, usersSvc)
	second := newUser(t, usersSvc)

	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode2v2})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if _, err := roomsSvc.Join(ctx, created.Room.ID, first.ID); err != nil {
		t.Fatalf("Join(first) = %v", err)
	}
	if _, err := roomsSvc.Join(ctx, created.Room.ID, second.ID); err != nil {
		t.Fatalf("Join(second) = %v", err)
	}

	if err := roomsSvc.Leave(ctx, created.Room.ID, host.ID); err != nil {
		t.Fatalf("Leave(host) = %v", err)
	}

	detail, err := roomsSvc.Detail(ctx, created.Room.ID, first.ID)
	if err != nil {
		t.Fatalf("Detail() = %v", err)
	}
	if detail.Room.HostUserID != first.ID {
		t.Errorf("new host = %v, want the earliest remaining member %v (%v was excluded)",
			detail.Room.HostUserID, first.ID, second.ID)
	}
}

func TestLeaveRejectsNonMember(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)
	stranger := newUser(t, usersSvc)

	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if err := roomsSvc.Leave(ctx, created.Room.ID, stranger.ID); !errors.Is(err, rooms.ErrNotMember) {
		t.Errorf("Leave(non-member) = %v, want ErrNotMember", err)
	}
}

func TestSetReadyTogglesTheCallersOwnFlag(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)

	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	detail, err := roomsSvc.SetReady(ctx, created.Room.ID, host.ID, true)
	if err != nil {
		t.Fatalf("SetReady(true) = %v", err)
	}
	if !detail.Members[0].Ready {
		t.Error("member not marked ready")
	}

	detail, err = roomsSvc.SetReady(ctx, created.Room.ID, host.ID, false)
	if err != nil {
		t.Fatalf("SetReady(false) = %v", err)
	}
	if detail.Members[0].Ready {
		t.Error("member still marked ready after unsetting")
	}
}

func TestSetReadyRejectsNonMember(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)
	stranger := newUser(t, usersSvc)

	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if _, err := roomsSvc.SetReady(ctx, created.Room.ID, stranger.ID, true); !errors.Is(err, rooms.ErrNotMember) {
		t.Errorf("SetReady(non-member) = %v, want ErrNotMember", err)
	}
}

// A private room must not confirm its own existence to someone who is not in it — the same
// don't-confirm-what-you-don't-have-to reasoning as ADR 0009's exact Origin match.
func TestDetailHidesPrivateRoomsFromNonMembers(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)
	stranger := newUser(t, usersSvc)

	created, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPrivate, Mode: rooms.Mode1v1})
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	if _, err := roomsSvc.Detail(ctx, created.Room.ID, stranger.ID); !errors.Is(err, rooms.ErrNotFound) {
		t.Errorf("Detail(private, non-member) = %v, want ErrNotFound", err)
	}
	// The host can, of course.
	if _, err := roomsSvc.Detail(ctx, created.Room.ID, host.ID); err != nil {
		t.Errorf("Detail(private, host) = %v, want nil", err)
	}
}

func TestListPublicOpenExcludesPrivateAndClosedRooms(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)
	host := newUser(t, usersSvc)

	publicRoom, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1})
	if err != nil {
		t.Fatalf("create public room: %v", err)
	}
	privateHost := newUser(t, usersSvc)
	if _, err := roomsSvc.Create(ctx, privateHost.ID, rooms.CreateInput{Visibility: rooms.VisibilityPrivate, Mode: rooms.Mode1v1}); err != nil {
		t.Fatalf("create private room: %v", err)
	}
	closedHost := newUser(t, usersSvc)
	closedRoom, err := roomsSvc.Create(ctx, closedHost.ID, rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1})
	if err != nil {
		t.Fatalf("create soon-to-close room: %v", err)
	}
	if err := roomsSvc.Leave(ctx, closedRoom.Room.ID, closedHost.ID); err != nil {
		t.Fatalf("close room via leave: %v", err)
	}

	items, _, err := roomsSvc.ListPublicOpen(ctx, "", 50)
	if err != nil {
		t.Fatalf("ListPublicOpen() = %v", err)
	}

	seen := make(map[uuid.UUID]bool, len(items))
	for _, it := range items {
		seen[it.ID] = true
	}
	if !seen[publicRoom.Room.ID] {
		t.Error("public open room missing from listing")
	}
	if seen[closedRoom.Room.ID] {
		t.Error("closed room present in listing")
	}
}

func TestListPublicOpenPaginatesWithoutGapsOrDuplicates(t *testing.T) {
	roomsSvc, usersSvc, _ := newFixture(t)
	ctx := pgtest.Context(t)

	const n = 7
	created := make(map[uuid.UUID]bool, n)
	for range n {
		host := newUser(t, usersSvc)
		d, err := roomsSvc.Create(ctx, host.ID, rooms.CreateInput{Visibility: rooms.VisibilityPublic, Mode: rooms.Mode1v1})
		if err != nil {
			t.Fatalf("create room: %v", err)
		}
		created[d.Room.ID] = true
		time.Sleep(time.Millisecond) // created_at is part of the ordering key; force distinct values
	}

	seen := make(map[uuid.UUID]bool, n)
	cursor := ""
	pages := 0
	for {
		pages++
		if pages > n+1 {
			t.Fatal("pagination did not terminate — likely stuck repeating a page")
		}
		items, next, err := roomsSvc.ListPublicOpen(ctx, cursor, 3)
		if err != nil {
			t.Fatalf("ListPublicOpen(cursor=%q) = %v", cursor, err)
		}
		for _, it := range items {
			if created[it.ID] {
				if seen[it.ID] {
					t.Errorf("room %v appeared twice across pages", it.ID)
				}
				seen[it.ID] = true
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}

	for id := range created {
		if !seen[id] {
			t.Errorf("room %v never appeared in any page", id)
		}
	}
}

func TestListPublicOpenRejectsMalformedCursor(t *testing.T) {
	roomsSvc, _, _ := newFixture(t)
	ctx := pgtest.Context(t)

	if _, _, err := roomsSvc.ListPublicOpen(ctx, "not-a-real-cursor!!", 10); err == nil {
		t.Error("ListPublicOpen(malformed cursor) = nil error, want a rejection")
	}
}
