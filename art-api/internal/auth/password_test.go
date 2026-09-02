package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("a-long-and-unique-password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	valid, err := VerifyPassword(hash, "a-long-and-unique-password")
	if err != nil || !valid {
		t.Fatalf("correct password rejected: valid=%v error=%v", valid, err)
	}
	valid, err = VerifyPassword(hash, "wrong-password")
	if err != nil || valid {
		t.Fatalf("wrong password accepted: valid=%v error=%v", valid, err)
	}
}

func TestPasswordAllowsAnyNonEmptyLength(t *testing.T) {
	hash, err := HashPassword("x")
	if err != nil {
		t.Fatalf("single-character password rejected: %v", err)
	}
	valid, err := VerifyPassword(hash, "x")
	if err != nil || !valid {
		t.Fatalf("single-character password did not verify: valid=%v error=%v", valid, err)
	}
	if _, err := HashPassword(""); err == nil {
		t.Fatal("empty password was accepted")
	}
}

func TestPasswordRejectsUntrustedArgonParameters(t *testing.T) {
	_, err := VerifyPassword("$argon2id$v=19$m=999999999,t=3,p=1$YQ$YQ", "password")
	if err == nil {
		t.Fatal("unbounded memory parameter was accepted")
	}
}
