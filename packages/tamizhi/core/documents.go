package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

func (r *Runtime) Upload(ctx context.Context, principal c.Principal, name string, data []byte) (c.Attachment, error) {
	if principal.ID == "" {
		return c.Attachment{}, ErrForbidden
	}
	if len(data) == 0 || len(data) > 10*1024*1024 {
		return c.Attachment{}, errors.New("attachment must contain between 1 byte and 10 MB")
	}
	name = filepath.Base(name)
	if len(name) > 200 {
		name = name[:200]
	}
	mime := http.DetectContentType(data)
	item := c.Attachment{ID: newID(), Name: name, MimeType: mime, Size: int64(len(data))}
	switch {
	case strings.HasPrefix(mime, "image/"):
		if mime != "image/png" && mime != "image/jpeg" && mime != "image/gif" && mime != "image/webp" {
			return item, errors.New("unsupported image format")
		}
	case mime == "application/pdf":
		if err := ctx.Err(); err != nil {
			return item, err
		}
		reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return item, errors.New("unable to parse PDF")
		}
		text, err := reader.GetPlainText()
		if err != nil {
			return item, errors.New("unable to extract PDF text")
		}
		plain, err := io.ReadAll(io.LimitReader(text, 100001))
		if err != nil || len(plain) > 100000 {
			return item, errors.New("PDF text exceeds 100 KB")
		}
		item.Text = string(plain)
	default:
		extension := strings.ToLower(filepath.Ext(name))
		if !contains([]string{".txt", ".md", ".csv", ".json", ".log", ".go", ".ts", ".tsx", ".py"}, extension) || !utf8.Valid(data) {
			return item, errors.New("unsupported attachment type")
		}
		if len(data) > 100000 {
			return item, errors.New("text attachment exceeds 100 KB")
		}
		item.MimeType = "text/plain"
		item.Text = string(data)
	}
	if err := ctx.Err(); err != nil {
		return item, err
	}
	return item, r.store.PutAttachment(ctx, principal.ID, item, data)
}
func (r *Runtime) resolveAttachments(ctx context.Context, principal c.Principal, model c.ModelInfo, messages []c.Message) ([]c.Message, error) {
	for i := range messages {
		messages[i].Attachments = append([]c.Attachment{}, messages[i].Attachments...)
		for j, input := range messages[i].Attachments {
			item, data, err := r.store.Attachment(ctx, principal.ID, input.ID)
			if err != nil {
				return nil, errors.New("attachment is unavailable for this user")
			}
			if strings.HasPrefix(item.MimeType, "image/") {
				if !contains(model.Modalities, "image") {
					return nil, fmt.Errorf("selected model does not support image input")
				}
				item.Text = imageDataURL(item.MimeType, data)
			}
			messages[i].Attachments[j] = item
		}
	}
	return messages, nil
}
func (r *Runtime) audio() AudioProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.providers {
		if provider, ok := entry.provider.(*OpenAIProvider); ok && (provider.Options.TranscriptionModel != "" || provider.Options.SpeechModel != "") {
			return provider
		}
	}
	return nil
}
func (r *Runtime) Capabilities(oidc bool) c.Capabilities {
	result := c.Capabilities{Version: "1", PDF: true, Sessions: true, Replay: true, Plugins: true, Memory: true, Checkpoints: true, OIDC: oidc}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.providers {
		for _, model := range entry.provider.Models() {
			if contains(model.Modalities, "image") {
				result.Images = true
			}
		}
		if provider, ok := entry.provider.(*OpenAIProvider); ok {
			result.Transcription = result.Transcription || provider.Options.TranscriptionModel != ""
			result.Speech = result.Speech || provider.Options.SpeechModel != ""
		}
	}
	return result
}
