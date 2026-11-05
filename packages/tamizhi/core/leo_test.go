package core

import (
	"context"
	"encoding/json"
	"errors"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"strings"
	"testing"
	"time"
)

func mountLeo(t *testing.T) (*Runtime, *Store) {
	t.Helper()
	runtime, store := fixture(t, idleProvider())
	runtime.options.DefaultModel = LeoModelID
	if err := runtime.Mount(LeoPlugin(), true); err != nil {
		t.Fatal(err)
	}
	return runtime, store
}
func TestLeoDefaultGreetsWithoutProviderOrTrainedAnswer(t *testing.T) {
	runtime, store := mountLeo(t)
	principal := c.Principal{ID: "leo-owner"}
	models, err := runtime.ModelsFor(context.Background(), principal)
	if err != nil || len(models) < 1 || models[0].ID != LeoModelID {
		t.Fatal(models, err)
	}
	for _, prompt := range []string{"hi", "Write an essay about a dragon"} {
		input := request(newID())
		input.Messages[0].Content = prompt
		input.Model = "Auto"
		run, err := runtime.StartRun(principal, input)
		if err != nil {
			t.Fatal(err)
		}
		if status := waitRun(t, store, principal.ID, run.ID).Status; status != "complete" {
			t.Fatal(status)
		}
		item, _ := store.Conversation(context.Background(), principal.ID, input.ConversationID)
		answer := item.Messages[len(item.Messages)-1].Content
		if prompt == "hi" && !strings.Contains(answer, "Leo") {
			t.Fatal(answer)
		}
		if prompt != "hi" && !strings.Contains(answer, "Tell me what") {
			t.Fatal(answer)
		}
		events, _ := store.RunEvents(context.Background(), principal.ID, run.ID, 0)
		chunks := 0
		for _, event := range events {
			if event.Type == "text" {
				chunks++
			}
			if event.Type == "tool" {
				t.Fatal("unsupported request executed a tool")
			}
		}
		if chunks < 2 {
			t.Fatal("answer was not streamed in chunks")
		}
	}
	if err := runtime.SetPlugin("provider.leo", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.resolveFor(context.Background(), principal, LeoModelID, false); !errors.Is(err, ErrModelUnavailable) {
		t.Fatal("disabled Leo resolved", err)
	}
}
func TestLeoResearchUsesRealToolsAndDoesNotObeyPageInstructions(t *testing.T) {
	runtime, store := mountLeo(t)
	if err := runtime.Mount(WebPlugin(WebOptions{Client: webFixtureClient(), Search: webFixtureSearch}), true); err != nil {
		t.Fatal(err)
	}
	input := request("leo-research")
	input.Model = LeoModelID
	input.Messages[0].Content = "/search What is the latest React version?"
	principal := c.Principal{ID: "research-owner"}
	run, err := runtime.StartRun(principal, input)
	if err != nil {
		t.Fatal(err)
	}
	if waitRun(t, store, principal.ID, run.ID).Status != "complete" {
		t.Fatal("research failed")
	}
	events, _ := store.RunEvents(context.Background(), principal.ID, run.ID, 0)
	names := []string{}
	answer := ""
	for _, event := range events {
		if event.Type == "tool" {
			names = append(names, event.Name)
		}
		if event.Type == "text" {
			answer += event.Delta
		}
	}
	if strings.Join(names, ",") != "web_search,web_open,web_read" || !strings.Contains(answer, "99.0.0") || !strings.Contains(answer, "https://react.dev/versions") {
		t.Fatal(names, answer)
	}
	if strings.Contains(answer, "PRIVATE") {
		t.Fatal("hidden source content escaped")
	}
	// Malicious prose is quoted as data and never schedules an additional action.
	if !strings.Contains(answer, "> ") || len(names) != 3 {
		t.Fatal(answer)
	}
}
func TestLeoMemoryApprovalAndOwnerIsolation(t *testing.T) {
	runtime, store := mountLeo(t)
	if err := runtime.Mount(MemoryPlugin(), true); err != nil {
		t.Fatal(err)
	}
	owner := c.Principal{ID: "notes-owner"}
	input := request("leo-remember")
	input.Model = LeoModelID
	input.Messages[0].Content = "/remember launch code is blue"
	run, err := runtime.StartRun(owner, input)
	if err != nil {
		t.Fatal(err)
	}
	var approval c.StreamEvent
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events, _ := store.RunEvents(context.Background(), owner.ID, run.ID, 0)
		for _, event := range events {
			if event.Type == "approval" {
				approval = event
			}
		}
		if approval.CallID != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if approval.CallID == "" {
		t.Fatal("memory write bypassed approval")
	}
	if !runtime.Approve(owner, c.ApprovalDecision{RunID: run.ID, CallID: approval.CallID, Approved: true}) {
		t.Fatal("approval failed")
	}
	if waitRun(t, store, owner.ID, run.ID).Status != "complete" {
		t.Fatal("memory write failed")
	}
	for _, principal := range []c.Principal{owner, {ID: "other-owner"}} {
		input := request(newID())
		input.Model = LeoModelID
		input.Messages[0].Content = "/recall launch"
		run, err := runtime.StartRun(principal, input)
		if err != nil {
			t.Fatal(err)
		}
		if waitRun(t, store, principal.ID, run.ID).Status != "complete" {
			t.Fatal("recall failed")
		}
		item, _ := store.Conversation(context.Background(), principal.ID, input.ConversationID)
		found := strings.Contains(item.Messages[len(item.Messages)-1].Content, "launch code is blue")
		if found != (principal.ID == owner.ID) {
			t.Fatal("memory ownership changed", principal.ID)
		}
	}
}
func TestLeoExplicitToolsValidateAvailabilityAndJSON(t *testing.T) {
	provider := &LeoProvider{}
	for _, prompt := range []string{"/tool absent {}", "/tool absent null", "/tool absent []", "/tool absent {broken}"} {
		text := ""
		calls := 0
		err := provider.Stream(context.Background(), ProviderInput{Messages: []c.Message{{Role: "user", Content: prompt}}}, func(delta ProviderDelta) error { text += delta.Text; calls += len(delta.Calls); return nil })
		if err != nil || text == "" || calls != 0 {
			t.Fatal(prompt, text, calls, err)
		}
	}
	runtime, store := mountLeo(t)
	var executed bool
	if err := runtime.Mount(ToolPlugin(Tool{Name: "mcp_report", Risk: "read", Schema: objectSchema(map[string]any{}), Execute: func(context.Context, json.RawMessage, ToolContext) (any, error) {
		executed = true
		return map[string]int{"total": 42}, nil
	}}), true); err != nil {
		t.Fatal(err)
	}
	input := request("leo-mcp")
	input.Model = LeoModelID
	input.Messages[0].Content = "/tool mcp_report {}"
	run, err := runtime.StartRun(c.Principal{ID: "tool-owner"}, input)
	if err != nil {
		t.Fatal(err)
	}
	if waitRun(t, store, "tool-owner", run.ID).Status != "complete" || !executed {
		t.Fatal("registered tool was not executed")
	}
	item, _ := store.Conversation(context.Background(), "tool-owner", input.ConversationID)
	if !strings.Contains(item.Messages[len(item.Messages)-1].Content, "42") {
		t.Fatal(item)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := provider.Stream(canceled, ProviderInput{}, func(ProviderDelta) error { t.Fatal("cancelled stream emitted"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLeoDenialDoesNotRetryOrWrite(t *testing.T) {
	runtime, store := mountLeo(t)
	if err := runtime.Mount(MemoryPlugin(), true); err != nil {
		t.Fatal(err)
	}
	owner := c.Principal{ID: "denied-owner"}
	input := request("leo-denied")
	input.Model = LeoModelID
	input.Messages[0].Content = "/remember do not save this"
	run, err := runtime.StartRun(owner, input)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	approved := false
	for time.Now().Before(deadline) {
		events, _ := store.RunEvents(context.Background(), owner.ID, run.ID, 0)
		for _, event := range events {
			if event.Type == "approval" {
				approved = runtime.Approve(owner, c.ApprovalDecision{RunID: run.ID, CallID: event.CallID, Approved: false})
				break
			}
		}
		if approved {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !approved || waitRun(t, store, owner.ID, run.ID).Status != "complete" {
		t.Fatal("denial was not handled")
	}
	notes, _ := store.Memories(context.Background(), owner.ID, "")
	if len(notes) != 0 {
		t.Fatal("denied memory was saved")
	}
	events, _ := store.RunEvents(context.Background(), owner.ID, run.ID, 0)
	attempts := 0
	for _, event := range events {
		if event.Type == "tool" {
			attempts++
		}
	}
	if attempts != 1 {
		t.Fatal("denied tool was retried", attempts)
	}
}
func TestLeoOnlyUsesCurrentTurnAndSupportsWorkflowPlugins(t *testing.T) {
	input := ProviderInput{Messages: []c.Message{
		{Role: "user", Content: "/remember stale"},
		{Role: "assistant", ToolCalls: []c.ToolCall{{ID: "old", Name: "remember", Arguments: `{"text":"stale"}`}}},
		{Role: "tool", ToolCallID: "old", Content: `{"text":"stale"}`},
		{Role: "user", Content: "hi"},
	}}
	turn := leoTurn(input)
	if turn.Prompt != "hi" || len(turn.Results) != 0 {
		t.Fatal(turn)
	}
	provider := &LeoProvider{Workflows: []LeoWorkflow{func(turn LeoTurn) (LeoStep, bool) {
		return LeoStep{Text: "Custom workspace workflow"}, turn.Prompt == "hi"
	}}}
	answer := ""
	if err := provider.Stream(context.Background(), input, func(delta ProviderDelta) error { answer += delta.Text; return nil }); err != nil || answer != "Custom workspace workflow" {
		t.Fatal(answer, err)
	}
}
func TestLeoCompactionUsesVerbatimBoundedContext(t *testing.T) {
	runtime, store := mountLeo(t)
	owner := c.Principal{ID: "compact-owner"}
	item := c.Conversation{ID: "leo-context", Title: "Context"}
	for i := 0; i < 20; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		item.Messages = append(item.Messages, c.Message{ID: newID(), Role: role, Content: "Exact recorded fact"})
	}
	saved, err := store.SaveConversation(context.Background(), owner.ID, item, nil, "test.seed")
	if err != nil {
		t.Fatal(err)
	}
	compacted, err := runtime.Compact(context.Background(), owner, item.ID, LeoModelID)
	if err != nil || !strings.Contains(compacted.Summary, "Exact recorded fact") || !strings.Contains(compacted.Summary, "verbatim") || len(compacted.Messages) != len(saved.Messages) {
		t.Fatal(compacted, err)
	}
}
