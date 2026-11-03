package contracts

import (
	"encoding/json"
	"errors"
	"strings"
)

type PermissionMode string

const (
	PermissionAsk  PermissionMode = "ask"
	PermissionSafe PermissionMode = "safe"
	PermissionFull PermissionMode = "full"
)

type Task struct {
	Label string `json:"label"`
	Done  bool   `json:"done"`
}
type ToolCall struct {
	Signature string `json:"signature,omitempty"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type Attachment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
	Text     string `json:"text,omitempty"`
}
type Message struct {
	ID           string       `json:"id,omitempty"`
	Role         string       `json:"role"`
	Content      string       `json:"content"`
	Attachments  []Attachment `json:"attachments,omitempty"`
	ToolCalls    []ToolCall   `json:"toolCalls,omitempty"`
	ToolCallID   string       `json:"toolCallId,omitempty"`
	Rating       string       `json:"rating,omitempty"`
	Tasks        []Task       `json:"tasks,omitempty"`
	OutfitLocked bool         `json:"outfitLocked,omitempty"`
	ProfileID    string       `json:"profileId,omitempty"`
	Persona      string       `json:"persona,omitempty"`
	Outfit       string       `json:"outfit,omitempty"`
}
type ActivityAppearance struct {
	Persona string `json:"persona,omitempty"`
	Outfit  string `json:"outfit,omitempty"`
}

type RunRequest struct {
	ActivityProfiles map[string]ActivityAppearance `json:"activityProfiles,omitempty"`
	ConversationID   string                        `json:"conversationId"`
	Messages         []Message                     `json:"messages"`
	Model            string                        `json:"model,omitempty"`
	Fast             bool                          `json:"fast"`
	Permission       PermissionMode                `json:"permission"`
	Title            string                        `json:"title,omitempty"`
	ExpectedVersion  *int64                        `json:"expectedVersion,omitempty"`
}
type ApprovalDecision struct {
	RunID    string `json:"runId"`
	CallID   string `json:"callId"`
	Approved bool   `json:"approved"`
}
type Usage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	TotalTokens  int64 `json:"totalTokens"`
}
type StreamEvent struct {
	Type           string `json:"type"`
	RunID          string `json:"runId"`
	ConversationID string `json:"conversationId"`
	Sequence       int64  `json:"sequence"`
	Delta          string `json:"delta,omitempty"`
	CallID         string `json:"callId,omitempty"`
	Name           string `json:"name,omitempty"`
	Input          any    `json:"input,omitempty"`
	Output         any    `json:"output,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Message        string `json:"message,omitempty"`
	Status         string `json:"status,omitempty"`
	ParentRunID    string `json:"parentRunId,omitempty"`
	Usage          *Usage `json:"usage,omitempty"`
}
type ModelInfo struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Provider      string   `json:"provider"`
	Modalities    []string `json:"modalities,omitempty"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	Fast          bool     `json:"fast,omitempty"`
}
type Conversation struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Messages   []Message `json:"messages"`
	UpdatedAt  int64     `json:"updatedAt"`
	Version    int64     `json:"version"`
	ParentID   string    `json:"parentId,omitempty"`
	BranchFrom string    `json:"branchFrom,omitempty"`
	Summary    string    `json:"summary,omitempty"`
}
type ConversationPage struct {
	Items      []Conversation `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
}
type RenameRequest struct {
	Title string `json:"title"`
}
type FeedbackRequest struct {
	Rating string `json:"rating"`
}
type TasksRequest struct {
	Tasks []Task `json:"tasks"`
}
type BranchRequest struct {
	MessageID string `json:"messageId"`
	Title     string `json:"title,omitempty"`
}
type Checkpoint struct {
	Summary        string    `json:"summary,omitempty"`
	ID             string    `json:"id"`
	ConversationID string    `json:"conversationId"`
	Label          string    `json:"label"`
	Messages       []Message `json:"messages"`
	CreatedAt      int64     `json:"createdAt"`
}
type CheckpointRequest struct {
	Label string `json:"label"`
}
type RunInfo struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	Status         string `json:"status"`
	LastSequence   int64  `json:"lastSequence"`
	ParentID       string `json:"parentId,omitempty"`
	Usage          Usage  `json:"usage"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}
type PluginInfo struct {
	Required     bool     `json:"required"`
	Error        string   `json:"error,omitempty"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Enabled      bool     `json:"enabled"`
	Status       string   `json:"status"`
	Dependencies []string `json:"dependencies"`
}
type PluginUpdate struct {
	Enabled bool `json:"enabled"`
}
type Principal struct {
	ID    string   `json:"id"`
	Name  string   `json:"name,omitempty"`
	Roles []string `json:"roles,omitempty"`
}
type LoginMethod struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type AuthSession struct {
	CanLogout    bool          `json:"canLogout"`
	Methods      []LoginMethod `json:"methods"`
	User         *Principal    `json:"user,omitempty"`
	LoginEnabled bool          `json:"loginEnabled"`
}

type SessionInfo struct {
	User      Principal `json:"user"`
	LoginURL  string    `json:"loginUrl,omitempty"`
	ExpiresAt int64     `json:"expiresAt,omitempty"`
}
type Capabilities struct {
	Version       string `json:"version"`
	Images        bool   `json:"images"`
	PDF           bool   `json:"pdf"`
	Transcription bool   `json:"transcription"`
	Speech        bool   `json:"speech"`
	OIDC          bool   `json:"oidc"`
	Sessions      bool   `json:"sessions"`
	Replay        bool   `json:"replay"`
	Plugins       bool   `json:"plugins"`
	Memory        bool   `json:"memory"`
	Checkpoints   bool   `json:"checkpoints"`
}
type Memory struct {
	ID        string   `json:"id"`
	Text      string   `json:"text"`
	Tags      []string `json:"tags,omitempty"`
	Score     float64  `json:"score,omitempty"`
	CreatedAt int64    `json:"createdAt"`
}
type MemoryRequest struct {
	Text string   `json:"text"`
	Tags []string `json:"tags,omitempty"`
}
type Transcript struct {
	Text string `json:"text"`
}
type SpeechRequest struct {
	Text  string `json:"text"`
	Voice string `json:"voice,omitempty"`
}
type ClientAction struct {
	Name  string `json:"name"`
	Path  string `json:"path,omitempty"`
	Query string `json:"query,omitempty"`
}
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r *RunRequest) Validate() error {
	if len(r.ConversationID) == 0 || len(r.ConversationID) > 128 || len(r.Messages) == 0 || len(r.Messages) > 200 {
		return errors.New("invalid conversation or message count")
	}
	if r.Permission == "" {
		r.Permission = PermissionAsk
	}
	if r.Permission != PermissionAsk && r.Permission != PermissionSafe && r.Permission != PermissionFull {
		return errors.New("invalid permission mode")
	}
	if len(r.Model) > 256 || len(r.Title) > 128 {
		return errors.New("model or title is too long")
	}
	if len(r.ActivityProfiles) > 64 {
		return errors.New("too many activity profiles")
	}
	for name, appearance := range r.ActivityProfiles {
		if len(name) == 0 || len(name) > 256 || len(appearance.Persona) > 32 || len(appearance.Outfit) > 32 {
			return errors.New("invalid activity profile")
		}
	}
	for i := range r.Messages {
		m := &r.Messages[i]
		if m.Role != "user" && m.Role != "assistant" {
			return errors.New("client history may only contain user or assistant messages")
		}
		if len(m.Content) > 100000 || len(m.Attachments) > 8 || len(m.ID) > 128 || len(m.ProfileID) > 128 || len(m.Persona) > 32 || len(m.Outfit) > 32 {
			return errors.New("message exceeds limits")
		}
		if len(m.ToolCalls) != 0 || m.ToolCallID != "" {
			return errors.New("client cannot supply tool executions")
		}
	}
	if r.Messages[len(r.Messages)-1].Role != "user" || strings.TrimSpace(r.Messages[len(r.Messages)-1].Content) == "" {
		return errors.New("a user prompt is required")
	}
	return nil
}
func JSON(value any) json.RawMessage { b, _ := json.Marshal(value); return b }
func (p Principal) Admin() bool {
	for _, role := range p.Roles {
		if role == "admin" {
			return true
		}
	}
	return false
}

// ProviderSettings never includes credential material in an API response.
type ProviderSettings struct {
	OAuthAvailable     bool        `json:"oauthAvailable"`
	AuthMethod         string      `json:"authMethod,omitempty"`
	ProjectID          string      `json:"projectId,omitempty"`
	ID                 string      `json:"id"`
	Name               string      `json:"name"`
	Connected          bool        `json:"connected"`
	Models             []ModelInfo `json:"models"`
	TranscriptionModel string      `json:"transcriptionModel,omitempty"`
	SpeechModel        string      `json:"speechModel,omitempty"`
}
type ProviderKeyRequest struct {
	APIKey string `json:"apiKey"`
}

type ProviderOAuthRequest struct {
	ProjectID string `json:"projectId"`
	ReturnTo  string `json:"returnTo"`
}
type ProviderAuthorization struct {
	URL string `json:"url"`
}
