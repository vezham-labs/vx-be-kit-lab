package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

func idleProvider() Provider {
	return fixtureProvider{stream: func(context.Context, ProviderInput, func(ProviderDelta) error) error { return nil }}
}
func TestWorkspaceAndHTTPBoundaries(t *testing.T) {
	runtime, _ := fixture(t, idleProvider())
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Mount(WorkspacePlugin(map[string]string{"alice": root}), true); err != nil {
		t.Fatal(err)
	}
	runtime.mu.RLock()
	read := runtime.tools["files_read"]
	write := runtime.tools["files_write"]
	runtime.mu.RUnlock()
	tool := ToolContext{Principal: c.Principal{ID: "alice"}}
	if _, err := read.Execute(context.Background(), json.RawMessage(`{"path":"escape/secret"}`), tool); err == nil {
		t.Fatal("symlink escaped workspace")
	}
	if _, err := read.Execute(context.Background(), json.RawMessage(`{"path":"../secret"}`), tool); err == nil {
		t.Fatal("relative traversal escaped workspace")
	}
	if _, err := write.Execute(context.Background(), json.RawMessage(`{"path":"note.txt","content":"hello"}`), tool); err != nil {
		t.Fatal(err)
	}
	output, err := read.Execute(context.Background(), json.RawMessage(`{"path":"note.txt"}`), tool)
	if err != nil || output.(map[string]string)["content"] != "hello" {
		t.Fatalf("read: %v %v", output, err)
	}
	if _, err = read.Execute(context.Background(), json.RawMessage(`{"path":"note.txt"}`), ToolContext{Principal: c.Principal{ID: "bob"}}); err == nil {
		t.Fatal("other user read workspace")
	}
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "::ffff:127.0.0.1", "2001:db8::1"} {
		if publicIP(netip.MustParseAddr(address)) {
			t.Fatal("nonpublic address accepted", address)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("HTTP tool reached a private endpoint") }))
	defer server.Close()
	response, err := publicHTTPClient().Get(server.URL)
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("private HTTP request succeeded")
	}
}
func TestMCPDiscoveryAndCall(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "echo", Description: "echo", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}}}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo result"}}}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer endpoint.Close()
	runtime, _ := fixture(t, idleProvider())
	if err := runtime.Mount(MCPPlugin(MCPOptions{Name: "fixture", URL: endpoint.URL, AllowTools: []string{"echo"}}), true); err != nil {
		t.Fatal(err)
	}
	runtime.mu.RLock()
	tool, ok := runtime.tools["mcp_fixture_echo"]
	runtime.mu.RUnlock()
	if !ok {
		t.Fatal("MCP tool not discovered")
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"text":"hello"}`), ToolContext{})
	raw, _ := json.Marshal(result)
	if err != nil || !strings.Contains(string(raw), "echo result") {
		t.Fatalf("MCP output %s %v", raw, err)
	}
	if err = runtime.SetPlugin("mcp.fixture", false); err != nil {
		t.Fatal(err)
	}
	runtime.mu.RLock()
	_, ok = runtime.tools["mcp_fixture_echo"]
	runtime.mu.RUnlock()
	if ok {
		t.Fatal("MCP tool survived plugin unload")
	}
}
func TestAttachmentsMemoryAndSubagent(t *testing.T) {
	provider := fixtureProvider{stream: func(ctx context.Context, input ProviderInput, emit func(ProviderDelta) error) error {
		last := input.Messages[len(input.Messages)-1]
		if last.Content == "delegate" {
			return emit(ProviderDelta{Calls: []c.ToolCall{{ID: "delegate", Name: "delegate_task", Arguments: `{"task":"child"}`}}})
		}
		if last.Role == "tool" {
			return emit(ProviderDelta{Text: "parent done"})
		}
		return emit(ProviderDelta{Text: "child done"})
	}}
	runtime, store := fixture(t, provider)
	if err := runtime.Mount(SubagentPlugin(), true); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Mount(MemoryPlugin(), true); err != nil {
		t.Fatal(err)
	}
	principal := c.Principal{ID: "alice"}
	attachment, err := runtime.Upload(context.Background(), principal, "notes.txt", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Attachment(context.Background(), "bob", attachment.ID); err == nil {
		t.Fatal("cross-user attachment access")
	}
	if _, err = runtime.Upload(context.Background(), principal, "script.exe", []byte{0, 1, 2}); err == nil {
		t.Fatal("unsupported upload accepted")
	}
	if _, err = store.AddMemory(context.Background(), "alice", c.MemoryRequest{Text: "loves Go"}); err != nil {
		t.Fatal(err)
	}
	items, err := store.Memories(context.Background(), "bob", "Go")
	if err != nil || len(items) != 0 {
		t.Fatal("cross-user memory access")
	}
	input := request("delegation")
	input.Messages[0].Content = "delegate"
	run, err := runtime.StartRun(principal, input)
	if err != nil {
		t.Fatal(err)
	}
	if status := waitRun(t, store, "alice", run.ID).Status; status != "complete" {
		t.Fatal(status)
	}
	events, err := store.RunEvents(context.Background(), "alice", run.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	child := false
	for _, event := range events {
		if event.Type == "subagent-event" {
			child = true
		}
	}
	if !child {
		t.Fatal("child events were not forwarded")
	}
}
