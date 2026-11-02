package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

func TestBYOKEncryptedSettingsAndNativeProviders(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			if strings.HasPrefix(r.URL.Path, "/anthropic") {
				if r.Header.Get("x-api-key") != "test-provider-key" {
					t.Error("missing Anthropic key")
				}
				fmt.Fprint(w, `{"data":[{"id":"claude-fixture","display_name":"Claude fixture"}]}`)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/gemini") {
				if r.Header.Get("x-goog-api-key") != "test-provider-key" {
					t.Error("missing Gemini key")
				}
				fmt.Fprint(w, `{"models":[{"name":"models/gemini-fixture","displayName":"Gemini fixture","supportedGenerationMethods":["generateContent"]}]}`)
				return
			}
			if r.Header.Get("Authorization") != "Bearer test-provider-key" {
				t.Error("missing OpenAI key")
			}
			fmt.Fprint(w, `{"data":[{"id":"gpt-fixture"},{"id":"whisper-1"},{"id":"tts-1"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch {
		case strings.HasSuffix(r.URL.Path, "/messages"):
			if r.Header.Get("x-api-key") != "test-provider-key" {
				t.Error("Anthropic stream auth")
			}
			fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-fixture\",\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Native reply\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		case strings.Contains(r.URL.Path, ":streamGenerateContent"):
			if r.Header.Get("x-goog-api-key") != "test-provider-key" {
				t.Error("Gemini stream auth")
			}
			fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Native reply\"}]}}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2,\"totalTokenCount\":5}}\n\n")
		default:
			if r.Header.Get("Authorization") != "Bearer test-provider-key" {
				t.Error("OpenAI stream auth")
			}
			fmt.Fprint(w, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Native reply\"}}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer endpoint.Close()
	runtime, store := fixture(t, idleProvider())
	runtime.options.BYOKProviders = []BYOKDefinition{{ID: "openai", Name: "OpenAI", Kind: "openai", BaseURL: endpoint.URL + "/openai/v1"}, {ID: "anthropic", Name: "Claude", Kind: "anthropic", BaseURL: endpoint.URL + "/anthropic/v1"}, {ID: "gemini", Name: "Gemini", Kind: "gemini", BaseURL: endpoint.URL + "/gemini/v1beta"}}
	ctx := context.Background()
	owner := c.Principal{ID: "alice"}
	for _, id := range []string{"openai", "anthropic", "gemini"} {
		saved, err := runtime.SaveProviderKey(ctx, owner, id, "test-provider-key")
		if err != nil || !saved.Connected || len(saved.Models) != 1 {
			t.Fatalf("connect %s: %+v %v", id, saved, err)
		}
		raw, err := store.Setting(ctx, owner.ID, "credential."+id)
		if err != nil || strings.Contains(raw, "test-provider-key") {
			t.Fatal("provider key stored as plaintext")
		}
		entry, model, err := runtime.resolveFor(ctx, owner, saved.Models[0].ID, false)
		if err != nil {
			t.Fatal(err)
		}
		answer := ""
		err = entry.provider.Stream(ctx, ProviderInput{Model: model.ID, Messages: []c.Message{{Role: "user", Content: "hello"}}}, func(delta ProviderDelta) error { answer += delta.Text; return nil })
		if err != nil || answer != "Native reply" {
			t.Fatalf("%s stream %q %v", id, answer, err)
		}
	}
	settings, err := runtime.ProviderSettings(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(settings)
	if bytes.Contains(raw, []byte("test-provider-key")) || bytes.Contains(raw, []byte("ciphertext")) {
		t.Fatal("settings leaked credentials")
	}
	other, err := runtime.ProviderSettings(ctx, c.Principal{ID: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range other {
		if provider.Connected {
			t.Fatal("provider key crossed user boundary")
		}
	}
	service, err := runtime.credentials()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := service.load(ctx, owner.ID, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.open("bob", "openai", saved.Ciphertext); err == nil {
		t.Fatal("ciphertext could be moved across owners")
	}
	saved.Ciphertext[len(saved.Ciphertext)-1] ^= 1
	if _, err = service.open(owner.ID, "openai", saved.Ciphertext); err == nil {
		t.Fatal("tampered ciphertext decrypted")
	}
	if err = runtime.DeleteProviderKey(ctx, owner, "openai"); err != nil {
		t.Fatal(err)
	}
	settings, _ = runtime.ProviderSettings(ctx, owner)
	if settings[0].Connected {
		t.Fatal("provider key was not removed")
	}
	server := httptest.NewServer(NewAPI(runtime, HTTPOptions{Authenticate: AuthFunc(func(*http.Request) (c.Principal, error) { return owner, nil })}))
	defer server.Close()
	response, err := http.Get(server.URL + "/api/tamizhi/providers")
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || bytes.Contains(payload, []byte("test-provider-key")) {
		t.Fatal("HTTP provider settings leaked key")
	}
}
func TestBYOKRejectsInvalidConnectionWithoutSavingKey(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"test-provider-key"}`)
	}))
	defer endpoint.Close()
	runtime, store := fixture(t, idleProvider())
	runtime.options.BYOKProviders = []BYOKDefinition{{ID: "openai", Name: "OpenAI", Kind: "openai", BaseURL: endpoint.URL}}
	_, err := runtime.SaveProviderKey(context.Background(), c.Principal{ID: "alice"}, "openai", "test-provider-key")
	if err == nil || strings.Contains(err.Error(), "test-provider-key") {
		t.Fatal("provider error leaked key or was accepted")
	}
	if _, err = store.Setting(context.Background(), "alice", "credential.openai"); err == nil {
		t.Fatal("invalid provider key was saved")
	}
}

func TestNativeBYOKToolRoundTrip(t *testing.T) {
	for _, kind := range []string{"anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			var requests int
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/models") {
					if kind == "anthropic" {
						fmt.Fprint(w, `{"data":[{"id":"claude-tools"}]}`)
					} else {
						fmt.Fprint(w, `{"models":[{"name":"models/gemini-tools","supportedGenerationMethods":["generateContent"]}]}`)
					}
					return
				}
				body, _ := io.ReadAll(r.Body)
				requests++
				w.Header().Set("Content-Type", "text/event-stream")
				if kind == "anthropic" {
					if requests == 1 {
						fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"native-call\",\"name\":\"read_number\",\"input\":{}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
					} else {
						if !bytes.Contains(body, []byte(`"tool_result"`)) || !bytes.Contains(body, []byte(`"tool_use_id":"native-call"`)) {
							t.Error("Anthropic tool result was not linked to the call")
						}
						fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Tool verified\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
					}
				} else {
					if requests == 1 {
						fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"id\":\"native-call\",\"name\":\"read_number\",\"args\":{}},\"thoughtSignature\":\"AQID\"}]}}]}\n\n")
					} else {
						if !bytes.Contains(body, []byte(`"thoughtSignature":"AQID"`)) || !bytes.Contains(body, []byte(`"functionResponse"`)) || !bytes.Contains(body, []byte(`"name":"read_number"`)) {
							t.Error("Gemini tool identity or thought signature was lost")
						}
						fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Tool verified\"}]}}]}\n\n")
					}
				}
			}))
			defer endpoint.Close()
			runtime, store := fixture(t, idleProvider())
			base := endpoint.URL + "/v1"
			model := "claude-tools"
			if kind == "gemini" {
				base = endpoint.URL + "/v1beta"
				model = "gemini-tools"
			}
			runtime.options.BYOKProviders = []BYOKDefinition{{ID: kind, Name: kind, Kind: kind, BaseURL: base}}
			principal := c.Principal{ID: "native-owner"}
			if _, err := runtime.SaveProviderKey(context.Background(), principal, kind, "native-fixture-key"); err != nil {
				t.Fatal(err)
			}
			if err := runtime.Mount(ToolPlugin(Tool{Name: "read_number", Description: "read number", Risk: "read", Schema: objectSchema(map[string]any{}), Execute: func(context.Context, json.RawMessage, ToolContext) (any, error) {
				return map[string]int{"number": 7}, nil
			}}), true); err != nil {
				t.Fatal(err)
			}
			input := request("native-tools")
			input.Model = kind + "/" + model
			run, err := runtime.StartRun(principal, input)
			if err != nil {
				t.Fatal(err)
			}
			if waitRun(t, store, principal.ID, run.ID).Status != "complete" {
				t.Fatal("native tool loop failed")
			}
			item, err := store.Conversation(context.Background(), principal.ID, input.ConversationID)
			if err != nil || item.Messages[len(item.Messages)-1].Content != "Tool verified" || requests != 2 {
				t.Fatal("native tool round trip did not finish", err)
			}
		})
	}
}
