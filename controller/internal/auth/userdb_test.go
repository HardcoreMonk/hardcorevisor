package auth

import (
	"testing"
)

func newTestDB(t *testing.T) *UserDB {
	t.Helper()
	db, err := NewUserDB(":memory:")
	if err != nil {
		t.Fatalf("NewUserDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestUserDB_CRUD(t *testing.T) {
	db := newTestDB(t)

	// Create
	if err := db.CreateUser("alice", "pass123", "admin"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := db.CreateUser("bob", "secret", "viewer"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Get
	user, err := db.GetUser("alice")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.Username != "alice" || user.Role != "admin" {
		t.Errorf("expected alice/admin, got %s/%s", user.Username, user.Role)
	}
	if user.ID == 0 {
		t.Error("expected non-zero ID")
	}

	// List
	users, err := db.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("expected 2 users, got %d", len(users))
	}

	// Delete
	if err := db.DeleteUser("bob"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	users, err = db.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers after delete: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("expected 1 user after delete, got %d", len(users))
	}

	// Delete non-existent
	if err := db.DeleteUser("nonexistent"); err == nil {
		t.Error("expected error deleting non-existent user")
	}

	// Get non-existent
	_, err = db.GetUser("nonexistent")
	if err == nil {
		t.Error("expected error getting non-existent user")
	}
}

func TestUserDB_VerifyPassword(t *testing.T) {
	db := newTestDB(t)

	if err := db.CreateUser("alice", "correcthorse", "operator"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Correct password
	user, err := db.VerifyPassword("alice", "correcthorse")
	if err != nil {
		t.Fatalf("VerifyPassword (correct): %v", err)
	}
	if user.Username != "alice" || user.Role != "operator" {
		t.Errorf("unexpected user: %+v", user)
	}

	// Wrong password
	_, err = db.VerifyPassword("alice", "wrongpassword")
	if err == nil {
		t.Error("expected error for wrong password")
	}

	// Non-existent user
	_, err = db.VerifyPassword("nobody", "anything")
	if err == nil {
		t.Error("expected error for non-existent user")
	}
}

func TestUserDB_DuplicateUser(t *testing.T) {
	db := newTestDB(t)

	if err := db.CreateUser("alice", "pass1", "admin"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Duplicate username should fail
	err := db.CreateUser("alice", "pass2", "viewer")
	if err == nil {
		t.Error("expected error for duplicate username")
	}
}

func TestUserDB_SeedDefaultAdmin(t *testing.T) {
	db := newTestDB(t)

	// First seed should create admin
	db.SeedDefaultAdmin()
	users, err := db.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user after seed, got %d", len(users))
	}
	if users[0].Username != "admin" || users[0].Role != "admin" {
		t.Errorf("expected admin/admin, got %s/%s", users[0].Username, users[0].Role)
	}

	// Verify the seeded password works
	_, err = db.VerifyPassword("admin", "admin")
	if err != nil {
		t.Errorf("expected seeded admin password to work: %v", err)
	}

	// Second seed should be a no-op
	db.SeedDefaultAdmin()
	users, err = db.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("expected still 1 user after second seed, got %d", len(users))
	}
}
