package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func authzFixture(t *testing.T, namespace string) (*Authorizer, *Store, string) {
	t.Helper()
	s := openTestStore(t)
	_, secret, err := s.CreateToken(context.Background(), "alice", []AllowPattern{allowPattern(t, "*.alice.routeup.dev")})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ServerConfig{Domain: "routeup.dev", Listen: ":8080", DBPath: "x", PublicNamespace: namespace}
	return NewAuthorizer(cfg, s), s, secret
}

// wantStatus asserts err is an *AuthzError with the given status.
func wantStatus(t *testing.T, err error, status int) {
	t.Helper()
	var ae *AuthzError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want *AuthzError", err)
	}
	if ae.Status != status {
		t.Errorf("status = %d (%q), want %d", ae.Status, ae.Reason, status)
	}
}

func TestAuthorize_TokenAccept(t *testing.T) {
	az, _, secret := authzFixture(t, "try")
	d, err := az.Authorize(context.Background(), ClaimAttempt{TokenSecret: secret, Route: "myapp"})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if d.Host != "myapp.alice.routeup.dev" {
		t.Errorf("host = %q, want myapp.alice.routeup.dev", d.Host)
	}
	if d.Mode != HoldByToken || d.Ephemeral {
		t.Errorf("mode/ephemeral = %v/%v, want token/false", d.Mode, d.Ephemeral)
	}
	if d.Base != "alice.routeup.dev" {
		t.Errorf("base = %q, want alice.routeup.dev", d.Base)
	}
}

func TestAuthorize_MultiLabelRejected(t *testing.T) {
	az, _, secret := authzFixture(t, "try")
	_, err := az.Authorize(context.Background(), ClaimAttempt{TokenSecret: secret, Route: "api.myapp"})
	wantStatus(t, err, http.StatusBadRequest)
}

func TestAuthorize_NamespaceTier_LeafNotReserved(t *testing.T) {
	// Inside an owned namespace, even a reserved root name like "api" is fine.
	s := openTestStore(t)
	_, secret, err := s.CreateToken(context.Background(), "mukul",
		[]AllowPattern{allowPattern(t, "*.mukul.routeup.dev")})
	if err != nil {
		t.Fatal(err)
	}
	az := NewAuthorizer(ServerConfig{Domain: "routeup.dev", Listen: ":8080", DBPath: "x"}, s)
	d, err := az.Authorize(context.Background(), ClaimAttempt{TokenSecret: secret, Route: "api"})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if d.Host != "api.mukul.routeup.dev" {
		t.Errorf("host = %q, want api.mukul.routeup.dev", d.Host)
	}
}

func TestAuthorize_RootTier_NamespaceLabelReserved(t *testing.T) {
	// A namespace token grants "mukul"; a root token must not be able to claim
	// "mukul.routeup.dev" out from under it.
	s := openTestStore(t)
	if _, _, err := s.CreateToken(context.Background(), "mukul",
		[]AllowPattern{allowPattern(t, "*.mukul.routeup.dev")}); err != nil {
		t.Fatal(err)
	}
	_, rootSecret, err := s.CreateToken(context.Background(), "root",
		[]AllowPattern{allowPattern(t, "*.routeup.dev")})
	if err != nil {
		t.Fatal(err)
	}
	az := NewAuthorizer(ServerConfig{Domain: "routeup.dev", Listen: ":8080", DBPath: "x"}, s)

	_, err = az.Authorize(context.Background(), ClaimAttempt{TokenSecret: rootSecret, Route: "mukul"})
	wantStatus(t, err, http.StatusForbidden)

	// but a free root name still works
	d, err := az.Authorize(context.Background(), ClaimAttempt{TokenSecret: rootSecret, Route: "freebie"})
	if err != nil {
		t.Fatalf("free root claim: %v", err)
	}
	if d.Host != "freebie.routeup.dev" {
		t.Errorf("host = %q, want freebie.routeup.dev", d.Host)
	}
}

func TestAuthorize_TokenReservedSubtree(t *testing.T) {
	// admin-tier token; "api" lands directly on a reserved subdomain.
	s := openTestStore(t)
	_, secret, err := s.CreateToken(context.Background(), "root", []AllowPattern{allowPattern(t, "*.routeup.dev")})
	if err != nil {
		t.Fatal(err)
	}
	az := NewAuthorizer(ServerConfig{Domain: "routeup.dev", Listen: ":8080", DBPath: "x"}, s)

	_, err = az.Authorize(context.Background(), ClaimAttempt{TokenSecret: secret, Route: "api"})
	wantStatus(t, err, http.StatusForbidden)

	// a non-reserved route under the same admin token succeeds
	d, err := az.Authorize(context.Background(), ClaimAttempt{TokenSecret: secret, Route: "myapp"})
	if err != nil {
		t.Fatalf("non-reserved claim: %v", err)
	}
	if d.Host != "myapp.routeup.dev" {
		t.Errorf("host = %q, want myapp.routeup.dev", d.Host)
	}
}

func TestAuthorize_TokenOutsideDomain(t *testing.T) {
	s := openTestStore(t)
	_, secret, err := s.CreateToken(context.Background(), "stray", []AllowPattern{allowPattern(t, "*.alice.example.com")})
	if err != nil {
		t.Fatal(err)
	}
	az := NewAuthorizer(ServerConfig{Domain: "routeup.dev", Listen: ":8080", DBPath: "x"}, s)
	_, err = az.Authorize(context.Background(), ClaimAttempt{TokenSecret: secret, Route: "myapp"})
	wantStatus(t, err, http.StatusForbidden)
}

func TestAuthorize_BadToken(t *testing.T) {
	az, _, _ := authzFixture(t, "try")
	_, err := az.Authorize(context.Background(), ClaimAttempt{TokenSecret: "sk_routeup_nope", Route: "api"})
	wantStatus(t, err, http.StatusUnauthorized)
}

func TestAuthorize_NamespaceAccept(t *testing.T) {
	az, _, _ := authzFixture(t, "try")
	d, err := az.Authorize(context.Background(), ClaimAttempt{Route: "foo"})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if d.Host != "foo.try.routeup.dev" || d.Mode != HoldByNamespace || !d.Ephemeral {
		t.Errorf("decision = %+v, want foo.try.routeup.dev namespace ephemeral", d)
	}
}

func TestAuthorize_NamespaceMultiLabelRejected(t *testing.T) {
	az, _, _ := authzFixture(t, "try")
	_, err := az.Authorize(context.Background(), ClaimAttempt{Route: "foo.bar"})
	wantStatus(t, err, http.StatusBadRequest)
}

func TestAuthorize_NoTokenNoNamespace(t *testing.T) {
	az, _, _ := authzFixture(t, "") // namespace disabled
	_, err := az.Authorize(context.Background(), ClaimAttempt{Route: "foo"})
	wantStatus(t, err, http.StatusUnauthorized)
}
