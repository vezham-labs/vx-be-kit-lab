package main

import (
	"bytes"
	"context"
	"encoding/json"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	core "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/core"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAllowedOriginsArePrivateValidatedAndReloaded(t *testing.T) {
	directory := t.TempDir()
	origins, err := readAllowedOrigins(directory, nil)
	if err != nil || len(origins) != 0 {
		t.Fatal(origins, err)
	}
	origins, err = readAllowedOrigins(directory, []string{"https://app.example", "https://app.example"})
	if err != nil || len(origins) != 1 {
		t.Fatal(origins, err)
	}
	again, err := readAllowedOrigins(directory, nil)
	if err != nil || len(again) != 1 || again[0] != origins[0] {
		t.Fatal(again, err)
	}
	if _, err = readAllowedOrigins(directory, []string{"https://app.example/private"}); err == nil {
		t.Fatal("origin path accepted")
	}
	file := filepath.Join(directory, "allowed-origins.json")
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	_ = os.Chmod(file, 0644)
	if _, err = readAllowedOrigins(directory, nil); err == nil {
		t.Fatal("public trust file accepted")
	}
}
func TestTerminalApprovalAndRejection(t *testing.T) {
	for _, answer := range []string{"y\n", "n\n", ""} {
		t.Run(strings.TrimSpace(answer), func(t *testing.T) {
			directory := t.TempDir()
			auth, _ := core.NewLocalAuth(strings.Repeat("a", 40), nil)
			r := httptest.NewRequest("POST", "http://127.0.0.1/api/tamizhi/auth/connect", strings.NewReader("{}"))
			r.Header.Set("Origin", "https://embed.example")
			w := httptest.NewRecorder()
			auth.ServeHTTP(w, r)
			var request c.CompanionConnection
			_ = json.Unmarshal(w.Body.Bytes(), &request)
			ctx, cancel := context.WithCancel(context.Background())
			var output bytes.Buffer
			done := make(chan struct{})
			go func() { handleConnections(ctx, auth, directory, strings.NewReader(answer), &output); close(done) }()
			defer func() { cancel(); <-done }()
			deadline := time.Now().Add(3 * time.Second)
			for auth.ConnectionPending(request.RequestID) && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			cancel()
			<-done
			if !strings.Contains(output.String(), "https://embed.example") || !strings.Contains(output.String(), request.Code) {
				t.Fatal("missing verification", output.String())
			}
			saved, err := readAllowedOrigins(directory, nil)
			if err != nil {
				t.Fatal(err)
			}
			if (answer == "y\n") != (len(saved) == 1) {
				t.Fatal(answer, saved)
			}
			if strings.Contains(output.String(), request.RequestID) {
				t.Fatal("private request capability printed")
			}
		})
	}
}
