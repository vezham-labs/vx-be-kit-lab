package core

import (
	"bytes"
	"context"
	"fmt"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func minimalPDF() []byte {
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	positions := []int{0}
	content := "BT /F1 12 Tf 10 10 Td (Hello PDF) Tj ET"
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)}
	for i, object := range objects {
		positions = append(positions, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, object)
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(positions))
	for _, offset := range positions[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(positions), xref)
	return out.Bytes()
}
func TestPDFImageAndAudioCapabilities(t *testing.T) {
	runtime, _ := fixture(t, idleProvider())
	principal := c.Principal{ID: "alice"}
	pdf, err := runtime.Upload(context.Background(), principal, "example.pdf", minimalPDF())
	if err != nil || !strings.Contains(pdf.Text, "Hello PDF") {
		t.Fatalf("PDF extraction %q %v", pdf.Text, err)
	}
	image, err := runtime.Upload(context.Background(), principal, "image.png", append([]byte{137, 80, 78, 71, 13, 10, 26, 10}, make([]byte, 32)...))
	if err != nil {
		t.Fatal(err)
	}
	messages := []c.Message{{Role: "user", Content: "look", Attachments: []c.Attachment{image}}}
	if _, err = runtime.resolveAttachments(context.Background(), principal, c.ModelInfo{Modalities: []string{"text"}}, messages); err == nil {
		t.Fatal("text-only model accepted image")
	}
	resolved, err := runtime.resolveAttachments(context.Background(), principal, c.ModelInfo{Modalities: []string{"image"}}, messages)
	if err != nil || !strings.HasPrefix(resolved[0].Attachments[0].Text, "data:image/png;base64,") {
		t.Fatal("image payload not resolved", err)
	}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-key" {
			t.Error("missing audio auth")
		}
		switch r.URL.Path {
		case "/audio/transcriptions":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			defer r.MultipartForm.RemoveAll()
			file, header, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				return
			}
			defer file.Close()
			body, _ := io.ReadAll(file)
			if header.Filename != "recording.webm" || string(body) != "recording" || r.FormValue("model") != "transcribe" {
				t.Error("invalid audio multipart")
			}
			fmt.Fprint(w, `{"text":"hello voice"}`)
		case "/audio/speech":
			w.Header().Set("Content-Type", "audio/mpeg")
			fmt.Fprint(w, "audio bytes")
		default:
			http.NotFound(w, r)
		}
	}))
	defer endpoint.Close()
	provider := &OpenAIProvider{Options: OpenAIProviderOptions{BaseURL: endpoint.URL, TranscriptionModel: "transcribe", SpeechModel: "speech", ResolveSecret: func(context.Context) (string, error) { return "fake-key", nil }}}
	text, err := provider.Transcribe(context.Background(), "recording.webm", "audio/webm", []byte("recording"))
	if err != nil || text != "hello voice" {
		t.Fatalf("transcription %q %v", text, err)
	}
	audio, mime, err := provider.Speech(context.Background(), c.SpeechRequest{Text: "hello"})
	if err != nil || mime != "audio/mpeg" || string(audio) != "audio bytes" {
		t.Fatal("speech", err)
	}
}
func TestCompactionCheckpointAndConfigReload(t *testing.T) {
	runtime, store := fixture(t, fixtureProvider{stream: func(ctx context.Context, input ProviderInput, emit func(ProviderDelta) error) error {
		return emit(ProviderDelta{Text: "Older facts and decisions"})
	}})
	ctx := context.Background()
	principal := c.Principal{ID: "alice"}
	item := c.Conversation{ID: "compact", Title: "Compaction", Messages: []c.Message{}}
	for i := 0; i < 18; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		item.Messages = append(item.Messages, c.Message{ID: fmt.Sprint(i), Role: role, Content: fmt.Sprint("message ", i)})
	}
	item, err := store.SaveConversation(ctx, principal.ID, item, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := store.Checkpoint(ctx, principal.ID, item.ID, "original")
	if err != nil {
		t.Fatal(err)
	}
	compacted, err := runtime.Compact(ctx, principal, item.ID, "")
	if err != nil || compacted.Summary == "" || len(compacted.Messages) != 18 {
		t.Fatal("compaction lost original history", err)
	}
	restored, err := store.RestoreCheckpoint(ctx, principal.ID, item.ID, checkpoint.ID)
	if err != nil || restored.Summary != "" {
		t.Fatal("checkpoint failed to restore summary", err)
	}
	if err = runtime.Mount(HTTPFetchPlugin(), true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tamizhi.json")
	if err = os.WriteFile(path, []byte(`{"plugins":{"http":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = WatchPluginConfig(ctx, path, runtime); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte(`{"plugins":{"http":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	disabled := false
	for time.Now().Before(deadline) {
		for _, plugin := range runtime.Plugins() {
			if plugin.Name == "http" && !plugin.Enabled {
				disabled = true
			}
		}
		if disabled {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !disabled {
		t.Fatal("config change did not unload plugin")
	}
	runtime.Close()
	if _, err = runtime.StartRun(principal, request("after-close")); err == nil {
		t.Fatal("closed runtime accepted work")
	}
}
