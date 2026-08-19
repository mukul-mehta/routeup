package state

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// OwnerKind identifies the long-running CLI command holding desired state.
type OwnerKind string

const (
	OwnerServe  OwnerKind = "serve"
	OwnerRunner OwnerKind = "runner"
	OwnerExpose OwnerKind = "expose"
)

// OwnerRecord is non-secret runtime identity for one live CLI owner. It does
// not persist targets, exposure configuration, or credentials.
type OwnerRecord struct {
	ID    string    `json:"id"`
	Route string    `json:"route"`
	Kind  OwnerKind `json:"kind"`
	PID   int       `json:"pid"`
}

// OwnerLease holds the runtime record for one live CLI owner.
type OwnerLease struct {
	path string
}

// RegisterOwner records a live CLI owner until its lease is released.
func RegisterOwner(route string, kind OwnerKind, pid int) (*OwnerLease, error) {
	if route == "" || pid <= 0 || !kind.valid() {
		return nil, errors.New("route, owner kind, and pid are required")
	}
	dir, err := ownersDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create owner state directory: %w", err)
	}
	record := OwnerRecord{ID: rand.Text(), Route: route, Kind: kind, PID: pid}
	path := filepath.Join(dir, record.ID+".json")
	file, err := os.CreateTemp(dir, ".owner-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temporary owner state: %w", err)
	}
	tempPath := file.Name()
	if err := json.NewEncoder(file).Encode(record); err != nil {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("write owner state: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("close owner state: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("publish owner state: %w", err)
	}
	return &OwnerLease{path: path}, nil
}

// Release removes this owner's runtime record. It is idempotent.
func (l *OwnerLease) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	err := os.Remove(l.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove owner state: %w", err)
	}
	return nil
}

// LiveOwners returns live CLI owner records and removes records whose process
// has exited. PID reuse can retain a stale record, which deliberately fails
// lifecycle operations closed rather than risking an unrelated process.
func LiveOwners() ([]OwnerRecord, error) {
	dir, err := ownersDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read owner state directory: %w", err)
	}
	owners := make([]OwnerRecord, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("open owner state %s: %w", entry.Name(), err)
		}
		var owner OwnerRecord
		decodeErr := json.NewDecoder(file).Decode(&owner)
		closeErr := file.Close()
		if decodeErr != nil || closeErr != nil || owner.ID == "" || owner.Route == "" || owner.PID <= 0 || !owner.Kind.valid() {
			return nil, fmt.Errorf("invalid owner state %s", entry.Name())
		}
		if !ownerProcessAlive(owner.PID) {
			_ = os.Remove(path)
			continue
		}
		owners = append(owners, owner)
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i].ID < owners[j].ID })
	return owners, nil
}

func ownersDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, OwnersDirName), nil
}

func (k OwnerKind) valid() bool {
	return k == OwnerServe || k == OwnerRunner || k == OwnerExpose
}

func ownerProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
