package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mukul-mehta/routeup/internal/ipc"
)

// SetupMarker persists user choices across runs. Schema is versioned.
type SetupMarker struct {
	Version int    `json:"version"`
	TLSPort int    `json:"tls_port"`
	BinPath string `json:"bin_path,omitempty"` // CLI binary that installed the platform setup
}

const (
	SetupMarkerName     = "setup.json"
	CurrentSetupVersion = 1
)

// SetupMarkerPath returns setup.json in the active state directory.
func SetupMarkerPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, SetupMarkerName), nil
}

// ReadSetupMarker returns wrapped os.ErrNotExist when no marker exists.
func ReadSetupMarker() (*SetupMarker, error) {
	path, err := SetupMarkerPath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var m SetupMarker
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &m, nil
}

// WriteSetupMarker writes the marker (0644; no secrets).
func WriteSetupMarker(m *SetupMarker) error {
	path, err := SetupMarkerPath()
	if err != nil {
		return err
	}
	if err := EnsureParentDir(path); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode marker: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// RemoveSetupMarker invalidates prior setup state before replacing core assets.
func RemoveSetupMarker() error {
	path, err := SetupMarkerPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// TLSPortOrDefault returns the marker's TLSPort or ipc.DefaultUserPort (443).
func TLSPortOrDefault() int {
	m, err := ReadSetupMarker()
	if err != nil || m == nil || m.TLSPort == 0 {
		return ipc.DefaultUserPort
	}
	return m.TLSPort
}
