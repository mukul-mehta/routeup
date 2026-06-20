package state

import (
	"errors"
	"os"
	"testing"

	"github.com/mukul-mehta/routeup/internal/ipc"
)

func TestSetupMarker_Roundtrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	in := &SetupMarker{Version: 1, TLSPort: 443, BinPath: "/opt/homebrew/bin/routeup"}
	if err := WriteSetupMarker(in); err != nil {
		t.Fatalf("WriteSetupMarker: %v", err)
	}
	out, err := ReadSetupMarker()
	if err != nil {
		t.Fatalf("ReadSetupMarker: %v", err)
	}
	if out.Version != in.Version || out.TLSPort != in.TLSPort || out.BinPath != in.BinPath {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", out, in)
	}
}

func TestSetupMarker_MissingReturnsErrNotExist(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := ReadSetupMarker()
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ReadSetupMarker on missing file: err = %v, want wrapping os.ErrNotExist", err)
	}
}

func TestTLSPortOrDefault_NoMarker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if got, want := TLSPortOrDefault(), ipc.DefaultUserPort; got != want {
		t.Errorf("TLSPortOrDefault with no marker = %d, want %d", got, want)
	}
}

func TestTLSPortOrDefault_WithMarker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := WriteSetupMarker(&SetupMarker{Version: 1, TLSPort: 9999}); err != nil {
		t.Fatalf("WriteSetupMarker: %v", err)
	}
	if got, want := TLSPortOrDefault(), 9999; got != want {
		t.Errorf("TLSPortOrDefault with marker = %d, want %d", got, want)
	}
}
