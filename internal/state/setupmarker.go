package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mukul-mehta/routeup/internal/ipc"
)

// SetupMarker persists user choices across runs. Schema is versioned.
type SetupMarker struct {
	Version int    `json:"version"`
	TLSPort int    `json:"tls_port"`
	BinPath string `json:"bin_path,omitempty"` // binary the port-bind machinery was wired to
}

const SetupMarkerName = "setup.json"

// SetupMarkerPath returns ~/.routeup/setup.json.
func SetupMarkerPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, StateDirName, SetupMarkerName), nil
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

// TLSPortOrDefault returns the marker's TLSPort or ipc.DefaultUserPort (443).
func TLSPortOrDefault() int {
	m, err := ReadSetupMarker()
	if err != nil || m == nil || m.TLSPort == 0 {
		return ipc.DefaultUserPort
	}
	return m.TLSPort
}
