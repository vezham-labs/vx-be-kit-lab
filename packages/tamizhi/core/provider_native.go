package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"sort"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"google.golang.org/genai"
)

type AnthropicProvider struct {
	Definition BYOKDefinition
	Key        string
	Catalog    []c.ModelInfo
	HTTPClient *http.Client
}

func (p *AnthropicProvider) Models() []c.ModelInfo { return append([]c.ModelInfo{}, p.Catalog...) }
func (p *AnthropicProvider) Stream(ctx context.Context, input ProviderInput, emit func(ProviderDelta) error) error {
	opts := []anthropicoption.RequestOption{anthropicoption.WithAPIKey(p.Key), anthropicoption.WithBaseURL(strings.TrimSuffix(p.Definition.BaseURL, "/v1")), anthropicoption.WithMaxRetries(2)}
	if p.HTTPClient != nil {
		opts = append(opts, anthropicoption.WithHTTPClient(p.HTTPClient))
	}
	client := anthropic.NewClient(opts...)
	messages := []map[string]any{}
	system := []map[string]any{}
	for _, message := range input.Messages {
		if message.Role == "system" {
			system = append(system, map[string]any{"type": "text", "text": message.Content})
			continue
		}
		role := message.Role
		blocks := []any{}
		if role == "tool" {
			role = "user"
			blocks = append(blocks, map[string]any{"type": "tool_result", "tool_use_id": message.ToolCallID, "content": message.Content})
		} else {
			if message.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": message.Content})
			}
			for _, attachment := range message.Attachments {
				if strings.HasPrefix(attachment.MimeType, "image/") {
					_, encoded, ok := strings.Cut(attachment.Text, ",")
					if !ok {
						return errors.New("unresolved image")
					}
					blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": attachment.MimeType, "data": encoded}})
				} else if attachment.Text != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": "File: " + attachment.Name + "\n" + attachment.Text})
				}
			}
			for _, call := range message.ToolCalls {
				var args any
				if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
					return err
				}
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": args})
			}
		}
		if len(blocks) == 0 {
			continue
		}
		if len(messages) > 0 && messages[len(messages)-1]["role"] == role {
			last := messages[len(messages)-1]
			last["content"] = append(last["content"].([]any), blocks...)
		} else {
			messages = append(messages, map[string]any{"role": role, "content": blocks})
		}
	}
	payload := map[string]any{"model": strings.TrimPrefix(input.Model, p.Definition.ID+"/"), "max_tokens": 8192, "messages": messages}
	if len(system) > 0 {
		payload["system"] = system
	}
	if len(input.Tools) > 0 {
		tools := []any{}
		for _, tool := range input.Tools {
			var schema any
			if err := json.Unmarshal(tool.Schema, &schema); err != nil {
				return err
			}
			tools = append(tools, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": schema})
		}
		payload["tools"] = tools
	}
	raw, _ := json.Marshal(payload)
	var params anthropic.MessageNewParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return err
	}
	stream := client.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	calls := map[int64]*c.ToolCall{}
	var usage c.Usage
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "message_start":
			usage.InputTokens = event.Message.Usage.InputTokens
		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				calls[event.Index] = &c.ToolCall{ID: event.ContentBlock.ID, Name: event.ContentBlock.Name}
			}
			if event.ContentBlock.Type == "text" && event.ContentBlock.Text != "" {
				if err := emit(ProviderDelta{Text: event.ContentBlock.Text}); err != nil {
					return err
				}
			}
		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				if err := emit(ProviderDelta{Text: event.Delta.Text}); err != nil {
					return err
				}
			}
			if event.Delta.Type == "input_json_delta" {
				if call := calls[event.Index]; call != nil {
					call.Arguments += event.Delta.PartialJSON
					if len(call.Arguments) > 100000 {
						return errors.New("tool arguments exceed budget")
					}
				}
			}
		case "message_delta":
			usage.OutputTokens = event.Usage.OutputTokens
		}
	}
	if err := stream.Err(); err != nil {
		return err
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	indices := []int{}
	for index := range calls {
		indices = append(indices, int(index))
	}
	sort.Ints(indices)
	ordered := []c.ToolCall{}
	for _, index := range indices {
		call := *calls[int64(index)]
		if call.Arguments == "" {
			call.Arguments = "{}"
		}
		ordered = append(ordered, call)
	}
	return emit(ProviderDelta{Calls: ordered, Usage: usage})
}

type GeminiProvider struct {
	OAuth      bool
	Definition BYOKDefinition
	Key        string
	Catalog    []c.ModelInfo
	HTTPClient *http.Client
}

func (p *GeminiProvider) Models() []c.ModelInfo { return append([]c.ModelInfo{}, p.Catalog...) }
func (p *GeminiProvider) Stream(ctx context.Context, input ProviderInput, emit func(ProviderDelta) error) error {
	config := &genai.GenerateContentConfig{MaxOutputTokens: 8192}
	contents := []*genai.Content{}
	callNames := map[string]string{}
	for _, message := range input.Messages {
		if message.Role == "system" {
			if config.SystemInstruction == nil {
				config.SystemInstruction = &genai.Content{Parts: []*genai.Part{}}
			}
			config.SystemInstruction.Parts = append(config.SystemInstruction.Parts, &genai.Part{Text: message.Content})
			continue
		}
		role := "user"
		if message.Role == "assistant" {
			role = "model"
		}
		parts := []*genai.Part{}
		if message.Role == "tool" {
			var result map[string]any
			if json.Unmarshal([]byte(message.Content), &result) != nil {
				result = map[string]any{"result": message.Content}
			}
			parts = append(parts, &genai.Part{FunctionResponse: &genai.FunctionResponse{ID: message.ToolCallID, Name: callNames[message.ToolCallID], Response: result}})
		} else {
			if message.Content != "" {
				parts = append(parts, &genai.Part{Text: message.Content})
			}
			for _, attachment := range message.Attachments {
				if strings.HasPrefix(attachment.MimeType, "image/") {
					_, encoded, ok := strings.Cut(attachment.Text, ",")
					if !ok {
						return errors.New("unresolved image")
					}
					data, err := base64.StdEncoding.DecodeString(encoded)
					if err != nil {
						return err
					}
					parts = append(parts, &genai.Part{InlineData: &genai.Blob{MIMEType: attachment.MimeType, Data: data}})
				} else if attachment.Text != "" {
					parts = append(parts, &genai.Part{Text: "File: " + attachment.Name + "\n" + attachment.Text})
				}
			}
			for _, call := range message.ToolCalls {
				var args map[string]any
				if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
					return err
				}
				part := &genai.Part{FunctionCall: &genai.FunctionCall{ID: call.ID, Name: call.Name, Args: args}}
				if call.Signature != "" {
					part.ThoughtSignature, _ = base64.StdEncoding.DecodeString(call.Signature)
				}
				parts = append(parts, part)
				callNames[call.ID] = call.Name
			}
		}
		if len(parts) > 0 {
			if len(contents) > 0 && contents[len(contents)-1].Role == role {
				contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, parts...)
			} else {
				contents = append(contents, &genai.Content{Role: role, Parts: parts})
			}
		}
	}
	if len(input.Tools) > 0 {
		declarations := []*genai.FunctionDeclaration{}
		for _, tool := range input.Tools {
			var schema any
			if err := json.Unmarshal(tool.Schema, &schema); err != nil {
				return err
			}
			declarations = append(declarations, &genai.FunctionDeclaration{Name: tool.Name, Description: tool.Description, ParametersJsonSchema: schema})
		}
		config.Tools = []*genai.Tool{{FunctionDeclarations: declarations}}
	}
	calls := []c.ToolCall{}
	var usage c.Usage
	model := strings.TrimPrefix(input.Model, p.Definition.ID+"/")
	var chunks iter.Seq2[*genai.GenerateContentResponse, error]
	if p.OAuth {
		chunks = p.oauthStream(ctx, model, contents, config)
	} else {
		base := strings.TrimSuffix(p.Definition.BaseURL, "/v1beta")
		client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: p.Key, Backend: genai.BackendGeminiAPI, HTTPClient: p.HTTPClient, HTTPOptions: genai.HTTPOptions{BaseURL: base, APIVersion: "v1beta"}})
		if err != nil {
			return err
		}
		chunks = client.Models.GenerateContentStream(ctx, model, contents, config)
	}
	for chunk, err := range chunks {
		if err != nil {
			return err
		}
		if chunk.UsageMetadata != nil {
			usage = c.Usage{InputTokens: int64(chunk.UsageMetadata.PromptTokenCount), OutputTokens: int64(chunk.UsageMetadata.CandidatesTokenCount), TotalTokens: int64(chunk.UsageMetadata.TotalTokenCount)}
		}
		if len(chunk.Candidates) == 0 || chunk.Candidates[0].Content == nil {
			continue
		}
		for _, part := range chunk.Candidates[0].Content.Parts {
			if part.Thought {
				continue
			}
			if part.Text != "" {
				if err := emit(ProviderDelta{Text: part.Text}); err != nil {
					return err
				}
			}
			if call := part.FunctionCall; call != nil {
				raw, _ := json.Marshal(call.Args)
				if len(raw) > 100000 {
					return errors.New("tool arguments exceed budget")
				}
				id := call.ID
				if id == "" {
					id = newID()
				}
				calls = append(calls, c.ToolCall{ID: id, Name: call.Name, Arguments: string(raw), Signature: base64.StdEncoding.EncodeToString(part.ThoughtSignature)})
			}
		}
	}
	return emit(ProviderDelta{Calls: calls, Usage: usage})
}
