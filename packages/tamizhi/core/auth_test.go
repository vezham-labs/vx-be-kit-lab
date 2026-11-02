package core

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOIDCPKCEStateNonceAndSession(t *testing.T) {
	_, store := fixture(t, fixtureProvider{stream: func(context.Context, ProviderInput, func(ProviderDelta) error) error { return nil }})
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var issuer, nonce, challenge string
	encode := base64.RawURLEncoding.EncodeToString
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "test", "alg": "RS256", "use": "sig", "n": encode(key.PublicKey.N.Bytes()), "e": encode(big.NewInt(int64(key.PublicKey.E)).Bytes())}}})
		case "/token":
			_ = r.ParseForm()
			sum := sha256.Sum256([]byte(r.FormValue("code_verifier")))
			if encode(sum[:]) != challenge {
				t.Error("PKCE verification failed")
			}
			header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test"})
			claims, _ := json.Marshal(map[string]any{"iss": issuer, "aud": "tamizhi", "sub": "alice", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": nonce, "name": "Alice"})
			unsigned := encode(header) + "." + encode(claims)
			digest := sha256.Sum256([]byte(unsigned))
			signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
			if err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fake-access", "token_type": "Bearer", "id_token": unsigned + "." + encode(signature), "expires_in": 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	defer idp.Close()
	issuer = idp.URL
	auth, err := NewAuth(context.Background(), store, nil, OIDCOptions{Issuer: issuer, ClientID: "tamizhi", RedirectURL: "http://api.example/api/tamizhi/auth/callback", FrontendURL: "http://app.example", Connections: []LoginConnection{{ID: "google", Name: "Google", Params: map[string]string{"kc_idp_hint": "google"}}}, AdminSubjects: []string{"alice"}})
	if err != nil {
		t.Fatal(err)
	}
	anonymous := httptest.NewRecorder()
	auth.ServeHTTP(anonymous, httptest.NewRequest("GET", "/api/tamizhi/auth/session", nil))
	var info c.AuthSession
	if json.Unmarshal(anonymous.Body.Bytes(), &info) != nil || !info.LoginEnabled || info.User != nil {
		t.Fatal("invalid anonymous session status")
	}
	login := httptest.NewRecorder()
	auth.ServeHTTP(login, httptest.NewRequest("GET", "/api/tamizhi/auth/login?returnTo=http%3A%2F%2Fapp.example%2Fchat%2Fprevious&connection=google", nil))
	if login.Code != 302 {
		t.Fatal(login.Code)
	}
	location, err := url.Parse(login.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Query().Get("kc_idp_hint") != "google" {
		t.Fatal("missing configured login connection")
	}
	nonce = location.Query().Get("nonce")
	challenge = location.Query().Get("code_challenge")
	state := location.Query().Get("state")
	if nonce == "" || challenge == "" || location.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("missing OIDC protections")
	}
	callback := httptest.NewRequest("GET", "/api/tamizhi/auth/callback?code=test&state="+url.QueryEscape(state), nil)
	for _, cookie := range login.Result().Cookies() {
		callback.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	auth.ServeHTTP(response, callback)
	if response.Header().Get("Location") != "http://app.example/chat/previous" {
		t.Fatal("login lost its return destination")
	}
	if response.Code != 302 {
		body, _ := io.ReadAll(response.Result().Body)
		t.Fatalf("callback %d: %s", response.Code, body)
	}
	session := httptest.NewRequest("GET", "/api/tamizhi/models", nil)
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "tamizhi_session" {
			if !cookie.HttpOnly || cookie.Path != "/api/tamizhi" {
				t.Fatal("insecure session cookie")
			}
			session.AddCookie(cookie)
		}
	}
	user, err := auth.Authenticate(session)
	if err != nil || user.Name != "Alice" || !user.Admin() {
		t.Fatalf("session %+v %v", user, err)
	}
	replay := httptest.NewRecorder()
	auth.ServeHTTP(replay, callback)
	if replay.Code != 403 {
		t.Fatal("authorization state was reused")
	}
	logout := httptest.NewRequest("POST", "/api/tamizhi/auth/logout", strings.NewReader(""))
	for _, cookie := range session.Cookies() {
		logout.AddCookie(cookie)
	}
	auth.ServeHTTP(httptest.NewRecorder(), logout)
	if _, err = auth.Authenticate(session); err == nil {
		t.Fatal("logout did not revoke session")
	}
}

func TestOIDCReturnURLAllowlist(t *testing.T) {
	a := &Auth{options: OIDCOptions{FrontendURL: "https://app.example/chat/new", ReturnOrigins: []string{"https://embed.example"}}}
	for _, target := range []string{"https://evil.example", "//evil.example", "javascript:alert(1)", "https://app.example.evil/chat", "https://user:pass@app.example", "/chat/1", "http://app.example/chat"} {
		if got := a.returnURL(target); got != a.options.FrontendURL {
			t.Errorf("accepted unsafe destination %q: %q", target, got)
		}
	}
	for _, target := range []string{"https://app.example/chat/1?q=hello#message", "https://embed.example/page"} {
		if got := a.returnURL(target); got != target {
			t.Errorf("lost trusted destination: %q", got)
		}
	}
}

func TestLoginConnectionRejectsProtocolOverrides(t *testing.T) {
	_, store := fixture(t, fixtureProvider{stream: func(context.Context, ProviderInput, func(ProviderDelta) error) error { return nil }})
	_, err := NewAuth(context.Background(), store, nil, OIDCOptions{Connections: []LoginConnection{{ID: "bad", Name: "Bad", Params: map[string]string{"redirect_uri": "https://evil.example"}}}})
	if err == nil {
		t.Fatal("accepted authorization protocol override")
	}
}

func TestExternalIdentityDoesNotAdvertiseCookieLogout(t *testing.T) {
	_, store := fixture(t, fixtureProvider{stream: func(context.Context, ProviderInput, func(ProviderDelta) error) error { return nil }})
	token := strings.Repeat("x", 32)
	auth, err := NewAuth(context.Background(), store, []TokenCredential{{Token: token, Principal: c.Principal{ID: "host-user"}}}, OIDCOptions{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/tamizhi/auth/session", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	auth.ServeHTTP(response, req)
	var info c.AuthSession
	if json.Unmarshal(response.Body.Bytes(), &info) != nil || info.User == nil || info.User.ID != "host-user" || info.CanLogout || info.LoginEnabled {
		t.Fatal("host-managed identity advertised local logout or login")
	}
}
