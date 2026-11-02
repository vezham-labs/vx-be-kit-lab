package core

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/url"
	"strings"

	"google.golang.org/genai"
)

// The Go SDK's Gemini API initializer requires an API key. OAuth uses the
// documented REST endpoint with the same SDK wire types and bounded SSE parsing.
func (p *GeminiProvider) oauthStream(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) iter.Seq2[*genai.GenerateContentResponse, error] {
	return func(yield func(*genai.GenerateContentResponse, error) bool) {
		if p.HTTPClient == nil {
			yield(nil, errors.New("Google OAuth requires an authorized HTTP client"))
			return
		}
		payload := map[string]any{"contents": contents, "generationConfig": map[string]any{"maxOutputTokens": config.MaxOutputTokens}}
		if config.SystemInstruction != nil {
			payload["systemInstruction"] = config.SystemInstruction
		}
		if len(config.Tools) > 0 {
			payload["tools"] = config.Tools
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			yield(nil, err)
			return
		}
		address := strings.TrimRight(p.Definition.BaseURL, "/") + "/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"
		request, err := http.NewRequestWithContext(ctx, "POST", address, bytes.NewReader(raw))
		if err != nil {
			yield(nil, err)
			return
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "text/event-stream")
		response, err := p.HTTPClient.Do(request)
		if err != nil {
			yield(nil, errors.New("Google model request failed"))
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			yield(nil, errors.New("Google model rejected this request; review project permissions or quota"))
			return
		}
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 4096), 4<<20)
		var data strings.Builder
		finished := false
		flush := func() bool {
			if data.Len() == 0 {
				return true
			}
			var chunk genai.GenerateContentResponse
			err := json.Unmarshal([]byte(data.String()), &chunk)
			data.Reset()
			if err != nil {
				yield(nil, errors.New("invalid Google response stream"))
				return false
			}
			for _, candidate := range chunk.Candidates {
				if candidate.FinishReason != "" {
					finished = true
				}
			}
			return yield(&chunk, nil)
		}
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				if !flush() {
					return
				}
				continue
			}
			if strings.HasPrefix(line, "data:") {
				if data.Len()+len(line) > 4<<20 {
					yield(nil, errors.New("Google stream event exceeds budget"))
					return
				}
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
				data.WriteByte('\n')
			}
		}
		if !flush() {
			return
		}
		if scanner.Err() != nil {
			yield(nil, errors.New("unable to read Google response stream"))
			return
		}
		if !finished {
			yield(nil, errors.New("Google response stream ended before completion"))
		}
	}
}
