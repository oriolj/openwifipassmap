package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/oriolj/openwifipassmap/migrations"
)

func openTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	if err := s.Migrate(ctx, migrations.Schema); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s, ctx
}

func TestEnsureUserEmailBackfill(t *testing.T) {
	s, ctx := openTestStore(t)

	// A pre-email account (blank email) and one that already has a real address.
	legacy, err := s.CreateUser(ctx, "legacy", "", "hash")
	if err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	real, err := s.CreateUser(ctx, "real", "real@example.com", "hash")
	if err != nil {
		t.Fatalf("create real: %v", err)
	}

	if err := s.EnsureUserEmail(ctx, "backfill@example.com"); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	got, err := s.GetUserByID(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("get legacy: %v", err)
	}
	if got.Email != "backfill@example.com" {
		t.Errorf("legacy email = %q, want backfill@example.com", got.Email)
	}
	got, err = s.GetUserByID(ctx, real.ID)
	if err != nil {
		t.Fatalf("get real: %v", err)
	}
	if got.Email != "real@example.com" {
		t.Errorf("real email = %q, should be untouched", got.Email)
	}

	// Idempotent: a second run with no blank emails left changes nothing.
	if err := s.EnsureUserEmail(ctx, "other@example.com"); err != nil {
		t.Fatalf("ensure again: %v", err)
	}
	got, _ = s.GetUserByID(ctx, legacy.ID)
	if got.Email != "backfill@example.com" {
		t.Errorf("legacy email changed on re-run: %q", got.Email)
	}
}

func TestGetUsersByEmailMultiple(t *testing.T) {
	s, ctx := openTestStore(t)
	if _, err := s.CreateUser(ctx, "a", "shared@example.com", "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "b", "SHARED@example.com", "h"); err != nil { // case-insensitive
		t.Fatal(err)
	}
	users, err := s.GetUsersByEmail(ctx, "shared@example.com")
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}
}

func TestPasswordResetTokenLifecycle(t *testing.T) {
	s, ctx := openTestStore(t)
	u, err := s.CreateUser(ctx, "resetter", "r@example.com", "oldhash")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	token, err := s.CreatePasswordResetToken(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	// First consume succeeds and returns the owner.
	uid, err := s.ConsumePasswordResetToken(ctx, token)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if uid != u.ID {
		t.Errorf("consume returned %q, want %q", uid, u.ID)
	}

	// Single-use: the same token is now gone.
	if _, err := s.ConsumePasswordResetToken(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Errorf("second consume err = %v, want ErrNotFound", err)
	}

	// Expired tokens are not accepted.
	expired, err := s.CreatePasswordResetToken(ctx, u.ID, -time.Minute)
	if err != nil {
		t.Fatalf("create expired: %v", err)
	}
	if _, err := s.ConsumePasswordResetToken(ctx, expired); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired consume err = %v, want ErrNotFound", err)
	}

	// The actual password update lands.
	if err := s.UpdateUserPassword(ctx, u.ID, "newhash"); err != nil {
		t.Fatalf("update password: %v", err)
	}
	got, _ := s.GetUserByID(ctx, u.ID)
	if got.PasswordHash != "newhash" {
		t.Errorf("password hash = %q, want newhash", got.PasswordHash)
	}
}

func TestDeleteUserSessions(t *testing.T) {
	s, ctx := openTestStore(t)
	u, err := s.CreateUser(ctx, "sess", "s@example.com", "h")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.CreateSession(ctx, u.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserForToken(ctx, sess.Token); err != nil {
		t.Fatalf("token should resolve before revoke: %v", err)
	}
	if err := s.DeleteUserSessions(ctx, u.ID); err != nil {
		t.Fatalf("delete sessions: %v", err)
	}
	if _, err := s.UserForToken(ctx, sess.Token); !errors.Is(err, ErrNotFound) {
		t.Errorf("token after revoke err = %v, want ErrNotFound", err)
	}
}
