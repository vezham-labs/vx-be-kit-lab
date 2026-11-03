// Command contracts generates the browser-safe protocol from Go DTOs.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"go.yaml.in/yaml/v4"
)

var types = []any{c.Task{}, c.ToolCall{}, c.Attachment{}, c.Message{}, c.ActivityAppearance{}, c.RunRequest{}, c.ApprovalDecision{}, c.Usage{}, c.StreamEvent{}, c.ModelInfo{}, c.Conversation{}, c.ConversationPage{}, c.RenameRequest{}, c.FeedbackRequest{}, c.TasksRequest{}, c.BranchRequest{}, c.Checkpoint{}, c.CheckpointRequest{}, c.RunInfo{}, c.PluginInfo{}, c.PluginUpdate{}, c.Principal{}, c.SessionInfo{}, c.AuthSession{}, c.LoginMethod{}, c.Capabilities{}, c.Memory{}, c.MemoryRequest{}, c.Transcript{}, c.SpeechRequest{}, c.ClientAction{}, c.APIError{}, c.ProviderSettings{}, c.ProviderKeyRequest{}, c.ProviderOAuthRequest{}, c.ProviderAuthorization{}}

func ts(t reflect.Type) string {
	if t == reflect.TypeFor[c.PermissionMode]() {
		return "PermissionMode"
	}
	switch t.Kind() {
	case reflect.Pointer:
		return ts(t.Elem())
	case reflect.Slice, reflect.Array:
		return "Array<" + ts(t.Elem()) + ">"
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int64, reflect.Float64:
		return "number"
	case reflect.Interface:
		return "unknown"
	case reflect.Map:
		return "Record<string, " + ts(t.Elem()) + ">"
	case reflect.Struct:
		return t.Name()
	}
	panic("unsupported type " + t.String())
}
func generate() (map[string][]byte, error) {
	var text strings.Builder
	text.WriteString("// vx-bot/NOTE: Generated from Go contracts. Run the backend contracts:generate target.\nexport type PermissionMode = 'ask' | 'safe' | 'full'\n\n")
	schemas := map[string]any{}
	for _, value := range types {
		t := reflect.TypeOf(value)
		text.WriteString("export interface " + t.Name() + " {\n")
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")
			optional := ""
			if len(tag) > 1 && tag[1] == "omitempty" {
				optional = "?"
			}
			text.WriteString("  " + tag[0] + optional + ": " + ts(field.Type) + "\n")
		}
		text.WriteString("}\n\n")
		schema, err := jsonschema.ForType(t, &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{reflect.TypeFor[c.PermissionMode](): {Type: "string", Enum: []any{string(c.PermissionAsk), string(c.PermissionSafe), string(c.PermissionFull)}}}})
		if err != nil {
			return nil, err
		}
		schemas[t.Name()] = schema
	}
	document := map[string]any{"openapi": "3.1.0", "info": map[string]string{"title": "Tamizhi API", "version": "1.0.0"}, "servers": []any{map[string]string{"url": "/api/tamizhi"}}, "components": map[string]any{"schemas": schemas, "securitySchemes": map[string]any{"session": map[string]any{"type": "apiKey", "in": "cookie", "name": "tamizhi_session"}, "bearer": map[string]any{"type": "http", "scheme": "bearer"}}}, "security": []any{map[string]any{"session": []string{}}, map[string]any{"bearer": []string{}}}}
	paths := map[string]any{}
	add := func(path, method, input, output string) {
		item, ok := paths[path].(map[string]any)
		if !ok {
			item = map[string]any{}
			paths[path] = item
		}
		operation := map[string]any{"responses": map[string]any{"200": map[string]any{"description": "Successful response", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/" + output}}}}, "401": map[string]any{"description": "Sign in required"}, "403": map[string]any{"description": "Access denied"}, "409": map[string]any{"description": "Concurrent update conflict"}}}
		if input != "" {
			operation["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/" + input}}}}
		}
		parameters := []any{}
		for _, part := range strings.Split(path, "/") {
			if strings.HasPrefix(part, "{") {
				parameters = append(parameters, map[string]any{"name": strings.Trim(part, "{}"), "in": "path", "required": true, "schema": map[string]string{"type": "string"}})
			}
		}
		if len(parameters) > 0 {
			operation["parameters"] = parameters
		}
		item[method] = operation
	}
	add("/chat", "post", "RunRequest", "StreamEvent")
	add("/approvals", "post", "ApprovalDecision", "APIError")
	add("/providers/gemini/oauth", "post", "ProviderOAuthRequest", "ProviderAuthorization")
	paths["/providers/gemini/oauth/callback"] = map[string]any{"get": map[string]any{"security": []any{}, "description": "One-use Google model authorization callback, bound to its HttpOnly state cookie and authenticated initiating owner.", "responses": map[string]any{"302": map[string]any{"description": "Return to the allowlisted frontend with connection status"}, "403": map[string]any{"description": "Invalid, expired or mismatched authorization state"}}}}

	add("/providers/{id}", "put", "ProviderKeyRequest", "ProviderSettings")
	add("/providers/{id}", "delete", "", "APIError")
	add("/session", "get", "", "SessionInfo")
	add("/auth/session", "get", "", "AuthSession")
	paths["/auth/session"].(map[string]any)["get"].(map[string]any)["security"] = []any{}
	add("/capabilities", "get", "", "Capabilities")
	add("/conversations", "get", "", "ConversationPage")
	add("/conversations/{id}", "get", "", "Conversation")
	add("/conversations/{id}", "patch", "RenameRequest", "Conversation")
	add("/conversations/{id}", "delete", "", "APIError")
	add("/conversations/{id}/branches", "post", "BranchRequest", "Conversation")
	add("/conversations/{id}/messages/{message}/feedback", "put", "FeedbackRequest", "Conversation")
	add("/conversations/{id}/messages/{message}/tasks", "put", "TasksRequest", "Conversation")
	add("/conversations/{id}/checkpoints", "post", "CheckpointRequest", "Checkpoint")
	add("/conversations/{id}/compact", "post", "", "Conversation")
	add("/conversations/{id}/checkpoints/{checkpoint}/restore", "post", "", "Conversation")
	add("/runs/{id}", "get", "", "RunInfo")
	add("/runs/{id}/events", "get", "", "StreamEvent")
	add("/runs/{id}/cancel", "post", "", "APIError")
	add("/plugins/{name}", "patch", "PluginUpdate", "PluginInfo")
	add("/memory", "post", "MemoryRequest", "Memory")
	add("/memory/{id}", "delete", "", "APIError")
	add("/audio/speech", "post", "SpeechRequest", "APIError")
	for _, entry := range []struct{ path, output string }{{"/providers", "ProviderSettings"}, {"/models", "ModelInfo"}, {"/plugins", "PluginInfo"}, {"/memory", "Memory"}, {"/conversations/{id}/checkpoints", "Checkpoint"}} {
		add(entry.path, "get", "", entry.output)
		op := paths[entry.path].(map[string]any)["get"].(map[string]any)
		op["responses"] = map[string]any{"200": map[string]any{"description": "List", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/" + entry.output}}}}}}
	}
	for _, entry := range []struct{ path, output string }{{"/attachments", "Attachment"}, {"/audio/transcriptions", "Transcript"}} {
		add(entry.path, "post", "", entry.output)
		paths[entry.path].(map[string]any)["post"].(map[string]any)["requestBody"] = map[string]any{"required": true, "content": map[string]any{"multipart/form-data": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"file": map[string]any{"type": "string", "format": "binary"}}, "required": []string{"file"}}}}}
	}
	for _, path := range []string{"/chat", "/runs/{id}/events"} {
		method := "get"
		if path == "/chat" {
			method = "post"
		}
		paths[path].(map[string]any)[method].(map[string]any)["responses"] = map[string]any{"200": map[string]any{"description": "Durable SSE events. Resume using sequence in Last-Event-ID or after query.", "content": map[string]any{"text/event-stream": map[string]any{"schema": map[string]string{"type": "string"}}}}}
	}
	document["paths"] = paths
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	// vx-bot/NOTE: JSON Schema structs use JSON tags; normalize them before YAML encoding.
	var normalized map[string]any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, err
	}
	var openAPI bytes.Buffer
	openAPI.WriteString("# vx-bot/NOTE: Generated from Go contracts. Regenerate with scripts/sync-tamizhi-contracts.\n")
	encoder := yaml.NewEncoder(&openAPI)
	encoder.SetIndent(2)
	encoder.CompactSeqIndent()
	if err := encoder.Encode(normalized); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	schemaBytes, err := json.MarshalIndent(schemas, "", "  ")
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"api.ts": []byte(strings.TrimRight(text.String(), "\n") + "\n"), "schemas.json": append(schemaBytes, '\n'), "openapi.yaml": openAPI.Bytes()}, nil
}
func main() {
	out := flag.String("output", "api/tamizhi", "output directory")
	check := flag.Bool("check", false, "fail if generated files differ")
	flag.Parse()
	files, err := generate()
	if err != nil {
		panic(err)
	}
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(*out, name)
		if *check {
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, files[name]) {
				fmt.Fprintln(os.Stderr, "contract drift:", path)
				os.Exit(1)
			}
		} else {
			if err = os.MkdirAll(*out, 0755); err != nil {
				panic(err)
			}
			if err = os.WriteFile(path, files[name], 0644); err != nil {
				panic(err)
			}
		}
	}
}
