package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

type OpenAIProviderOptions struct {
	ID                 string                                `json:"id"`
	BaseURL            string                                `json:"baseUrl"`
	APIKeyEnv          string                                `json:"apiKeyEnv"`
	Models             []c.ModelInfo                         `json:"models"`
	TranscriptionModel string                                `json:"transcriptionModel,omitempty"`
	SpeechModel        string                                `json:"speechModel,omitempty"`
	HTTPClient         *http.Client                          `json:"-"`
	Store              *Store                                `json:"-"`
	ResolveSecret      func(context.Context) (string, error) `json:"-"`
}
type OpenAIProvider struct{ Options OpenAIProviderOptions }

func (p *OpenAIProvider) Models() []c.ModelInfo { return append([]c.ModelInfo{}, p.Options.Models...) }
func (p *OpenAIProvider) secret(ctx context.Context) (string, error) {
	if p.Options.ResolveSecret != nil {
		return p.Options.ResolveSecret(ctx)
	}
	key := os.Getenv(p.Options.APIKeyEnv)
	if key == "" {
		return "", errors.New("provider credentials are not configured")
	}
	return key, nil
}
func (p *OpenAIProvider) wireModel(id string) string {
	prefix := p.Options.ID + "/"
	return strings.TrimPrefix(id, prefix)
}
func (p *OpenAIProvider) Stream(ctx context.Context, input ProviderInput, emit func(ProviderDelta) error) error {
	key, err := p.secret(ctx)
	if err != nil {
		return err
	}
	options := []option.RequestOption{option.WithAPIKey(key), option.WithMaxRetries(2)}
	if p.Options.BaseURL != "" {
		options = append(options, option.WithBaseURL(p.Options.BaseURL))
	}
	if p.Options.HTTPClient != nil {
		options = append(options, option.WithHTTPClient(p.Options.HTTPClient))
	}
	client := openai.NewClient(options...)
	messages := make([]map[string]any, 0, len(input.Messages))
	for _, message := range input.Messages {
		item := map[string]any{"role": message.Role, "content": message.Content}
		if len(message.ToolCalls) > 0 {
			calls := []map[string]any{}
			for _, call := range message.ToolCalls {
				calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": call.Arguments}})
			}
			item["tool_calls"] = calls
		}
		if message.ToolCallID != "" {
			item["tool_call_id"] = message.ToolCallID
		}
		if len(message.Attachments) > 0 {
			parts := []map[string]any{{"type": "text", "text": message.Content}}
			for _, attachment := range message.Attachments {
				if strings.HasPrefix(attachment.MimeType, "image/") {
					if attachment.Text == "" {
						return errors.New("image payload was not resolved")
					}
					parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": attachment.Text}})
				} else if attachment.Text != "" {
					parts = append(parts, map[string]any{"type": "text", "text": "File: " + attachment.Name + "\n" + attachment.Text})
				}
			}
			item["content"] = parts
		}
		messages = append(messages, item)
	}
	tools := []map[string]any{}
	for _, tool := range input.Tools {
		var schema any
		if err = json.Unmarshal(tool.Schema, &schema); err != nil {
			return err
		}
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": schema}})
	}
	payload := map[string]any{"model": p.wireModel(input.Model), "messages": messages, "stream_options": map[string]any{"include_usage": true}}
	if len(tools) > 0 {
		payload["tools"] = tools
	}
	raw, _ := json.Marshal(payload)
	var params openai.ChatCompletionNewParams
	if err = json.Unmarshal(raw, &params); err != nil {
		return err
	}
	stream := client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()
	calls := map[int64]*c.ToolCall{}
	var usage c.Usage
	for stream.Next() {
		chunk := stream.Current()
		if chunk.Usage.TotalTokens > 0 {
			usage = c.Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens, TotalTokens: chunk.Usage.TotalTokens}
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.Delta.Content != "" {
				if err = emit(ProviderDelta{Text: choice.Delta.Content}); err != nil {
					return err
				}
			}
			for _, part := range choice.Delta.ToolCalls {
				call := calls[part.Index]
				if call == nil {
					call = &c.ToolCall{}
					calls[part.Index] = call
				}
				if part.ID != "" {
					call.ID = part.ID
				}
				if part.Function.Name != "" {
					call.Name = part.Function.Name
				}
				call.Arguments += part.Function.Arguments
				if len(call.Arguments) > 100000 {
					return errors.New("tool arguments exceed limit")
				}
			}
		}
	}
	if err = stream.Err(); err != nil {
		return err
	}
	ordered := []c.ToolCall{}
	for index := int64(0); index < int64(len(calls)); index++ {
		if call := calls[index]; call != nil {
			if call.ID == "" {
				call.ID = newID()
			}
			ordered = append(ordered, *call)
		}
	}
	return emit(ProviderDelta{Calls: ordered, Usage: usage})
}
func (p *OpenAIProvider) request(ctx context.Context, path, contentType string, body io.Reader) (*http.Response, error) {
	key, err := p.secret(ctx)
	if err != nil {
		return nil, err
	}
	base := p.Options.BaseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", contentType)
	client := p.Options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	} else if client.Timeout == 0 {
		bounded := *client
		bounded.Timeout = 120 * time.Second
		client = &bounded
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("audio provider returned status %d", response.StatusCode)
	}
	return response, nil
}
func (p *OpenAIProvider) Transcribe(ctx context.Context, name, mime string, data []byte) (string, error) {
	if p.Options.TranscriptionModel == "" {
		return "", errors.New("transcription is not configured")
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	if err := writer.WriteField("model", p.Options.TranscriptionModel); err != nil {
		return "", err
	}
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		return "", err
	}
	if _, err = part.Write(data); err != nil {
		return "", err
	}
	if err = writer.Close(); err != nil {
		return "", err
	}
	response, err := p.request(ctx, "/audio/transcriptions", writer.FormDataContentType(), &buffer)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var result c.Transcript
	if err = json.NewDecoder(io.LimitReader(response.Body, 1000000)).Decode(&result); err != nil {
		return "", err
	}
	return result.Text, nil
}
func (p *OpenAIProvider) Speech(ctx context.Context, request c.SpeechRequest) ([]byte, string, error) {
	if p.Options.SpeechModel == "" {
		return nil, "", errors.New("speech is not configured")
	}
	voice := request.Voice
	if voice == "" {
		voice = "alloy"
	}
	raw, _ := json.Marshal(map[string]any{"model": p.Options.SpeechModel, "input": request.Text, "voice": voice})
	response, err := p.request(ctx, "/audio/speech", "application/json", bytes.NewReader(raw))
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 20000001))
	if len(data) > 20000000 {
		return nil, "", errors.New("speech output is too large")
	}
	mime := response.Header.Get("Content-Type")
	if !strings.HasPrefix(mime, "audio/") {
		mime = "audio/mpeg"
	}
	return data, mime, err
}
func imageDataURL(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}
