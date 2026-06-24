package server

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func allowPattern(t *testing.T, s string) AllowPattern {
	t.Helper()
	p, err := ParseAllowPattern(s)
	if err != nil {
		t.Fatalf("ParseAllowPattern(%q): %v", s, err)
	}
	return p
}

func TestCreateAndVerifyToken(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	patterns := []AllowPattern{allowPattern(t, "*.alice.routeup.dev")}
	id, secret, err := s.CreateToken(ctx, "alice", patterns)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if !strings.HasPrefix(secret, "sk_routeup_") {
		t.Errorf("secret %q missing prefix", secret)
	}
	if len(secret) != len("sk_routeup_")+43 {
		t.Errorf("secret len = %d, want %d", len(secret), len("sk_routeup_")+43)
	}

	tok, err := s.VerifyToken(ctx, secret)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if tok.ID != id || tok.Name != "alice" {
		t.Errorf("verified token = %+v, want id=%s name=alice", tok, id)
	}
	if len(tok.Patterns) != 1 || tok.Patterns[0].String() != "*.alice.routeup.dev" {
		t.Errorf("patterns = %v, want [*.alice.routeup.dev]", tok.Patterns)
	}

	if _, err := s.VerifyToken(ctx, "sk_routeup_wrongsecretwrongsecretwrongsecretwrongse"); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("VerifyToken(wrong) err = %v, want ErrTokenInvalid", err)
	}
	if _, err := s.VerifyToken(ctx, "not-a-routeup-token"); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("VerifyToken(bad prefix) err = %v, want ErrTokenInvalid", err)
	}
}

func TestVerifyToken_Revoked(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	id, secret, err := s.CreateToken(ctx, "bob", []AllowPattern{allowPattern(t, "*.bob.routeup.dev")})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.RevokeToken(ctx, id)
	if err != nil || !ok {
		t.Fatalf("RevokeToken = (%v, %v), want (true, nil)", ok, err)
	}
	if _, err := s.VerifyToken(ctx, secret); !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("VerifyToken after revoke err = %v, want ErrTokenInvalid", err)
	}
}

func TestListTokens(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if _, _, err := s.CreateToken(ctx, "alice", []AllowPattern{allowPattern(t, "*.alice.routeup.dev")}); err != nil {
		t.Fatal(err)
	}
	id2, _, err := s.CreateToken(ctx, "bob", []AllowPattern{allowPattern(t, "*.bob.routeup.dev"), allowPattern(t, "*.team.routeup.dev")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevokeToken(ctx, id2); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListTokens(ctx)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListTokens len = %d, want 2", len(list))
	}
	byName := map[string]Token{}
	for _, tok := range list {
		byName[tok.Name] = tok
	}
	if byName["alice"].Revoked() {
		t.Errorf("alice should be active")
	}
	if !byName["bob"].Revoked() {
		t.Errorf("bob should be revoked")
	}
	if len(byName["bob"].Patterns) != 2 {
		t.Errorf("bob patterns = %d, want 2", len(byName["bob"].Patterns))
	}
}

func TestRevokeToken_Unknown(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	ok, err := s.RevokeToken(ctx, "deadbeef")
	if err != nil {
		t.Fatalf("RevokeToken(unknown): %v", err)
	}
	if ok {
		t.Errorf("RevokeToken(unknown) = true, want false")
	}
}

func TestCreateToken_Validation(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if _, _, err := s.CreateToken(ctx, "", []AllowPattern{allowPattern(t, "*.x.routeup.dev")}); err == nil {
		t.Errorf("expected error for empty name")
	}
	if _, _, err := s.CreateToken(ctx, "noscope", nil); err == nil {
		t.Errorf("expected error for no patterns")
	}
}

func TestHashVerify_Roundtrip(t *testing.T) {
	hash, err := hashSecret("sk_routeup_example")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := verifySecret("sk_routeup_example", hash)
	if err != nil || !ok {
		t.Errorf("verifySecret(correct) = (%v, %v), want (true, nil)", ok, err)
	}
	ok, err = verifySecret("sk_routeup_wrong", hash)
	if err != nil {
		t.Fatalf("verifySecret(wrong) error: %v", err)
	}
	if ok {
		t.Errorf("verifySecret(wrong) = true, want false")
	}
}
