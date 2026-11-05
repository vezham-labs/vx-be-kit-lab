package core

import (
	"context"
	"encoding/json"
	"errors"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnavailableModelHasActionableResponseAndDoesNotSavePrompt(t *testing.T) {
	runtime, store := fixture(t, idleProvider())
	principal := c.Principal{ID: "no-model-owner"}
	api := NewAPI(runtime, HTTPOptions{Authenticate: AuthFunc(func(*http.Request) (c.Principal, error) { return principal, nil })})
	payload := request("unavailable-model")
	payload.Model = "missing/model"
	raw, _ := json.Marshal(payload)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest("POST", "/api/tamizhi/chat", strings.NewReader(string(raw))))
	var result c.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != 503 || result.Code != "model_unavailable" || !strings.Contains(result.Message, "AI settings") {
		t.Fatal(response.Code, result)
	}
	if _, err := store.Conversation(context.Background(), principal.ID, payload.ConversationID); !errors.Is(err, ErrNotFound) {
		t.Fatal("rejected prompt was persisted", err)
	}
	payload.Model = "test/model"
	if _, err := runtime.StartRun(principal, payload); err != nil {
		t.Fatal("failed request retained the conversation lock", err)
	}
}
