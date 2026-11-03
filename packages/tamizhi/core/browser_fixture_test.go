package core

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	cordis "github.com/tr1v3r/cordis-go"
)

// TestBrowserFixtureServer is an opt-in process used only by local Playwright
// verification. It never loads production credentials or a production database.
func TestBrowserFixtureServer(t *testing.T) {
	if os.Getenv("TAMIZHI_BROWSER_FIXTURE") != "1" {
		t.Skip("browser fixture is opt-in")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := OpenStore(ctx, "sqlite", filepath.Join(t.TempDir(), "browser.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runtime, err := NewRuntime(ctx, store, RuntimeOptions{GeminiOAuth: &GeminiOAuthOptions{ClientID: "google-browser-fixture", ClientSecret: "browser-google-secret", RedirectURL: "http://127.0.0.1:4398/api/tamizhi/providers/gemini/oauth/callback", FrontendURL: "http://127.0.0.1:4399/chat/new", AuthURL: "http://127.0.0.1:4397/authorize", TokenURL: "http://127.0.0.1:4397/token", RevokeURL: "http://127.0.0.1:4397/revoke"}, CredentialKey: bytes.Repeat([]byte{2}, 32), BYOKProviders: []BYOKDefinition{{ID: "openai", Name: "OpenAI browser fixture", Kind: "openai", BaseURL: "http://127.0.0.1:4397/v1"}, {ID: "gemini", Name: "Gemini browser fixture", Kind: "gemini", BaseURL: "http://127.0.0.1:4397/v1beta"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err = runtime.Mount(MemoryPlugin(), true); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	if err := runtime.Mount(PluginDefinition{Name: "optional.browser-retry", Kind: "test", Optional: true, Apply: func(*cordis.Context, *Runtime) error {
		attempts++
		if attempts == 1 {
			return errors.New("browser activation failure")
		}
		return nil
	}}, true); !errors.Is(err, errPluginActivation) {
		t.Fatalf("expected fixture activation failure: %v", err)
	}
	auth, err := NewAuth(ctx, store, nil, OIDCOptions{Connections: []LoginConnection{{ID: "google", Name: "Google", Params: map[string]string{"kc_idp_hint": "google"}}, {ID: "microsoft", Name: "Microsoft", Params: map[string]string{"kc_idp_hint": "microsoft"}}, {ID: "github", Name: "GitHub", Params: map[string]string{"kc_idp_hint": "github"}}, {ID: "sso", Name: "Organization SSO", Params: map[string]string{"kc_idp_hint": "sso"}}}, Issuer: "http://127.0.0.1:4397", ClientID: "browser-fixture", RedirectURL: "http://127.0.0.1:4398/api/tamizhi/auth/callback", FrontendURL: "http://127.0.0.1:4399/chat/new", AdminSubjects: []string{"browser-user"}})
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Addr: "127.0.0.1:4398", Handler: NewAPI(runtime, HTTPOptions{Authenticate: auth, AuthRoutes: auth, OIDC: true, Origins: []string{"http://127.0.0.1:4399"}}), ReadHeaderTimeout: 10 * time.Second}
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
