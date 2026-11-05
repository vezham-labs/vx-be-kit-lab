package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	cordis "github.com/tr1v3r/cordis-go"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

type RuntimeOptions struct {
	GeminiOAuth       *GeminiOAuthOptions
	CredentialKey     []byte
	CredentialKeyFile string
	BYOKProviders     []BYOKDefinition
	BYOKHTTPClient    *http.Client
	DefaultModel      string
	SystemPrompt      string
	AllowFullAccess   bool
	MaxRuns           int
	MaxSteps          int
	MaxDepth          int
	MaxParallelTools  int
	RunTimeout        time.Duration
	ApprovalTimeout   time.Duration
	CompactThreshold  int
	Logger            *slog.Logger
}
type activeRun struct {
	parent       string
	owner        string
	conversation string
	cancel       context.CancelFunc
	notify       chan struct{}
	done         chan struct{}
}
type pendingApproval struct {
	owner    string
	decision chan bool
}
type Runtime struct {
	web             *webState
	ctx             context.Context
	cancel          context.CancelFunc
	root            *cordis.Context
	store           *Store
	options         RuntimeOptions
	closeOnce       sync.Once
	credentialOnce  sync.Once
	credentialStore *credentialService
	credentialError error
	closed          bool
	mu              sync.RWMutex
	pluginMu        sync.Mutex
	plugins         map[string]*pluginEntry
	providers       map[string]providerEntry
	tools           map[string]Tool
	runs            map[string]*activeRun
	sessions        map[string]string
	approvals       map[string]*pendingApproval
	wg              sync.WaitGroup
}

func NewRuntime(parent context.Context, store *Store, options RuntimeOptions) (*Runtime, error) {
	if options.MaxRuns <= 0 {
		options.MaxRuns = 32
	}
	if options.MaxSteps <= 0 {
		options.MaxSteps = 8
	}
	if options.MaxDepth <= 0 {
		options.MaxDepth = 2
	}
	if options.MaxParallelTools <= 0 {
		options.MaxParallelTools = 4
	}
	if options.RunTimeout <= 0 {
		options.RunTimeout = 3 * time.Minute
	}
	if options.ApprovalTimeout <= 0 {
		options.ApprovalTimeout = time.Minute
	}
	if options.CompactThreshold <= 0 {
		options.CompactThreshold = 100000
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(parent)
	r := &Runtime{ctx: ctx, cancel: cancel, root: cordis.New(), store: store, options: options, plugins: map[string]*pluginEntry{}, providers: map[string]providerEntry{}, tools: map[string]Tool{}, runs: map[string]*activeRun{}, sessions: map[string]string{}, approvals: map[string]*pendingApproval{}}
	if err := store.RecoverRuns(ctx); err != nil {
		cancel()
		r.root.Fiber().Dispose()
		return nil, err
	}
	if _, err := r.root.Provide("tamizhi", r); err != nil {
		return nil, err
	}
	return r, nil
}
func (r *Runtime) Close() {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.cancel()
		for _, run := range r.runs {
			run.cancel()
		}
		r.mu.Unlock()
		r.wg.Wait()
		r.pluginMu.Lock()
		r.root.Fiber().Dispose()
		r.pluginMu.Unlock()
	})
}
func (r *Runtime) emit(ctx context.Context, owner string, event c.StreamEvent) error {
	saved, err := r.store.AppendRunEvent(ctx, owner, event)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if active := r.runs[event.RunID]; active != nil {
		close(active.notify)
		active.notify = make(chan struct{})
	}
	r.mu.Unlock()
	r.root.Emit("tamizhi/event", saved)
	r.mu.RLock()
	active := r.runs[event.RunID]
	parentID := ""
	if active != nil {
		parentID = active.parent
	}
	parent := r.runs[parentID]
	r.mu.RUnlock()
	if parent != nil {
		if err = r.emit(ctx, owner, c.StreamEvent{Type: "subagent-event", RunID: parentID, ConversationID: parent.conversation, Output: saved}); err != nil {
			return err
		}
	}
	return nil
}
func (r *Runtime) StartRun(principal c.Principal, request c.RunRequest) (c.RunInfo, error) {
	return r.startRun(principal, request, "", 0, r.ctx)
}
func (r *Runtime) startRun(principal c.Principal, request c.RunRequest, parent string, depth int, parentContext context.Context) (c.RunInfo, error) {
	if principal.ID == "" {
		return c.RunInfo{}, ErrForbidden
	}
	if err := request.Validate(); err != nil {
		return c.RunInfo{}, err
	}
	key := principal.ID + "\x00" + request.ConversationID
	r.mu.Lock()
	if r.closed || r.ctx.Err() != nil {
		r.mu.Unlock()
		return c.RunInfo{}, errors.New("runtime is shutting down")
	}
	if _, busy := r.sessions[key]; busy || len(r.sessions) >= r.options.MaxRuns {
		r.mu.Unlock()
		return c.RunInfo{}, ErrConflict
	}
	r.sessions[key] = "starting"
	r.wg.Add(1)
	r.mu.Unlock()
	release := func() { r.mu.Lock(); delete(r.sessions, key); r.mu.Unlock(); r.wg.Done() }
	if _, _, err := r.resolveFor(parentContext, principal, request.Model, request.Fast); err != nil {
		release()
		return c.RunInfo{}, err
	}
	item, err := r.store.Conversation(parentContext, principal.ID, request.ConversationID)
	if errors.Is(err, ErrNotFound) {
		item = c.Conversation{ID: request.ConversationID, Title: request.Title, Messages: []c.Message{}}
		if item.Title == "" {
			item.Title = request.Messages[len(request.Messages)-1].Content
			if len(item.Title) > 80 {
				item.Title = item.Title[:80]
			}
		}
	} else if err != nil {
		release()
		return c.RunInfo{}, err
	}
	incoming := request.Messages[len(request.Messages)-1]
	if incoming.ID == "" {
		incoming.ID = newID()
	}
	if len(item.Messages) == 0 {
		item.Messages = request.Messages
		for i := range item.Messages {
			if item.Messages[i].ID == "" {
				item.Messages[i].ID = newID()
			}
		}
	} else {
		found := -1
		for i, message := range item.Messages {
			if message.ID == incoming.ID && message.Role == "user" {
				found = i
				break
			}
		}
		if found >= 0 {
			item.Messages = append(append([]c.Message{}, item.Messages[:found]...), incoming)
		} else {
			item.Messages = append(item.Messages, incoming)
		}
	}
	if len(item.Messages) > 200 {
		start := len(item.Messages) - 200
		for start < len(item.Messages) && item.Messages[start].Role != "user" {
			start++
		}
		item.Messages = item.Messages[start:]
	}
	item, err = r.store.SaveConversation(parentContext, principal.ID, item, request.ExpectedVersion, "user.message")
	if err != nil {
		release()
		return c.RunInfo{}, err
	}
	run, err := r.store.CreateRun(parentContext, principal.ID, request, parent)
	if err != nil {
		release()
		return run, err
	}
	ctx, cancel := context.WithTimeout(parentContext, r.options.RunTimeout)
	active := &activeRun{parent: parent, owner: principal.ID, conversation: item.ID, cancel: cancel, notify: make(chan struct{}), done: make(chan struct{})}
	r.mu.Lock()
	r.runs[run.ID] = active
	r.sessions[key] = run.ID
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		defer cancel()
		defer func() {
			r.mu.Lock()
			delete(r.runs, run.ID)
			delete(r.sessions, key)
			close(active.notify)
			close(active.done)
			r.mu.Unlock()
		}()
		r.drive(ctx, principal, request, item, run, depth)
	}()
	return run, nil
}
func (r *Runtime) Cancel(principal c.Principal, id string) error {
	r.mu.RLock()
	active := r.runs[id]
	r.mu.RUnlock()
	if active == nil {
		if _, _, err := r.store.Run(r.ctx, principal.ID, id); err != nil {
			return err
		}
		return nil
	}
	if active.owner != principal.ID {
		return ErrNotFound
	}
	active.cancel()
	return nil
}
func (r *Runtime) Approve(principal c.Principal, decision c.ApprovalDecision) bool {
	key := decision.RunID + ":" + decision.CallID
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := r.approvals[key]
	if pending == nil || pending.owner != principal.ID {
		return false
	}
	delete(r.approvals, key)
	pending.decision <- decision.Approved
	return true
}
func (r *Runtime) waitApproval(ctx context.Context, principal c.Principal, run c.RunInfo, call c.ToolCall, input any) bool {
	key := run.ID + ":" + call.ID
	pending := &pendingApproval{owner: principal.ID, decision: make(chan bool, 1)}
	r.mu.Lock()
	r.approvals[key] = pending
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.approvals, key); r.mu.Unlock() }()
	if err := r.emit(ctx, principal.ID, c.StreamEvent{Type: "approval", RunID: run.ID, ConversationID: run.ConversationID, CallID: call.ID, Name: call.Name, Input: input}); err != nil {
		return false
	}
	timer := time.NewTimer(r.options.ApprovalTimeout)
	defer timer.Stop()
	select {
	case approved := <-pending.decision:
		return approved
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}
func (r *Runtime) executeTool(ctx context.Context, principal c.Principal, request c.RunRequest, run c.RunInfo, call c.ToolCall, depth int) (output any) {
	r.mu.RLock()
	tool, exists := r.tools[call.Name]
	r.mu.RUnlock()
	if !exists {
		return map[string]any{"error": "Tool is not installed"}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(tool.lifetime, cancel)
	defer stop()
	if tool.lifetime.Err() != nil {
		return map[string]any{"error": "Tool was unloaded"}
	}

	var input any
	if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
		return map[string]any{"error": "Invalid tool arguments"}
	}
	if err := tool.validator.Validate(input); err != nil {
		return map[string]any{"error": "Tool arguments failed validation"}
	}
	if err := r.emit(ctx, principal.ID, c.StreamEvent{Type: "tool", RunID: run.ID, ConversationID: run.ConversationID, CallID: call.ID, Name: call.Name, Input: input}); err != nil {
		return map[string]any{"error": "Unable to record tool request"}
	}
	allowed := (request.Permission == c.PermissionFull && r.options.AllowFullAccess) || (request.Permission == c.PermissionSafe && tool.Risk == "read")
	if !allowed && !r.waitApproval(ctx, principal, run, call, input) {
		output = map[string]any{"error": "Tool request was not approved"}
		_ = r.emit(context.WithoutCancel(ctx), principal.ID, c.StreamEvent{Type: "tool-result", RunID: run.ID, ConversationID: run.ConversationID, CallID: call.ID, Name: call.Name, Output: output})
		return
	}
	if ctx.Err() != nil {
		return map[string]any{"error": "Run canceled"}
	}
	digest := sha256.Sum256([]byte(call.Name + "\x00" + call.Arguments))
	callKey := call.ID + ":" + hex.EncodeToString(digest[:])
	cached, complete, err := r.store.BeginTool(ctx, run.ID, callKey)
	if err != nil {
		return map[string]any{"error": "A prior tool attempt needs review"}
	}
	if complete {
		_ = json.Unmarshal(cached, &output)
		return
	}
	defer func() {
		if failure := recover(); failure != nil {
			output = map[string]any{"error": "Tool failed"}
		}
		if output == nil {
			output = map[string]any{"error": "Tool returned no result"}
		}
		_ = r.store.EndTool(context.WithoutCancel(ctx), run.ID, callKey, output)
		_ = r.emit(context.WithoutCancel(ctx), principal.ID, c.StreamEvent{Type: "tool-result", RunID: run.ID, ConversationID: run.ConversationID, CallID: call.ID, Name: call.Name, Output: output})
	}()
	output, err = tool.Execute(ctx, json.RawMessage(call.Arguments), ToolContext{Principal: principal, ConversationID: run.ConversationID, RunID: run.ID, Depth: depth, Permission: request.Permission, Runtime: r})
	if err != nil {
		output = map[string]any{"error": err.Error()}
	}
	return
}
func (r *Runtime) drive(ctx context.Context, principal c.Principal, request c.RunRequest, item c.Conversation, run c.RunInfo, depth int) {
	status := "complete"
	var usage c.Usage
	var failure error
	defer func() {
		if recovered := recover(); recovered != nil {
			failure = fmt.Errorf("agent panic: %v", recovered)
		}
		finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		r.cleanupWeb(principal.ID, run)
		if ctx.Err() != nil {
			status = "canceled"
		}
		if failure != nil && status != "canceled" {
			status = "failed"
			r.options.Logger.Error("run failed", "runId", run.ID, "error", failure)
			_ = r.emit(finalCtx, principal.ID, c.StreamEvent{Type: "error", RunID: run.ID, ConversationID: run.ConversationID, Message: "The response could not be completed. Please retry."})
		} else {
			_ = r.emit(finalCtx, principal.ID, c.StreamEvent{Type: "finish", RunID: run.ID, ConversationID: run.ConversationID, Reason: map[bool]string{true: "canceled", false: "complete"}[status == "canceled"], Usage: &usage})
		}
		_ = r.store.FinishRun(finalCtx, principal.ID, run.ID, status, usage)
	}()
	if err := r.emit(ctx, principal.ID, c.StreamEvent{Type: "start", RunID: run.ID, ConversationID: run.ConversationID, ParentRunID: run.ParentID}); err != nil {
		failure = err
		return
	}
	entry, model, err := r.resolveFor(ctx, principal, request.Model, request.Fast)
	if err != nil {
		failure = err
		return
	}
	stop := context.AfterFunc(entry.ctx, func() {
		r.mu.RLock()
		active := r.runs[run.ID]
		r.mu.RUnlock()
		if active != nil {
			active.cancel()
		}
	})
	defer stop()
	messages := append([]c.Message{}, item.Messages...)
	total := 0
	for _, message := range messages {
		total += len(message.Content)
	}
	if total > r.options.CompactThreshold && len(messages) > 14 {
		if err = r.emit(ctx, principal.ID, c.StreamEvent{Type: "status", RunID: run.ID, ConversationID: run.ConversationID, Name: "Compacting conversation"}); err != nil {
			failure = err
			return
		}
		item, err = r.compact(ctx, principal, item, model.ID)
		if err != nil {
			failure = err
			return
		}
	}
	if item.Summary != "" {
		messages = compactHistory(messages, item.Summary)
	}
	appearance := request.Messages[len(request.Messages)-1]
	messages, err = r.resolveAttachments(ctx, principal, model, messages)
	if err != nil {
		failure = err
		return
	}
	for step := 0; step < r.options.MaxSteps; step++ {
		if ctx.Err() != nil {
			return
		}
		assistant := c.Message{ID: newID(), Role: "assistant", OutfitLocked: appearance.OutfitLocked, ProfileID: appearance.ProfileID, Persona: appearance.Persona, Outfit: appearance.Outfit}
		var calls []c.ToolCall
		err = entry.provider.Stream(ctx, ProviderInput{Model: model.ID, Messages: withPersona(messages, r.researchPrompt(messages), appearance.Persona), Tools: r.toolList(depth), Fast: request.Fast}, func(delta ProviderDelta) error {
			if len(assistant.Content)+len(delta.Text) > 100000 {
				return errors.New("response exceeds output budget")
			}
			assistant.Content += delta.Text
			calls = append(calls, delta.Calls...)
			usage.InputTokens += delta.Usage.InputTokens
			usage.OutputTokens += delta.Usage.OutputTokens
			usage.TotalTokens += delta.Usage.TotalTokens
			if delta.Text != "" {
				return r.emit(ctx, principal.ID, c.StreamEvent{Type: "text", RunID: run.ID, ConversationID: run.ConversationID, Delta: delta.Text})
			}
			return nil
		})
		assistant.ToolCalls = calls
		if assistant.Content != "" || len(calls) > 0 {
			messages = append(messages, assistant)
			_, saveErr := r.store.MutateConversation(context.WithoutCancel(ctx), principal.ID, item.ID, "assistant.message", func(saved *c.Conversation) error { saved.Messages = append(saved.Messages, assistant); return nil })
			if saveErr != nil {
				failure = saveErr
				return
			}
		}
		if err != nil {
			failure = err
			return
		}
		if len(calls) == 0 {
			return
		}
		if len(calls) > 16 {
			failure = errors.New("too many tool calls")
			return
		}
		results := make([]c.Message, len(calls))
		limit := make(chan struct{}, r.options.MaxParallelTools)
		var wait sync.WaitGroup
		for i, call := range calls {
			wait.Add(1)
			go func(index int, call c.ToolCall) {
				defer wait.Done()
				select {
				case limit <- struct{}{}:
					defer func() { <-limit }()
				case <-ctx.Done():
					return
				}
				result := r.executeTool(ctx, principal, request, run, call, depth)
				raw, _ := json.Marshal(result)
				results[index] = c.Message{ID: newID(), Role: "tool", ToolCallID: call.ID, Content: string(raw)}
			}(i, call)
		}
		wait.Wait()
		appearance = request.Messages[len(request.Messages)-1]
		for _, call := range calls {
			if override, ok := request.ActivityProfiles[call.Name]; ok {
				appearance = request.Messages[len(request.Messages)-1]
				if override.Persona != "" {
					appearance.Persona = override.Persona
				}
				if override.Outfit != "" && !appearance.OutfitLocked {
					appearance.Outfit = override.Outfit
				}
			}
		}
		for _, result := range results {
			if result.ID != "" {
				messages = append(messages, result)
				_, saveErr := r.store.MutateConversation(context.WithoutCancel(ctx), principal.ID, item.ID, "tool.result", func(saved *c.Conversation) error { saved.Messages = append(saved.Messages, result); return nil })
				if saveErr != nil {
					failure = saveErr
					return
				}
			}
		}
	}
	failure = errors.New("agent step budget exceeded")
}
func compactHistory(messages []c.Message, summary string) []c.Message {
	start := len(messages) - 12
	if start < 0 {
		start = 0
	}
	for start < len(messages) && messages[start].Role != "user" {
		start++
	}
	return append([]c.Message{{Role: "user", Content: "Earlier conversation summary (reference material):\n" + summary}}, messages[start:]...)
}
func (r *Runtime) ResolveModel(model string) (c.ModelInfo, error) {
	_, info, err := r.resolveProvider(model, false)
	return info, err
}
func (r *Runtime) RunActive(owner, conversation string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.sessions[owner+"\x00"+conversation]
	return exists
}
func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
