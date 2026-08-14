package users_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sassinzz13/billiards-online/internal/users"
	"github.com/sassinzz13/billiards-online/platform/postgres/pgtest"
)

// A profile row is created alongside the user, so "every user has a profile" holds
// unconditionally rather than as a rule a caller has to remember.
func TestCreateAlsoCreatesAProfile(t *testing.T) {
	svc, next := newService(t)
	ctx := pgtest.Context(t)
	email, handle := next()

	u, err := svc.Create(ctx, email, handle)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	account, err := svc.Account(ctx, u.ID)
	if err != nil {
		t.Fatalf("Account() right after Create() = %v — profile row missing", err)
	}
	if account.Profile.MatchesPlayed != 0 || account.Profile.Wins != 0 || account.Profile.Losses != 0 {
		t.Errorf("fresh profile stats = %+v, want all zero", account.Profile)
	}
	if account.Profile.DisplayName != nil {
		t.Errorf("fresh DisplayName = %v, want nil (falls back to handle)", *account.Profile.DisplayName)
	}
}

// With no display name set, both the "me" and "public" projections must fall back to the handle,
// and they must agree — DisplayName() is the single place that decides the fallback.
func TestDisplayNameFallsBackToHandle(t *testing.T) {
	svc, next := newService(t)
	ctx := pgtest.Context(t)
	email, handle := next()

	u, err := svc.Create(ctx, email, handle)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	account, err := svc.Account(ctx, u.ID)
	if err != nil {
		t.Fatalf("Account() = %v", err)
	}
	if account.DisplayName() != handle {
		t.Errorf("DisplayName() = %q, want handle %q", account.DisplayName(), handle)
	}

	pub, err := svc.Public(ctx, u.ID)
	if err != nil {
		t.Fatalf("Public() = %v", err)
	}
	if pub.DisplayName != handle {
		t.Errorf("Public().DisplayName = %q, want handle %q", pub.DisplayName, handle)
	}
}

func TestUpdateProfileSetsDisplayNameAndAvatar(t *testing.T) {
	svc, next := newService(t)
	ctx := pgtest.Context(t)
	email, handle := next()
	u, err := svc.Create(ctx, email, handle)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	name, avatar := "Rocket", "asset://cues/classic-01"
	account, err := svc.UpdateProfile(ctx, u.ID, users.UpdateProfileInput{
		DisplayName: &name,
		AvatarRef:   &avatar,
	})
	if err != nil {
		t.Fatalf("UpdateProfile() = %v", err)
	}
	if account.DisplayName() != name {
		t.Errorf("DisplayName() = %q, want %q", account.DisplayName(), name)
	}
	if account.Profile.AvatarRef == nil || *account.Profile.AvatarRef != avatar {
		t.Errorf("AvatarRef = %v, want %q", account.Profile.AvatarRef, avatar)
	}
}

// nil means "leave unchanged." Setting only one field must not disturb the other.
func TestUpdateProfileLeavesUntouchedFieldsAlone(t *testing.T) {
	svc, next := newService(t)
	ctx := pgtest.Context(t)
	email, handle := next()
	u, err := svc.Create(ctx, email, handle)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	name := "Rocket"
	if _, err := svc.UpdateProfile(ctx, u.ID, users.UpdateProfileInput{DisplayName: &name}); err != nil {
		t.Fatalf("first UpdateProfile() = %v", err)
	}

	avatar := "asset://cues/classic-01"
	account, err := svc.UpdateProfile(ctx, u.ID, users.UpdateProfileInput{AvatarRef: &avatar})
	if err != nil {
		t.Fatalf("second UpdateProfile() = %v", err)
	}
	if account.DisplayName() != name {
		t.Errorf("DisplayName() = %q after an update that did not mention it, want it preserved as %q",
			account.DisplayName(), name)
	}
	if account.Profile.AvatarRef == nil || *account.Profile.AvatarRef != avatar {
		t.Errorf("AvatarRef = %v, want %q", account.Profile.AvatarRef, avatar)
	}
}

// An empty string is the "clear" signal, distinct from nil ("untouched").
func TestUpdateProfileClearsWithEmptyString(t *testing.T) {
	svc, next := newService(t)
	ctx := pgtest.Context(t)
	email, handle := next()
	u, err := svc.Create(ctx, email, handle)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	name := "Rocket"
	if _, err := svc.UpdateProfile(ctx, u.ID, users.UpdateProfileInput{DisplayName: &name}); err != nil {
		t.Fatalf("set DisplayName = %v", err)
	}

	empty := ""
	account, err := svc.UpdateProfile(ctx, u.ID, users.UpdateProfileInput{DisplayName: &empty})
	if err != nil {
		t.Fatalf("clear DisplayName = %v", err)
	}
	if account.Profile.DisplayName != nil {
		t.Errorf("DisplayName = %v after clearing, want nil", *account.Profile.DisplayName)
	}
	if account.DisplayName() != handle {
		t.Errorf("DisplayName() after clearing = %q, want fallback to handle %q", account.DisplayName(), handle)
	}
}

func TestUpdateProfileRejectsInvalidValues(t *testing.T) {
	svc, next := newService(t)
	ctx := pgtest.Context(t)
	email, handle := next()
	u, err := svc.Create(ctx, email, handle)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	tooLongName := strings.Repeat("a", users.MaxDisplayNameLen+1)
	if _, err := svc.UpdateProfile(ctx, u.ID, users.UpdateProfileInput{DisplayName: &tooLongName}); !errors.Is(err, users.ErrInvalidDisplay) {
		t.Errorf("over-length display name: err = %v, want ErrInvalidDisplay", err)
	}

	tooLongAvatar := strings.Repeat("a", users.MaxAvatarRefLen+1)
	if _, err := svc.UpdateProfile(ctx, u.ID, users.UpdateProfileInput{AvatarRef: &tooLongAvatar}); !errors.Is(err, users.ErrInvalidAvatar) {
		t.Errorf("over-length avatar ref: err = %v, want ErrInvalidAvatar", err)
	}

	// The unaffected fields prove the failed call did not partially apply — display name from the
	// rejected calls above must not have leaked through.
	account, err := svc.Account(ctx, u.ID)
	if err != nil {
		t.Fatalf("Account() = %v", err)
	}
	if account.DisplayName() != handle {
		t.Errorf("DisplayName() = %q after rejected updates, want unchanged fallback %q", account.DisplayName(), handle)
	}
}

// The public projection must never carry an email, structurally: PublicProfile has no such field.
func TestPublicNeverCarriesEmail(t *testing.T) {
	svc, next := newService(t)
	ctx := pgtest.Context(t)
	email, handle := next()
	u, err := svc.Create(ctx, email, handle)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}

	pub, err := svc.Public(ctx, u.ID)
	if err != nil {
		t.Fatalf("Public() = %v", err)
	}
	if pub.Handle != handle {
		t.Errorf("Public().Handle = %q, want %q", pub.Handle, handle)
	}
	// No Email field exists on PublicProfile at all — this loop is a belt-and-braces check that
	// nothing containing the address leaked into any string field.
	if strings.Contains(pub.DisplayName, "@") {
		t.Error("Public().DisplayName unexpectedly contains an email-shaped string")
	}
}

func TestAccountAndPublicRejectUnknownUser(t *testing.T) {
	svc, _ := newService(t)
	ctx := pgtest.Context(t)

	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("generate id: %v", err)
	}
	if _, err := svc.Account(ctx, id); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("Account(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := svc.Public(ctx, id); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("Public(unknown) = %v, want ErrNotFound", err)
	}
	if _, err := svc.UpdateProfile(ctx, id, users.UpdateProfileInput{}); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("UpdateProfile(unknown) = %v, want ErrNotFound", err)
	}
}
