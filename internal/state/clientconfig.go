package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ClientConfig holds the per-user defaults for talking to a public server: the
// server URL and a token. It is written by `routeup setup --server/--token` and
// read by `expose`/`serve --expose`, so those commands need no flags once it's
// set. Stored at ~/.routeup/client.json with 0600 permissions because the token
// is a secret.
type ClientConfig struct {
	Server string `json:"server,omitempty"`
	Token  string `json:"token,omitempty"`
}

// ClientConfigPath returns the path of the client config file.
func ClientConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, StateDirName, ClientConfigName), nil
}

// ReadClientConfig loads the client config. A missing file is not an error: it
// returns a zero ClientConfig so callers can layer flags and env over it.
func ReadClientConfig() (ClientConfig, error) {
	path, err := ClientConfigPath()
	if err != nil {
		return ClientConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ClientConfig{}, nil
		}
		return ClientConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var c ClientConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return ClientConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// WriteClientConfig writes the client config with 0600 permissions (it holds a
// token).
func WriteClientConfig(c ClientConfig) error {
	path, err := ClientConfigPath()
	if err != nil {
		return err
	}
	if err := EnsureParentDir(path); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode client config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
