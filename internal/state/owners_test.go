package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOwnerLeaseTracksOnlyLiveOwners(t *testing.T) {
	t.Setenv(StateDirEnv, t.TempDir())
	lease, err := RegisterOwner("myapp", OwnerServe, os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	owners, err := LiveOwners()
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 1 || owners[0].Route != "myapp" || owners[0].Kind != OwnerServe || owners[0].PID != os.Getpid() {
		t.Fatalf("owners = %#v", owners)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	owners, err = LiveOwners()
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners after release = %#v", owners)
	}
}

func TestLiveOwnersIgnoresUnpublishedTemporaryRecord(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(StateDirEnv, dir)
	ownersPath := filepath.Join(dir, OwnersDirName)
	if err := os.MkdirAll(ownersPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ownersPath, ".owner-crashed.tmp"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	owners, err := LiveOwners()
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 0 {
		t.Fatalf("owners = %#v", owners)
	}
}

func TestRegisterOwnerRejectsInvalidIdentity(t *testing.T) {
	t.Setenv(StateDirEnv, t.TempDir())
	if _, err := RegisterOwner("", OwnerServe, os.Getpid()); err == nil {
		t.Fatal("missing route was accepted")
	}
	if _, err := RegisterOwner("myapp", "unknown", os.Getpid()); err == nil {
		t.Fatal("unknown owner kind was accepted")
	}
}
