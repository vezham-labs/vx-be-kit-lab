package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

type fixtureProvider struct {
	stream func(context.Context, ProviderInput, func(ProviderDelta) error) error
}

func (p fixtureProvider) Models() []c.ModelInfo {
	return []c.ModelInfo{{ID: "test/model", Name: "Test", Provider: "test", Modalities: []string{"text"}}}
}
func (p fixtureProvider) Stream(ctx context.Context, input ProviderInput, emit func(ProviderDelta) error) error {
	return p.stream(ctx, input, emit)
}
func fixture(t *testing.T, provider Provider) (*Runtime, *Store) {
	t.Helper()
	store, err := OpenStore(context.Background(), "sqlite", filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(context.Background(), store, RuntimeOptions{CredentialKey: bytes.Repeat([]byte{1}, 32), DefaultModel: "test/model", RunTimeout: 5 * time.Second, ApprovalTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close(); store.Close() })
	if err = runtime.Mount(ProviderPlugin("test", provider), true); err != nil {
		t.Fatal(err)
	}
	return runtime, store
}
func request(id string) c.RunRequest {
	return c.RunRequest{ConversationID: id, Permission: c.PermissionSafe, Messages: []c.Message{{ID: "prompt", Role: "user", Content: "hello"}}}
}
func waitRun(t *testing.T, store *Store, owner, id string) c.RunInfo {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, _, err := store.Run(context.Background(), owner, id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != "running" {
			return run
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("run did not terminate")
	return c.RunInfo{}
}
func TestDurableRunOwnershipBranchAndRecovery(t *testing.T) {
	runtime, store := fixture(t, fixtureProvider{stream: func(ctx context.Context, input ProviderInput, emit func(ProviderDelta) error) error {
		for _, text := range []string{"hello ", "world"} {
			if err := emit(ProviderDelta{Text: text}); err != nil {
				return err
			}
		}
		return nil
	}})
	owner := c.Principal{ID: "alice"}
	run, err := runtime.StartRun(owner, request("conversation"))
	if err != nil {
		t.Fatal(err)
	}
	if status := waitRun(t, store, owner.ID, run.ID).Status; status != "complete" {
		t.Fatal(status)
	}
	events, err := store.RunEvents(context.Background(), owner.ID, run.ID, 0)
	if err != nil || len(events) != 4 {
		t.Fatalf("events: %v %v", events, err)
	}
	for i, event := range events {
		if event.Sequence != int64(i+1) {
			t.Fatal("sequence gap")
		}
	}
	if _, err = store.RunEvents(context.Background(), "bob", run.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user replay: %v", err)
	}
	item, err := store.Conversation(context.Background(), owner.ID, "conversation")
	if err != nil || item.Messages[1].Content != "hello world" {
		t.Fatalf("conversation: %+v %v", item, err)
	}
	checkpoint, err := store.Checkpoint(context.Background(), owner.ID, item.ID, "before branch")
	if err != nil {
		t.Fatal(err)
	}
	branch, err := store.Branch(context.Background(), owner.ID, item.ID, c.BranchRequest{MessageID: item.Messages[1].ID})
	if err != nil || branch.ID == item.ID || branch.ParentID != item.ID {
		t.Fatalf("branch: %+v %v", branch, err)
	}
	if _, err = store.RestoreCheckpoint(context.Background(), owner.ID, branch.ID, checkpoint.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("checkpoint must belong to selected conversation")
	}
	version := int64(0)
	if _, err = store.SaveConversation(context.Background(), owner.ID, item, &version, "test"); !errors.Is(err, ErrConflict) {
		t.Fatal("expected version conflict")
	}
	pending, err := store.CreateRun(context.Background(), owner.ID, request("conversation"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RecoverRuns(context.Background()); err != nil {
		t.Fatal(err)
	}
	saved, _, err := store.Run(context.Background(), owner.ID, pending.ID)
	if err != nil || saved.Status != "interrupted" {
		t.Fatalf("recovery: %+v %v", saved, err)
	}
}
func TestApprovalOwnerAndToolResult(t *testing.T) {
	var executions atomic.Int32
	provider := fixtureProvider{stream: func(ctx context.Context, input ProviderInput, emit func(ProviderDelta) error) error {
		if input.Messages[len(input.Messages)-1].Role == "tool" {
			return emit(ProviderDelta{Text: "done"})
		}
		return emit(ProviderDelta{Calls: []c.ToolCall{{ID: "call", Name: "write_test", Arguments: `{"value":"ok"}`}}})
	}}
	runtime, store := fixture(t, provider)
	if err := runtime.Mount(ToolPlugin(Tool{Name: "write_test", Description: "test", Risk: "write", Schema: objectSchema(map[string]any{"value": stringSchema(10)}, "value"), Execute: func(ctx context.Context, args json.RawMessage, tool ToolContext) (any, error) {
		executions.Add(1)
		return map[string]string{"result": "ok"}, nil
	}}), true); err != nil {
		t.Fatal(err)
	}
	run, err := runtime.StartRun(c.Principal{ID: "alice"}, request("approval"))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	ready := false
	for time.Now().Before(deadline) {
		runtime.mu.RLock()
		ready = runtime.approvals[run.ID+":call"] != nil
		runtime.mu.RUnlock()
		if ready {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !ready {
		t.Fatal("approval not requested")
	}
	decision := c.ApprovalDecision{RunID: run.ID, CallID: "call", Approved: true}
	if runtime.Approve(c.Principal{ID: "bob"}, decision) {
		t.Fatal("other owner approved")
	}
	if !runtime.Approve(c.Principal{ID: "alice"}, decision) {
		t.Fatal("owner approval failed")
	}
	if runtime.Approve(c.Principal{ID: "alice"}, decision) {
		t.Fatal("approval replay succeeded")
	}
	if status := waitRun(t, store, "alice", run.ID).Status; status != "complete" || executions.Load() != 1 {
		t.Fatalf("status %s, executions %d", status, executions.Load())
	}
}
func TestCancelAndProviderUnload(t *testing.T) {
	started := make(chan struct{})
	runtime, store := fixture(t, fixtureProvider{stream: func(ctx context.Context, _ ProviderInput, _ func(ProviderDelta) error) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}})
	run, err := runtime.StartRun(c.Principal{ID: "alice"}, request("cancel"))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err = runtime.Cancel(c.Principal{ID: "bob"}, run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("other owner canceled run")
	}
	if err = runtime.SetPlugin("provider.test", false); err != nil {
		t.Fatal(err)
	}
	if status := waitRun(t, store, "alice", run.ID).Status; status != "canceled" {
		t.Fatal(status)
	}
}
func TestHTTPStreamReplayAndAuthentication(t *testing.T) {
	runtime, _ := fixture(t, fixtureProvider{stream: func(ctx context.Context, _ ProviderInput, emit func(ProviderDelta) error) error {
		return emit(ProviderDelta{Text: "streamed"})
	}})
	token := "development-token-at-least-24-characters"
	auth, err := NewAuth(context.Background(), runtime.store, []TokenCredential{{Token: token, Principal: c.Principal{ID: "alice"}}}, OIDCOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewAPI(runtime, HTTPOptions{Authenticate: auth, Origins: []string{"https://app.example"}}))
	defer server.Close()
	response, err := http.Get(server.URL + "/api/tamizhi/models")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal(response.StatusCode)
	}
	body, _ := json.Marshal(request("http"))
	req, _ := http.NewRequest("POST", server.URL+"/api/tamizhi/chat", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("HTTP %d: %s", response.StatusCode, raw)
	}
	scanner := bufio.NewScanner(response.Body)
	var events []c.StreamEvent
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			var event c.StreamEvent
			if err = json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &event); err != nil {
				t.Fatal(err)
			}
			events = append(events, event)
		}
	}
	response.Body.Close()
	if len(events) != 3 || events[1].Delta != "streamed" {
		t.Fatalf("events: %+v", events)
	}
	req, _ = http.NewRequest("GET", fmt.Sprintf("%s/api/tamizhi/runs/%s/events?after=1", server.URL, events[0].RunID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if strings.Contains(string(raw), `"type":"start"`) || !strings.Contains(string(raw), `"type":"finish"`) {
		t.Fatalf("bad replay: %s", raw)
	}
	req, _ = http.NewRequest("POST", server.URL+"/api/tamizhi/chat", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "https://evil.example")
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("untrusted origin accepted")
	}
}
func TestOpenAIProviderStreamingWire(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer fake-key" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input["model"] != "model" || input["stream"] != true {
			t.Errorf("invalid provider input: %v", input)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"id\":\"x\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
	}))
	defer endpoint.Close()
	provider := &OpenAIProvider{Options: OpenAIProviderOptions{ID: "test", BaseURL: endpoint.URL, ResolveSecret: func(context.Context) (string, error) { return "fake-key", nil }}}
	text := ""
	var usage c.Usage
	err := provider.Stream(context.Background(), ProviderInput{Model: "test/model", Messages: []c.Message{{Role: "user", Content: "hi"}}}, func(delta ProviderDelta) error { text += delta.Text; usage = delta.Usage; return nil })
	if err != nil || text != "hello" || usage.TotalTokens != 5 {
		t.Fatalf("provider result %q %+v %v", text, usage, err)
	}
}
