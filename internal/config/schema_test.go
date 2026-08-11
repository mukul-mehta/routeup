package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRouteupSchemaIsValidJSONAndCoversConfigFields(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "routeup.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Schema     string                     `json:"$schema"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Schema != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("schema draft = %q", schema.Schema)
	}
	for _, field := range []string{"$schema", "name", "port", "targets", "expose", "capture", "command", "port_env_var"} {
		if _, ok := schema.Properties[field]; !ok {
			t.Errorf("schema missing %q", field)
		}
	}
}
