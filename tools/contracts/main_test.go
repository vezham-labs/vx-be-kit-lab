package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"go.yaml.in/yaml/v4"
)

func TestGeneratedOpenAPIYAML(t *testing.T) {
	files, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := files["openapi.json"]; exists {
		t.Fatal("unexpected JSON OpenAPI artifact")
	}
	data := files["openapi.yaml"]
	if !bytes.HasPrefix(data, []byte("# vx-bot/NOTE: Generated from Go contracts.")) {
		t.Fatal("missing generated notice")
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document["openapi"] != "3.1.0" {
		t.Fatal("incorrect OpenAPI version")
	}
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	var expected map[string]any
	if err := json.Unmarshal(files["schemas.json"], &expected); err != nil {
		t.Fatal(err)
	}
	actual, err := json.Marshal(schemas)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, canonical) {
		t.Fatal("YAML schema differs from generated JSON Schema")
	}
	paths := document["paths"].(map[string]any)
	pluginResponses := paths["/plugins/{name}"].(map[string]any)["patch"].(map[string]any)["responses"].(map[string]any)
	if _, exists := pluginResponses["503"]; !exists {
		t.Fatal("missing plugin persistence failure response")
	}
	pluginSchema := pluginResponses["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if pluginSchema["type"] != "array" {
		t.Fatal("plugin update must return a plugin list")
	}
	chat := paths["/chat"].(map[string]any)["post"].(map[string]any)
	response := chat["responses"].(map[string]any)["200"].(map[string]any)
	if _, exists := response["content"].(map[string]any)["text/event-stream"]; !exists {
		t.Fatal("missing SSE response")
	}
	again, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, again["openapi.yaml"]) {
		t.Fatal("nondeterministic YAML output")
	}
}
