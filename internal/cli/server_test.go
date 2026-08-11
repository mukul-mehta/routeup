package cli

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/mukul-mehta/routeup/internal/server"
)

func TestNewServerLoggerHonorsFormatAndLevel(t *testing.T) {
	var output bytes.Buffer
	logger, err := newServerLogger(&output, server.ServerConfig{LogFormat: server.LogFormatJSON, LogLevel: "warn"})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("hidden")
	logger.Warn("visible", "count", 2)

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("decode log: %v\n%s", err, output.String())
	}
	if entry["level"] != slog.LevelWarn.String() || entry["msg"] != "visible" || entry["count"] != float64(2) {
		t.Fatalf("log entry = %#v", entry)
	}
}
