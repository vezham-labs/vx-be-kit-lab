package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("TAMIZHI_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set TAMIZHI_TEST_POSTGRES_URL to test PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	store, err := OpenStore(ctx, "pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// Opening again verifies that schema initialization is repeatable.
	second, err := OpenStore(ctx, "pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
	owner := "test-" + newID()
	id := newID()
	item, err := store.SaveConversation(ctx, owner, c.Conversation{ID: id, Title: "Postgres CRUD", Messages: []c.Message{{ID: "user", Role: "user", Content: "hello"}, {ID: "answer", Role: "assistant", Content: "saved answer"}}}, nil, "test.created")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Conversation(ctx, "another-user", id); !errors.Is(err, ErrNotFound) {
		t.Fatal("conversation ownership", err)
	}
	stale := item.Version - 1
	if _, err = store.SaveConversation(ctx, owner, item, &stale, "test.conflict"); !errors.Is(err, ErrConflict) {
		t.Fatal("expected PostgreSQL conflict", err)
	}
	item, err = store.MutateConversation(ctx, owner, id, "test.renamed", func(item *c.Conversation) error {
		item.Title = "Renamed PostgreSQL"
		item.Messages[1].Rating = "like"
		item.Messages[1].Tasks = []c.Task{{Label: "Task", Done: true}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Conversation(ctx, owner, id)
	if err != nil || saved.Title != item.Title || saved.Messages[1].Rating != "like" || !saved.Messages[1].Tasks[0].Done {
		t.Fatal("persisted rename/feedback/tasks", err)
	}
	checkpoint, err := store.Checkpoint(ctx, owner, id, "before edits")
	if err != nil {
		t.Fatal(err)
	}
	checkpoints, err := store.Checkpoints(ctx, owner, id)
	if err != nil || len(checkpoints) != 1 {
		t.Fatal("checkpoint list", err)
	}
	branch, err := store.Branch(ctx, owner, id, c.BranchRequest{MessageID: "answer", Title: "Neon branch chat"})
	if err != nil || branch.ParentID != id {
		t.Fatal("conversation branching", err)
	}
	if _, err = store.MutateConversation(ctx, owner, id, "test.edit", func(item *c.Conversation) error {
		item.Messages = append(item.Messages, c.Message{ID: "extra", Role: "user", Content: "more"})
		item.Summary = "changed"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	restored, err := store.RestoreCheckpoint(ctx, owner, id, checkpoint.ID)
	if err != nil || len(restored.Messages) != 2 || restored.Summary != "" {
		t.Fatal("checkpoint restore", err)
	}
	page, err := store.Conversations(ctx, owner, "", "", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal("first cursor page", err)
	}
	next, err := store.Conversations(ctx, owner, "", page.NextCursor, 1)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal("cursor pagination", err)
	}
	found, err := store.Conversations(ctx, owner, "Renamed", "", 10)
	if err != nil || len(found.Items) != 1 || found.Items[0].ID != id {
		t.Fatal("title search", err)
	}
	run, err := store.CreateRun(ctx, owner, request(id), "")
	if err != nil {
		t.Fatal(err)
	}
	for i, kind := range []string{"start", "text", "finish"} {
		event, err := store.AppendRunEvent(ctx, owner, c.StreamEvent{RunID: run.ID, ConversationID: id, Type: kind, Delta: "persisted", Reason: "complete"})
		if err != nil || event.Sequence != int64(i+1) {
			t.Fatal("run sequence", err)
		}
	}
	events, err := store.RunEvents(ctx, owner, run.ID, 1)
	if err != nil || len(events) != 2 || events[0].Sequence != 2 {
		t.Fatal("durable replay", err)
	}
	if _, err = store.RunEvents(ctx, "another-user", run.ID, 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("replay ownership", err)
	}
	if _, complete, err := store.BeginTool(ctx, run.ID, "call"); err != nil || complete {
		t.Fatal("initial tool attempt", err)
	}
	if err = store.EndTool(ctx, run.ID, "call", map[string]string{"result": "saved"}); err != nil {
		t.Fatal(err)
	}
	if raw, complete, err := store.BeginTool(ctx, run.ID, "call"); err != nil || !complete || !strings.Contains(string(raw), "saved") {
		t.Fatal("tool execution deduplication", err)
	}
	if err = store.FinishRun(ctx, owner, run.ID, "complete", c.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}); err != nil {
		t.Fatal(err)
	}
	completed, _, err := store.Run(ctx, owner, run.ID)
	if err != nil || completed.Status != "complete" || completed.Usage.TotalTokens != 5 {
		t.Fatal("run usage", err)
	}
	attachment := c.Attachment{ID: newID(), Name: "notes.txt", MimeType: "text/plain", Size: 5}
	if err = store.PutAttachment(ctx, owner, attachment, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, content, err := store.Attachment(ctx, owner, attachment.ID)
	if err != nil || string(content) != "hello" {
		t.Fatal("attachment bytes", err)
	}
	if _, _, err = store.Attachment(ctx, "another-user", attachment.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("attachment ownership", err)
	}
	fact, err := store.AddMemory(ctx, owner, c.MemoryRequest{Text: "Postgres is configured", Tags: []string{"test"}})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := store.Memories(ctx, owner, "Postgres")
	if err != nil || len(facts) != 1 || facts[0].ID != fact.ID {
		t.Fatal("memory retrieval", err)
	}
	if err = store.DeleteMemory(ctx, owner, fact.ID); err != nil {
		t.Fatal(err)
	}
	facts, err = store.Memories(ctx, owner, "Postgres")
	if err != nil || len(facts) != 0 {
		t.Fatal("memory deletion", err)
	}
	for _, value := range []string{"true", "false"} {
		if err = store.SetSetting(ctx, owner, "plugin.test", value); err != nil {
			t.Fatal(err)
		}
	}
	if value, err := store.Setting(ctx, owner, "plugin.test"); err != nil || value != "false" {
		t.Fatal("settings upsert", err)
	}
	runtime, err := NewRuntime(ctx, store, RuntimeOptions{DefaultModel: "test/model"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err = runtime.Mount(ProviderPlugin("test", fixtureProvider{stream: func(ctx context.Context, input ProviderInput, emit func(ProviderDelta) error) error {
		return emit(ProviderDelta{Text: "Neon-backed reply"})
	}}), true); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewAPI(runtime, HTTPOptions{Authenticate: AuthFunc(func(*http.Request) (c.Principal, error) { return c.Principal{ID: owner}, nil })}))
	defer server.Close()
	input := request(newID())
	body, _ := json.Marshal(input)
	response, err := http.Post(server.URL+"/api/tamizhi/chat", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	wire, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(wire), "Neon-backed reply") || !strings.Contains(string(wire), `"type":"finish"`) {
		t.Fatalf("Postgres-backed HTTP stream: %d %v", response.StatusCode, err)
	}
	answer, err := store.Conversation(ctx, owner, input.ConversationID)
	if err != nil || len(answer.Messages) != 2 || answer.Messages[1].Content != "Neon-backed reply" {
		t.Fatal("HTTP reply persistence", err)
	}
	if err = store.DeleteConversation(ctx, owner, branch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Conversation(ctx, owner, branch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted conversation remains visible", err)
	}
}
