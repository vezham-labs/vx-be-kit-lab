package main

import (
	"bytes"
	"context"
	"encoding/json"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	core "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/core"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalCLIHelpAndLoopbackBinding(t *testing.T) {
	var out bytes.Buffer
	if err := command(context.Background(), []string{"help"}, strings.NewReader(""), &out, io.Discard); err != nil || !strings.Contains(out.String(), "tamizhi serve") {
		t.Fatal(out.String(), err)
	}
	if _, err := localURL("0.0.0.0:7331"); err == nil {
		t.Fatal("public listener allowed")
	}
	if _, err := localURL("127.0.0.1:7331"); err != nil {
		t.Fatal(err)
	}
}
func TestAdministratorCredentialIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	token, err := administratorToken(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	again, err := administratorToken(dir, false)
	if err != nil || again != token {
		t.Fatal(err)
	}
	if err = os.Chmod(filepath.Join(dir, "local-token"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = administratorToken(dir, false); err == nil {
		t.Fatal("world-readable token accepted")
	}
}
func TestCLIChatUsesTheSameStreamingAPI(t *testing.T) {
	dir := t.TempDir()
	token, _ := administratorToken(dir, true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("Origin") != "" {
			t.Error("invalid CLI authentication")
		}
		if r.URL.Path != "/api/tamizhi/chat" {
			t.Error(r.URL.Path)
		}
		var input c.RunRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.Messages[0].Content != "hello" {
			t.Error(input)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []c.StreamEvent{{Type: "start", RunID: "run", ConversationID: input.ConversationID}, {Type: "text", RunID: "run", ConversationID: input.ConversationID, Delta: "hello back"}, {Type: "finish", RunID: "run", ConversationID: input.ConversationID, Reason: "complete"}} {
			raw, _ := json.Marshal(event)
			w.Write([]byte("data: " + string(raw) + "\n\n"))
		}
	}))
	defer server.Close()
	var out bytes.Buffer
	err := command(context.Background(), []string{"chat", "--data-dir", dir, "--listen", strings.TrimPrefix(server.URL, "http://"), "--permission", "safe", "hello"}, strings.NewReader(""), &out, io.Discard)
	if err != nil || out.String() != "hello back\n" {
		t.Fatal(out.String(), err)
	}
}

func TestIndependentCompanionStartsWithoutNeonOrOIDC(t *testing.T) {
	t.Setenv("TAMIZHI_ENCRYPTION_KEY", "")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveLocal(ctx, []string{"--data-dir", directory, "--listen", address, "--origin", "https://tamizhi.example"}, strings.NewReader(""), io.Discard)
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("companion did not stop")
		}
	}()
	// vx-bot/NOTE: Close this test's own probe connections before waiting for server shutdown.
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	defer client.CloseIdleConnections()
	ready := false
	for tries := 0; tries < 100; tries++ {
		if response, err := client.Get("http://" + address + "/healthz"); err == nil {
			ready = response.StatusCode == 200
			response.Body.Close()
			if ready {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("companion failed to start")
	}
	token, err := administratorToken(directory, false)
	if err != nil {
		t.Fatal(err)
	}
	response, err := companionRequest(ctx, client, "http://"+address+"/api/tamizhi", token, "/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var models []c.ModelInfo
	if err = json.NewDecoder(response.Body).Decode(&models); err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != core.LeoModelID {
		t.Fatal("Leo must be available without provider credentials", models)
	}
}
