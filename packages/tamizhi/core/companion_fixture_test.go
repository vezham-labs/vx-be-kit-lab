package core

import (
	"context"
	"encoding/json"
	"errors"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Opt-in integration fixture uses real auth, HTTP, SQLite and tool execution; inference and web pages are deterministic.
func TestCompanionFixtureServer(t *testing.T) {
	if os.Getenv("TAMIZHI_COMPANION_FIXTURE") != "1" {
		t.Skip("companion fixture is opt-in")
	}
	directory := os.Getenv("TAMIZHI_COMPANION_FIXTURE_DIR")
	if directory == "" || !filepath.IsAbs(directory) {
		t.Fatal("fixture requires an explicitly supplied temporary directory")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	token, err := localSecret()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(directory, "local-token"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(token); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	auth, err := NewLocalAuth(token, []string{"http://127.0.0.1:4419", "http://test.vezham.local:4419"})
	if err != nil {
		t.Fatal(err)
	}
	runtime, _ := fixture(t, fixtureProvider{stream: func(ctx context.Context, input ProviderInput, emit func(ProviderDelta) error) error {
		tools := []c.Message{}
		for _, message := range input.Messages {
			if message.Role == "tool" {
				tools = append(tools, message)
			}
		}
		call := c.ToolCall{ID: "search", Name: "web_search", Arguments: `{"query":"latest React version"}`}
		switch len(tools) {
		case 0:
		case 1:
			var result c.WebSearchResult
			_ = json.Unmarshal([]byte(tools[0].Content), &result)
			if len(result.Sources) == 0 {
				return emit(ProviderDelta{Text: "Research unavailable."})
			}
			raw, _ := json.Marshal(map[string]string{"url": result.Sources[0].URL})
			call = c.ToolCall{ID: "open", Name: "web_open", Arguments: string(raw)}
		case 2:
			var result c.WebDocument
			_ = json.Unmarshal([]byte(tools[1].Content), &result)
			raw, _ := json.Marshal(map[string]string{"pageId": result.PageID, "query": "React version"})
			call = c.ToolCall{ID: "read", Name: "web_read", Arguments: string(raw)}
		default:
			var result c.WebDocument
			_ = json.Unmarshal([]byte(tools[2].Content), &result)
			if len(result.Sources) == 0 || !strings.Contains(result.Content, "99.0.0") {
				return emit(ProviderDelta{Text: "Source unavailable."})
			}
			for _, text := range []string{"React fixture ", "99.0.0: ", result.Sources[0].URL} {
				if err := emit(ProviderDelta{Text: text}); err != nil {
					return err
				}
			}
			return nil
		}
		return emit(ProviderDelta{Calls: []c.ToolCall{call}})
	}})
	if err = runtime.Mount(LeoPlugin(), true); err != nil {
		t.Fatal(err)
	}
	runtime.options.RunTimeout = time.Minute
	if err = runtime.Mount(WebPlugin(WebOptions{Client: webFixtureClient(), Search: webFixtureSearch}), true); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Addr: "127.0.0.1:4418", ReadHeaderTimeout: 5 * time.Second, Handler: NewAPI(runtime, HTTPOptions{Origins: []string{"http://127.0.0.1:4419", "http://test.vezham.local:4419"}, Authenticate: auth, AuthRoutes: auth})}
	failed := make(chan error, 1)
	go func() { failed <- server.ListenAndServe() }()
	select {
	case err = <-failed:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatal(err)
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}
