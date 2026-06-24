package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// runToken executes the token command tree with args, returning combined stdout.
func runToken(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newTokenCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("token %v: %v\noutput:\n%s", args, err, out.String())
	}
	return out.String()
}

func TestToken_CreateListRevoke(t *testing.T) {
	db := filepath.Join(t.TempDir(), "tokens.db")

	createOut := runToken(t, "create", "alice", "--allow", "*.alice.routeup.dev", "--db", db)
	if !strings.Contains(createOut, "sk_routeup_") {
		t.Fatalf("create output missing secret:\n%s", createOut)
	}
	if !strings.Contains(createOut, "*.alice.routeup.dev") {
		t.Errorf("create output missing allow pattern:\n%s", createOut)
	}

	// Pull the id out of the create output to revoke it later.
	var id string
	for _, line := range strings.Split(createOut, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "id:") {
			id = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "id:"))
		}
	}
	if id == "" {
		t.Fatalf("could not parse token id from:\n%s", createOut)
	}

	listOut := runToken(t, "list", "--db", db)
	if !strings.Contains(listOut, "alice") || !strings.Contains(listOut, "active") {
		t.Errorf("list output unexpected:\n%s", listOut)
	}
	if strings.Contains(listOut, "sk_routeup_") {
		t.Errorf("list must never print secrets:\n%s", listOut)
	}

	revokeOut := runToken(t, "revoke", id, "--db", db)
	if !strings.Contains(revokeOut, "revoked") {
		t.Errorf("revoke output = %q", revokeOut)
	}

	listOut2 := runToken(t, "list", "--db", db)
	if !strings.Contains(listOut2, "revoked") {
		t.Errorf("expected token to show revoked:\n%s", listOut2)
	}
}

func TestToken_CreateRequiresAllow(t *testing.T) {
	db := filepath.Join(t.TempDir(), "tokens.db")
	cmd := newTokenCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"create", "alice", "--db", db})
	if err := cmd.Execute(); err == nil {
		t.Errorf("expected error when --allow is missing")
	}
}

func TestToken_RevokeUnknown(t *testing.T) {
	db := filepath.Join(t.TempDir(), "tokens.db")
	out := runToken(t, "revoke", "deadbeef", "--db", db)
	if !strings.Contains(out, "no active token") {
		t.Errorf("revoke unknown output = %q", out)
	}
}
