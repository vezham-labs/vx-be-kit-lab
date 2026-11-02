package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"golang.org/x/oauth2"
)

type googleFixture struct {
	runtime                               *Runtime
	store                                 *Store
	server                                *httptest.Server
	client                                *http.Client
	refreshes, revocations, modelRequests atomic.Int32
	calls                                 atomic.Int32
}

func newGoogleFixture(t *testing.T) *googleFixture {
	t.Helper()
	runtime, store := fixture(t, idleProvider())
	f := &googleFixture{runtime: runtime, store: store}
	f.server = httptest.NewUnstartedServer(nil)
	callback := "http://" + f.server.Listener.Addr().String() + "/api/tamizhi/providers/gemini/oauth/callback"
	challenges := map[string]string{}
	var mu sync.Mutex
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			q := r.URL.Query()
			if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("access_type") != "offline" || q.Get("prompt") != "consent" || q.Get("redirect_uri") != callback {
				t.Error("missing OAuth protections")
			}
			code := newID()
			mu.Lock()
			challenges[code] = q.Get("code_challenge")
			mu.Unlock()
			http.Redirect(w, r, callback+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), 302)
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("client_secret") != "fixture-app-secret" {
				t.Error("missing application client secret")
			}
			access := "google-access-fixture"
			refresh := "google-refresh-fixture"
			if r.Form.Get("grant_type") == "authorization_code" {
				sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				mu.Lock()
				challenge, ok := challenges[r.Form.Get("code")]
				delete(challenges, r.Form.Get("code"))
				mu.Unlock()
				if !ok || challenge != base64.RawURLEncoding.EncodeToString(sum[:]) {
					w.WriteHeader(400)
					return
				}
			} else if r.Form.Get("grant_type") == "refresh_token" {
				f.refreshes.Add(1)
				access = "google-renewed-fixture"
				refresh = "google-rotated-fixture"
			} else {
				w.WriteHeader(400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_in": 3600})
		case "/revoke":
			_ = r.ParseForm()
			if r.Form.Get("token") == "" {
				t.Error("missing revoke token")
			}
			f.revocations.Add(1)
		case "/v1beta/models":
			if r.Header.Get("x-goog-api-key") == "fixture-api-key" { // Independent BYOK replacement.
			} else if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer google-") || r.Header.Get("x-goog-user-project") != "fixture-project" || r.Header.Get("x-goog-api-key") != "" {
				t.Error("missing owner OAuth or quota-project headers")
			}
			fmt.Fprint(w, `{"models":[{"name":"models/gemini-oauth","supportedGenerationMethods":["generateContent"]}]}`)
		case "/v1beta/models/gemini-oauth:streamGenerateContent":
			f.modelRequests.Add(1)
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer google-") || r.Header.Get("x-goog-user-project") != "fixture-project" || r.Header.Get("x-goog-api-key") != "" {
				t.Error("model used wrong account credentials")
			}
			body, _ := io.ReadAll(r.Body)
			n := f.calls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			if n == 1 {
				if !bytes.Contains(body, []byte(`"functionDeclarations"`)) || !bytes.Contains(body, []byte(`"inlineData"`)) {
					t.Error("OAuth stream lost tools or image input")
				}
				fmt.Fprint(w, "data: {\"candidates\":[{\"finishReason\":\"STOP\",\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"id\":\"oauth-call\",\"name\":\"read_number\",\"args\":{}},\"thoughtSignature\":\"AQID\"}]}}]}\n\n")
			} else {
				if !bytes.Contains(body, []byte(`"functionResponse"`)) || !bytes.Contains(body, []byte(`"thoughtSignature":"AQID"`)) {
					t.Error("OAuth tool response lost its identity or signature")
				}
				fmt.Fprint(w, "data: {\"candidates\":[{\"finishReason\":\"STOP\",\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Account tool verified\"}]}}],\"usageMetadata\":{\"promptTokenCount\":5,\"candidatesTokenCount\":3,\"totalTokenCount\":8}}\n\n")
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(google.Close)
	runtime.options.BYOKProviders = []BYOKDefinition{{ID: "gemini", Name: "Gemini", Kind: "gemini", BaseURL: google.URL + "/v1beta"}}
	runtime.options.GeminiOAuth = &GeminiOAuthOptions{ClientID: "fixture-client", ClientSecret: "fixture-app-secret", RedirectURL: callback, FrontendURL: "https://app.example/chat/new", AuthURL: google.URL + "/authorize", TokenURL: google.URL + "/token", RevokeURL: google.URL + "/revoke"}
	authenticate := AuthFunc(func(r *http.Request) (c.Principal, error) {
		id := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if id == "" {
			return c.Principal{}, ErrForbidden
		}
		return c.Principal{ID: id}, nil
	})
	f.server.Config.Handler = NewAPI(runtime, HTTPOptions{Authenticate: authenticate, Origins: []string{"https://app.example"}})
	f.server.Start()
	t.Cleanup(f.server.Close)
	jar, _ := cookiejar.New(nil)
	f.client = &http.Client{Jar: jar, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return f
}
func (f *googleFixture) begin(t *testing.T) string {
	t.Helper()
	req, _ := http.NewRequest("POST", f.server.URL+"/api/tamizhi/providers/gemini/oauth", strings.NewReader(`{"projectId":"fixture-project","returnTo":"https://app.example/chat/saved"}`))
	req.Header.Set("Authorization", "Bearer alice")
	response, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("OAuth start rejected", response.StatusCode)
	}
	var result c.ProviderAuthorization
	if json.NewDecoder(response.Body).Decode(&result) != nil {
		t.Fatal("invalid authorization response")
	}
	return result.URL
}
func (f *googleFixture) callback(t *testing.T, authorize string) string {
	t.Helper()
	response, err := f.client.Get(authorize)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.Header.Get("Location")
}
func (f *googleFixture) complete(t *testing.T, callback string) *http.Response {
	t.Helper()
	response, err := f.client.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response
}
func TestGeminiOAuthAccountToolsRefreshAndDisconnect(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	owner := c.Principal{ID: "alice"}
	callback := f.callback(t, f.begin(t))
	response := f.complete(t, callback)
	if response.StatusCode != 302 || !strings.Contains(response.Header.Get("Location"), "tamizhi_oauth=connected") || !strings.Contains(response.Header.Get("Location"), "/chat/saved") {
		t.Fatal("Google connection did not return to the host page")
	}
	if f.complete(t, callback).StatusCode != 403 {
		t.Fatal("OAuth state was reused")
	}
	raw, _ := f.store.Setting(ctx, owner.ID, "credential.gemini")
	if strings.Contains(raw, "google-access-fixture") || strings.Contains(raw, "google-refresh-fixture") {
		t.Fatal("provider tokens were stored unencrypted")
	}
	settings, err := f.runtime.ProviderSettings(ctx, owner)
	if err != nil || len(settings) != 1 || !settings[0].OAuthAvailable || settings[0].AuthMethod != "oauth" {
		t.Fatal("missing connected account metadata")
	}
	other, _ := f.runtime.ProviderSettings(ctx, c.Principal{ID: "bob"})
	if other[0].Connected {
		t.Fatal("another owner saw the grant")
	}
	service, _ := f.runtime.credentials()
	saved, _ := service.load(ctx, owner.ID, "gemini")
	plain, _ := service.open(owner.ID, "gemini", saved.Ciphertext)
	var token oauth2.Token
	_ = json.Unmarshal([]byte(plain), &token)
	token.Expiry = time.Now().Add(-time.Minute)
	encoded, _ := json.Marshal(token)
	saved.Ciphertext = service.seal(owner.ID, "gemini", string(encoded))
	encoded, _ = json.Marshal(saved)
	_ = f.store.SetSetting(ctx, owner.ID, "credential.gemini", string(encoded))
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Go(func() {
			if access, err := service.oauthAccess(ctx, owner.ID, saved.Revision); err != nil || access != "google-renewed-fixture" {
				t.Error("failed token refresh", err)
			}
		})
	}
	group.Wait()
	renewed, _ := service.load(ctx, owner.ID, "gemini")
	rotatedPlain, _ := service.open(owner.ID, "gemini", renewed.Ciphertext)
	var rotated oauth2.Token
	_ = json.Unmarshal([]byte(rotatedPlain), &rotated)
	if rotated.RefreshToken != "google-rotated-fixture" {
		t.Fatal("rotated refresh token was not persisted")
	}
	if f.refreshes.Load() != 1 {
		t.Fatal("concurrent requests refreshed more than once")
	}
	if err := f.runtime.Mount(ToolPlugin(Tool{Name: "read_number", Description: "read number", Risk: "read", Schema: objectSchema(map[string]any{}), Execute: func(context.Context, json.RawMessage, ToolContext) (any, error) {
		return map[string]int{"number": 7}, nil
	}}), true); err != nil {
		t.Fatal(err)
	}
	input := request("google-oauth-tools")
	input.Model = "gemini/gemini-oauth"
	input.Messages[0].Attachments = []c.Attachment{{ID: "image", Name: "pixel.png", MimeType: "image/png", Text: "data:image/png;base64,AQID"}}
	if err := f.store.PutAttachment(ctx, owner.ID, input.Messages[0].Attachments[0], []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	run, err := f.runtime.StartRun(owner, input)
	if err != nil {
		t.Fatal(err)
	}
	if waitRun(t, f.store, owner.ID, run.ID).Status != "complete" {
		t.Fatal("OAuth tool loop failed")
	}
	conversation, _ := f.store.Conversation(ctx, owner.ID, input.ConversationID)
	if conversation.Messages[len(conversation.Messages)-1].Content != "Account tool verified" || f.modelRequests.Load() != 2 {
		t.Fatal("OAuth tool loop did not finish")
	}
	if err := f.runtime.DeleteProviderKey(ctx, owner, "gemini"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.oauthAccess(ctx, owner.ID, saved.Revision); err == nil {
		t.Fatal("disconnected grant was resurrected")
	}
	if f.revocations.Load() != 1 {
		t.Fatal("Google grant was not revoked")
	}
}
func TestGeminiOAuthCancellationAndCredentialFencing(t *testing.T) {
	f := newGoogleFixture(t)
	ctx := context.Background()
	owner := c.Principal{ID: "alice"}
	callback := f.callback(t, f.begin(t))
	if _, err := f.runtime.SaveProviderKey(ctx, owner, "gemini", "fixture-api-key"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.complete(t, callback).Header.Get("Location"), "tamizhi_oauth=failed") {
		t.Fatal("stale authorization overwrote a newer credential")
	}
	settings, _ := f.runtime.ProviderSettings(ctx, owner)
	if settings[0].AuthMethod != "api-key" {
		t.Fatal("API key was overwritten")
	}
	authorization, _ := url.Parse(f.begin(t))
	denied := f.server.URL + "/api/tamizhi/providers/gemini/oauth/callback?error=access_denied&state=" + url.QueryEscape(authorization.Query().Get("state"))
	if !strings.Contains(f.complete(t, denied).Header.Get("Location"), "tamizhi_oauth=failed") {
		t.Fatal("denial did not return safely")
	}
	settings, _ = f.runtime.ProviderSettings(ctx, owner)
	if settings[0].AuthMethod != "api-key" {
		t.Fatal("denial changed the existing connection")
	}
	lateCallback := f.callback(t, f.begin(t))
	if err := f.runtime.DeleteProviderKey(ctx, owner, "gemini"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.complete(t, lateCallback).Header.Get("Location"), "tamizhi_oauth=failed") {
		t.Fatal("late authorization resurrected a disconnected connection")
	}
	forged := f.server.URL + "/api/tamizhi/providers/gemini/oauth/callback?code=fake&state=" + strings.Repeat("x", 64)
	if f.complete(t, forged).StatusCode != 403 {
		t.Fatal("accepted a forged OAuth callback")
	}
}

func TestGeminiOAuthRejectsExpiredStateAndDifferentSession(t *testing.T) {
	for _, scenario := range []string{"expired", "different-user"} {
		t.Run(scenario, func(t *testing.T) {
			f := newGoogleFixture(t)
			authorization := f.begin(t)
			parsed, _ := url.Parse(authorization)
			state := parsed.Query().Get("state")
			callback := f.callback(t, authorization)
			if scenario == "expired" {
				_, err := f.store.db.ExecContext(context.Background(), f.store.query(`UPDATE oauth_states SET expires_at=? WHERE hash=?`), now()-1, authHash(state))
				if err != nil {
					t.Fatal(err)
				}
			}
			req, _ := http.NewRequest("GET", callback, nil)
			if scenario == "different-user" {
				req.Header.Set("Authorization", "Bearer bob")
			}
			response, err := f.client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 403 {
				t.Fatal("unsafe callback was accepted")
			}
			settings, _ := f.runtime.ProviderSettings(context.Background(), c.Principal{ID: "alice"})
			if settings[0].Connected {
				t.Fatal("unsafe callback saved credentials")
			}
		})
	}
}

func TestGeminiOAuthTransportRejectsCredentialForwarding(t *testing.T) {
	var accessed bool
	transport := &providerOAuthTransport{origin: "https://generativelanguage.googleapis.com/v1beta", project: "fixture-project", access: func(context.Context) (string, error) { accessed = true; return "secret", nil }}
	request, _ := http.NewRequest("GET", "https://attacker.example/models", nil)
	if _, err := transport.RoundTrip(request); err == nil || accessed {
		t.Fatal("credentials were requested for another host")
	}
}
