package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Authenticator interface {
	Authenticate(*http.Request) (c.Principal, error)
}
type AuthFunc func(*http.Request) (c.Principal, error)

func (f AuthFunc) Authenticate(r *http.Request) (c.Principal, error) { return f(r) }

type HTTPOptions struct {
	Origins       []string
	OriginAllowed func(*http.Request) bool
	Authenticate  Authenticator
	AuthRoutes    http.Handler
	OIDC          bool
}
type API struct {
	Runtime *Runtime
	Options HTTPOptions
	mux     *http.ServeMux
}

func NewAPI(runtime *Runtime, options HTTPOptions) *API {
	a := &API{Runtime: runtime, Options: options, mux: http.NewServeMux()}
	a.mux.HandleFunc("GET /api/tamizhi/capabilities", a.capabilities)
	a.mux.HandleFunc("GET /api/tamizhi/session", func(w http.ResponseWriter, r *http.Request) {
		p, _ := principal(r)
		respond(w, 200, c.SessionInfo{User: p})
	})
	a.mux.HandleFunc("GET /api/tamizhi/models", func(w http.ResponseWriter, r *http.Request) {
		p, _ := principal(r)
		models, err := runtime.ModelsFor(r.Context(), p)
		if err != nil {
			apiError(w, err)
			return
		}
		respond(w, 200, models)
	})
	a.mux.HandleFunc("GET /api/tamizhi/plugins", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, runtime.Plugins()) })
	a.mux.HandleFunc("PATCH /api/tamizhi/plugins/{name}", a.plugin)
	a.mux.HandleFunc("POST /api/tamizhi/providers/gemini/oauth", a.startGeminiOAuth)
	a.mux.HandleFunc("GET /api/tamizhi/providers", a.providerSettings)
	a.mux.HandleFunc("PUT /api/tamizhi/providers/{id}", a.saveProviderKey)
	a.mux.HandleFunc("PUT /api/tamizhi/providers/{id}/search", a.providerSearch)
	a.mux.HandleFunc("DELETE /api/tamizhi/providers/{id}", a.deleteProviderKey)
	a.mux.HandleFunc("POST /api/tamizhi/chat", a.chat)
	a.mux.HandleFunc("GET /api/tamizhi/runs/{id}", a.run)
	a.mux.HandleFunc("GET /api/tamizhi/runs/{id}/events", a.events)
	a.mux.HandleFunc("POST /api/tamizhi/runs/{id}/cancel", a.cancel)
	a.mux.HandleFunc("POST /api/tamizhi/approvals", a.approve)
	a.mux.HandleFunc("GET /api/tamizhi/conversations", a.conversations)
	a.mux.HandleFunc("GET /api/tamizhi/conversations/{id}", a.conversation)
	a.mux.HandleFunc("PATCH /api/tamizhi/conversations/{id}", a.rename)
	a.mux.HandleFunc("DELETE /api/tamizhi/conversations/{id}", a.deleteConversation)
	a.mux.HandleFunc("POST /api/tamizhi/conversations/{id}/branches", a.branch)
	a.mux.HandleFunc("PUT /api/tamizhi/conversations/{id}/messages/{message}/feedback", a.feedback)
	a.mux.HandleFunc("PUT /api/tamizhi/conversations/{id}/messages/{message}/tasks", a.tasks)
	a.mux.HandleFunc("POST /api/tamizhi/conversations/{id}/checkpoints", a.checkpoint)
	a.mux.HandleFunc("GET /api/tamizhi/conversations/{id}/checkpoints", a.checkpoints)
	a.mux.HandleFunc("POST /api/tamizhi/conversations/{id}/checkpoints/{checkpoint}/restore", a.restore)
	a.mux.HandleFunc("POST /api/tamizhi/conversations/{id}/compact", a.compact)
	a.mux.HandleFunc("POST /api/tamizhi/attachments", a.upload)
	a.mux.HandleFunc("GET /api/tamizhi/memory", a.memories)
	a.mux.HandleFunc("POST /api/tamizhi/memory", a.memory)
	a.mux.HandleFunc("DELETE /api/tamizhi/memory/{id}", a.deleteMemory)
	a.mux.HandleFunc("POST /api/tamizhi/audio/transcriptions", a.transcribe)
	a.mux.HandleFunc("POST /api/tamizhi/audio/speech", a.speech)
	return a
}

type principalKey struct{}

func principal(r *http.Request) (c.Principal, bool) {
	p, ok := r.Context().Value(principalKey{}).(c.Principal)
	return p, ok
}
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	origin := r.Header.Get("Origin")
	if origin != "" {
		allowed := false
		for _, candidate := range a.Options.Origins {
			if origin == candidate {
				allowed = true
				break
			}
		}
		if a.Options.OriginAllowed != nil {
			allowed = a.Options.OriginAllowed(r)
		}
		if !allowed {
			apiError(w, ErrForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Expose-Headers", "X-Tamizhi-Run-ID")
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,Last-Event-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	if r.URL.Path == "/healthz" {
		respond(w, 200, map[string]string{"status": "ok"})
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/api/tamizhi/providers/gemini/oauth/callback" {
		a.finishGeminiOAuth(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/tamizhi/auth/") && a.Options.AuthRoutes != nil {
		a.Options.AuthRoutes.ServeHTTP(w, r)
		return
	}
	if a.Options.Authenticate == nil {
		apiError(w, ErrForbidden)
		return
	}
	p, err := a.Options.Authenticate.Authenticate(r)
	if err != nil || p.ID == "" {
		respond(w, 401, c.APIError{Code: "unauthorized", Message: "Sign in to use Tamizhi"})
		return
	}
	a.mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)))
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, err error) {
	status := 400
	code := "invalid_request"
	message := "The request could not be processed"
	switch {
	case errors.Is(err, ErrModelUnavailable):
		status = http.StatusServiceUnavailable
		code = "model_unavailable"
		message = "No AI model is available. Open AI settings to connect a provider, then choose a model."
	case errors.Is(err, ErrNotFound):
		status = 404
		code = "not_found"
		message = "Resource not found"
	case errors.Is(err, ErrForbidden):
		status = 403
		code = "forbidden"
		message = "Access denied"
	case errors.Is(err, ErrConflict):
		status = 409
		code = "conflict"
		message = "The conversation changed or a run is already active"
	}
	respond(w, status, c.APIError{Code: code, Message: message})
}
func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		apiError(w, err)
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		apiError(w, errors.New("unexpected trailing JSON"))
		return false
	}
	return true
}
func (a *API) capabilities(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	result := a.Runtime.Capabilities(a.Options.OIDC)
	if models, err := a.Runtime.ModelsFor(r.Context(), p); err == nil {
		for _, model := range models {
			result.Images = result.Images || contains(model.Modalities, "image")
		}
	}
	if audio := a.Runtime.audioFor(r.Context(), p); audio != nil {
		if provider, ok := audio.(*OpenAIProvider); ok {
			result.Transcription = provider.Options.TranscriptionModel != ""
			result.Speech = provider.Options.SpeechModel != ""
		}
	}
	respond(w, 200, result)
}
func (a *API) plugin(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	if !p.Admin() {
		apiError(w, ErrForbidden)
		return
	}
	var body c.PluginUpdate
	if !decode(w, r, &body) {
		return
	}
	if err := a.Runtime.ConfigurePlugin(r.Context(), r.PathValue("name"), body.Enabled); err != nil {
		if errors.Is(err, errPluginPersistence) {
			respond(w, http.StatusServiceUnavailable, c.APIError{Code: "setting_unconfirmed", Message: "Unable to confirm saved plugin settings. Refresh status before retrying."})
			return
		}
		apiError(w, err)
		return
	}
	respond(w, 200, a.Runtime.Plugins())
}
func (a *API) chat(w http.ResponseWriter, r *http.Request) {
	var request c.RunRequest
	if !decode(w, r, &request) {
		return
	}
	p, _ := principal(r)
	run, err := a.Runtime.StartRun(p, request)
	if err != nil {
		apiError(w, err)
		return
	}
	w.Header().Set("X-Tamizhi-Run-ID", run.ID)
	a.follow(w, r, p, run.ID, 0)
}
func (a *API) run(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	run, _, err := a.Runtime.store.Run(r.Context(), p.ID, r.PathValue("id"))
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 200, run)
}
func (a *API) events(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	cursor := r.URL.Query().Get("after")
	if cursor == "" {
		cursor = r.Header.Get("Last-Event-ID")
	}
	after := int64(0)
	var err error
	if cursor != "" {
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after < 0 {
			apiError(w, errors.New("invalid cursor"))
			return
		}
	}
	a.follow(w, r, p, r.PathValue("id"), after)
}
func (a *API) follow(w http.ResponseWriter, r *http.Request, p c.Principal, id string, after int64) {
	if _, _, err := a.Runtime.store.Run(r.Context(), p.ID, id); err != nil {
		apiError(w, err)
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		respond(w, 500, c.APIError{Code: "stream_unavailable", Message: "Streaming unavailable"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		a.Runtime.mu.RLock()
		active := a.Runtime.runs[id]
		var notify <-chan struct{}
		if active != nil {
			notify = active.notify
		}
		a.Runtime.mu.RUnlock()
		events, err := a.Runtime.store.RunEvents(r.Context(), p.ID, id, after)
		if err != nil {
			return
		}
		for _, event := range events {
			raw, err := json.Marshal(event)
			if err != nil {
				return
			}
			_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, err = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.Sequence, raw); err != nil {
				return
			}
			if err = controller.Flush(); err != nil {
				return
			}
			after = event.Sequence
		}
		if len(events) == 256 {
			continue
		}
		run, _, err := a.Runtime.store.Run(r.Context(), p.ID, id)
		if err != nil {
			return
		}
		if run.Status != "running" && run.Status != "waiting" && run.Status != "queued" {
			if after < run.LastSequence {
				continue
			}
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-notify:
		case <-ticker.C:
			_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, err = io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			if err = controller.Flush(); err != nil {
				return
			}
		}
	}
}
func (a *API) cancel(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	if err := a.Runtime.Cancel(p, r.PathValue("id")); err != nil {
		apiError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (a *API) approve(w http.ResponseWriter, r *http.Request) {
	var decision c.ApprovalDecision
	if !decode(w, r, &decision) {
		return
	}
	p, _ := principal(r)
	if !a.Runtime.Approve(p, decision) {
		apiError(w, ErrNotFound)
		return
	}
	w.WriteHeader(204)
}
func (a *API) conversations(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	items, err := a.Runtime.store.Conversations(r.Context(), p.ID, r.URL.Query().Get("q"), r.URL.Query().Get("cursor"), 30)
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 200, items)
}
func (a *API) conversation(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	item, err := a.Runtime.store.Conversation(r.Context(), p.ID, r.PathValue("id"))
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 200, item)
}
func (a *API) mutate(w http.ResponseWriter, r *http.Request, kind string, fn func(*c.Conversation) error) {
	p, _ := principal(r)
	if a.Runtime.RunActive(p.ID, r.PathValue("id")) {
		apiError(w, ErrConflict)
		return
	}
	item, err := a.Runtime.store.MutateConversation(r.Context(), p.ID, r.PathValue("id"), kind, fn)
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 200, item)
}
func (a *API) rename(w http.ResponseWriter, r *http.Request) {
	var body c.RenameRequest
	if !decode(w, r, &body) {
		return
	}
	body.Title = strings.TrimSpace(body.Title)
	if body.Title == "" || len(body.Title) > 128 {
		apiError(w, errors.New("invalid title"))
		return
	}
	a.mutate(w, r, "conversation.renamed", func(item *c.Conversation) error { item.Title = body.Title; return nil })
}
func (a *API) deleteConversation(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	if a.Runtime.RunActive(p.ID, r.PathValue("id")) {
		apiError(w, ErrConflict)
		return
	}
	if err := a.Runtime.store.DeleteConversation(r.Context(), p.ID, r.PathValue("id")); err != nil {
		apiError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (a *API) branch(w http.ResponseWriter, r *http.Request) {
	var body c.BranchRequest
	if !decode(w, r, &body) {
		return
	}
	p, _ := principal(r)
	item, err := a.Runtime.store.Branch(r.Context(), p.ID, r.PathValue("id"), body)
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 201, item)
}
func (a *API) feedback(w http.ResponseWriter, r *http.Request) {
	var body c.FeedbackRequest
	if !decode(w, r, &body) {
		return
	}
	if body.Rating != "" && body.Rating != "like" && body.Rating != "dislike" {
		apiError(w, errors.New("invalid rating"))
		return
	}
	a.mutate(w, r, "message.feedback", func(item *c.Conversation) error {
		for i := range item.Messages {
			if item.Messages[i].ID == r.PathValue("message") && item.Messages[i].Role == "assistant" {
				item.Messages[i].Rating = body.Rating
				return nil
			}
		}
		return ErrNotFound
	})
}
func (a *API) tasks(w http.ResponseWriter, r *http.Request) {
	var body c.TasksRequest
	if !decode(w, r, &body) {
		return
	}
	if len(body.Tasks) > 100 {
		apiError(w, errors.New("too many tasks"))
		return
	}
	for _, task := range body.Tasks {
		if len(task.Label) > 1000 {
			apiError(w, errors.New("task too long"))
			return
		}
	}
	a.mutate(w, r, "message.tasks", func(item *c.Conversation) error {
		for i := range item.Messages {
			if item.Messages[i].ID == r.PathValue("message") {
				item.Messages[i].Tasks = body.Tasks
				return nil
			}
		}
		return ErrNotFound
	})
}
func (a *API) checkpoint(w http.ResponseWriter, r *http.Request) {
	var body c.CheckpointRequest
	if !decode(w, r, &body) {
		return
	}
	p, _ := principal(r)
	item, err := a.Runtime.store.Checkpoint(r.Context(), p.ID, r.PathValue("id"), body.Label)
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 201, item)
}
func (a *API) checkpoints(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	items, err := a.Runtime.store.Checkpoints(r.Context(), p.ID, r.PathValue("id"))
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 200, items)
}
func (a *API) restore(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	if a.Runtime.RunActive(p.ID, r.PathValue("id")) {
		apiError(w, ErrConflict)
		return
	}
	item, err := a.Runtime.store.RestoreCheckpoint(r.Context(), p.ID, r.PathValue("id"), r.PathValue("checkpoint"))
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 200, item)
}
func (a *API) compact(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	item, err := a.Runtime.Compact(r.Context(), p, r.PathValue("id"), "")
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 200, item)
}
func (a *API) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 11<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		apiError(w, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		apiError(w, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 10<<20+1))
	if err != nil {
		apiError(w, err)
		return
	}
	p, _ := principal(r)
	item, err := a.Runtime.Upload(r.Context(), p, header.Filename, data)
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 201, item)
}
func (a *API) memories(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	items, err := a.Runtime.store.Memories(r.Context(), p.ID, r.URL.Query().Get("q"))
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 200, items)
}
func (a *API) memory(w http.ResponseWriter, r *http.Request) {
	var body c.MemoryRequest
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Text) == "" || len(body.Text) > 10000 || len(body.Tags) > 20 {
		apiError(w, errors.New("invalid memory"))
		return
	}
	p, _ := principal(r)
	item, err := a.Runtime.store.AddMemory(r.Context(), p.ID, body)
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 201, item)
}
func (a *API) deleteMemory(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	if err := a.Runtime.store.DeleteMemory(r.Context(), p.ID, r.PathValue("id")); err != nil {
		apiError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (a *API) transcribe(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	provider := a.Runtime.audioFor(r.Context(), p)
	if provider == nil {
		apiError(w, ErrNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 11<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		apiError(w, err)
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		apiError(w, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 10<<20+1))
	if err != nil || len(data) > 10<<20 {
		apiError(w, errors.New("audio too large"))
		return
	}
	text, err := provider.Transcribe(r.Context(), header.Filename, header.Header.Get("Content-Type"), data)
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 200, c.Transcript{Text: text})
}
func (a *API) speech(w http.ResponseWriter, r *http.Request) {
	var body c.SpeechRequest
	if !decode(w, r, &body) {
		return
	}
	if len(body.Text) > 10000 || strings.TrimSpace(body.Text) == "" {
		apiError(w, errors.New("invalid speech text"))
		return
	}
	p, _ := principal(r)
	provider := a.Runtime.audioFor(r.Context(), p)
	if provider == nil {
		apiError(w, ErrNotFound)
		return
	}
	data, mime, err := provider.Speech(r.Context(), body)
	if err != nil {
		apiError(w, err)
		return
	}
	w.Header().Set("Content-Type", mime)
	_, _ = w.Write(data)
}

func (a *API) providerSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	items, err := a.Runtime.ProviderSettings(r.Context(), p)
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 200, items)
}
func (a *API) saveProviderKey(w http.ResponseWriter, r *http.Request) {
	var body c.ProviderKeyRequest
	if !decode(w, r, &body) {
		return
	}
	p, _ := principal(r)
	item, err := a.Runtime.SaveProviderKey(r.Context(), p, r.PathValue("id"), body.APIKey)
	if err != nil {
		respond(w, 400, c.APIError{Code: "provider_connection_failed", Message: err.Error()})
		return
	}
	respond(w, 200, item)
}
func (a *API) deleteProviderKey(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	if err := a.Runtime.DeleteProviderKey(r.Context(), p, r.PathValue("id")); err != nil {
		apiError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (a *API) providerSearch(w http.ResponseWriter, r *http.Request) {
	var body c.ProviderSearchRequest
	if !decode(w, r, &body) {
		return
	}
	p, _ := principal(r)
	settings, err := a.Runtime.SetProviderSearch(r.Context(), p, r.PathValue("id"), body.Enabled)
	if err != nil {
		apiError(w, err)
		return
	}
	respond(w, 200, settings)
}
