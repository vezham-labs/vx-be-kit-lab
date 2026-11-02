package core

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"golang.org/x/oauth2"
)

// GeminiOAuthOptions is server-owned registration, never request-controlled endpoints.
type GeminiOAuthOptions struct {
	ClientID, ClientSecret, RedirectURL, FrontendURL string
	AuthURL, TokenURL, RevokeURL                     string
}

func (o GeminiOAuthOptions) config() oauth2.Config {
	authURL, tokenURL := o.AuthURL, o.TokenURL
	if authURL == "" {
		authURL = "https://accounts.google.com/o/oauth2/v2/auth"
	}
	if tokenURL == "" {
		tokenURL = "https://oauth2.googleapis.com/token"
	}
	return oauth2.Config{ClientID: o.ClientID, ClientSecret: o.ClientSecret, RedirectURL: o.RedirectURL, Endpoint: oauth2.Endpoint{AuthURL: authURL, TokenURL: tokenURL, AuthStyle: oauth2.AuthStyleInParams}, Scopes: []string{"https://www.googleapis.com/auth/cloud-platform", "https://www.googleapis.com/auth/generative-language.retriever"}}
}

type providerOAuthPending struct {
	Owner, Verifier, ProjectID, ReturnURL, Revision string
}
type providerOAuthState struct {
	Owner      string
	Ciphertext []byte
}

var googleProjectID = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

func (s *credentialService) credentialLock(owner, provider string) *sync.Mutex {
	sum := sha256.Sum256([]byte(owner + "\x00" + provider))
	return &s.locks[int(sum[0])%len(s.locks)]
}

func (a *API) startGeminiOAuth(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	service, err := a.Runtime.credentials()
	if err != nil || service.oauth == nil {
		apiError(w, ErrNotFound)
		return
	}
	var input c.ProviderOAuthRequest
	if !decode(w, r, &input) {
		return
	}
	if !googleProjectID.MatchString(input.ProjectID) {
		apiError(w, errors.New("invalid Google Cloud project ID"))
		return
	}
	if _, err = service.definition("gemini"); err != nil {
		apiError(w, err)
		return
	}
	guard := &Auth{options: OIDCOptions{FrontendURL: service.oauth.FrontendURL, ReturnOrigins: a.Options.Origins}}
	pending := providerOAuthPending{Owner: p.ID, Verifier: oauth2.GenerateVerifier(), ProjectID: input.ProjectID, ReturnURL: guard.returnURL(input.ReturnTo)}
	lock := service.credentialLock(p.ID, "gemini")
	lock.Lock()
	saved, loadErr := service.load(r.Context(), p.ID, "gemini")
	if loadErr == nil {
		pending.Revision = saved.Revision
	}
	lock.Unlock()
	if loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows) {
		apiError(w, loadErr)
		return
	}
	raw, _ := json.Marshal(pending)
	wrapped, _ := json.Marshal(providerOAuthState{Owner: p.ID, Ciphertext: service.seal(p.ID, "gemini-oauth-state", string(raw))})
	state := newID() + newID()
	_, _ = service.store.db.ExecContext(r.Context(), service.store.query(`DELETE FROM oauth_states WHERE expires_at<?`), now())
	_, err = service.store.db.ExecContext(r.Context(), service.store.query(`INSERT INTO oauth_states(hash,data,expires_at) VALUES(?,?,?)`), authHash(state), string(wrapped), time.Now().Add(5*time.Minute).UnixMilli())
	if err != nil {
		apiError(w, err)
		return
	}
	setProviderOAuthCookie(w, service.oauth.RedirectURL, state, 300)
	config := service.oauth.config()
	address := config.AuthCodeURL(state, oauth2.S256ChallengeOption(pending.Verifier), oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("prompt", "consent"))
	respond(w, 200, c.ProviderAuthorization{URL: address})
}

func setProviderOAuthCookie(w http.ResponseWriter, redirect, value string, age int) {
	parsed, _ := url.Parse(redirect)
	http.SetCookie(w, &http.Cookie{Name: "tamizhi_gemini_oauth", Value: value, Path: "/api/tamizhi/providers/gemini/oauth", HttpOnly: true, Secure: parsed != nil && parsed.Scheme == "https", SameSite: http.SameSiteLaxMode, MaxAge: age})
}

func (a *API) finishGeminiOAuth(w http.ResponseWriter, r *http.Request) {
	service, err := a.Runtime.credentials()
	if err != nil || service.oauth == nil {
		apiError(w, ErrNotFound)
		return
	}
	cookie, err := r.Cookie("tamizhi_gemini_oauth")
	state := r.URL.Query().Get("state")
	if err != nil || len(state) < 32 || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 {
		apiError(w, ErrForbidden)
		return
	}
	setProviderOAuthCookie(w, service.oauth.RedirectURL, "", -1)
	var raw string
	err = service.store.db.QueryRowContext(r.Context(), service.store.query(`DELETE FROM oauth_states WHERE hash=? AND expires_at>? RETURNING data`), authHash(state), now()).Scan(&raw)
	if err != nil {
		apiError(w, ErrForbidden)
		return
	}
	var wrapped providerOAuthState
	if json.Unmarshal([]byte(raw), &wrapped) != nil {
		apiError(w, ErrForbidden)
		return
	}
	plain, err := service.open(wrapped.Owner, "gemini-oauth-state", wrapped.Ciphertext)
	var pending providerOAuthPending
	if err != nil || json.Unmarshal([]byte(plain), &pending) != nil || pending.Owner != wrapped.Owner {
		apiError(w, ErrForbidden)
		return
	}
	if a.Options.Authenticate != nil {
		if p, authErr := a.Options.Authenticate.Authenticate(r); authErr == nil && p.ID != pending.Owner {
			apiError(w, ErrForbidden)
			return
		}
	}
	redirect := func(status string) {
		guard := &Auth{options: OIDCOptions{FrontendURL: service.oauth.FrontendURL, ReturnOrigins: a.Options.Origins}}
		target, _ := url.Parse(guard.returnURL(pending.ReturnURL))
		query := target.Query()
		query.Set("tamizhi_oauth", status)
		target.RawQuery = query.Encode()
		http.Redirect(w, r, target.String(), http.StatusFound)
	}
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		redirect("failed")
		return
	}
	lock := service.credentialLock(pending.Owner, "gemini")
	lock.Lock()
	defer lock.Unlock()
	previous, loadErr := service.load(r.Context(), pending.Owner, "gemini")
	if (loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows)) || previous.Revision != pending.Revision {
		redirect("failed")
		return
	}
	config := service.oauth.config()
	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, service.client)
	token, err := config.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(pending.Verifier))
	if err != nil || token.AccessToken == "" || token.RefreshToken == "" || !strings.EqualFold(token.TokenType, "Bearer") {
		redirect("failed")
		return
	}
	definition, _ := service.definition("gemini")
	transport := &providerOAuthTransport{base: service.client.Transport, origin: definition.BaseURL, project: pending.ProjectID, access: func(context.Context) (string, error) { return token.AccessToken, nil }}
	discovery := *service.client
	discovery.Transport = transport
	settings, err := service.discoverWithClient(ctx, definition, "", &discovery)
	if err != nil {
		redirect("failed")
		return
	}
	settings.AuthMethod = "oauth"
	settings.ProjectID = pending.ProjectID
	secret, _ := json.Marshal(token)
	saved := savedProvider{Settings: settings, Revision: newID(), Ciphertext: service.seal(pending.Owner, "gemini", string(secret))}
	rawBytes, _ := json.Marshal(saved)
	err = service.store.SetSetting(ctx, pending.Owner, "credential.gemini", string(rawBytes))
	if err != nil {
		redirect("failed")
		return
	}
	a.Runtime.cancelOwnerRuns(pending.Owner)
	redirect("connected")
}

// Tokens are loaded on each request, so replacement/removal cannot resurrect old grants.
func (s *credentialService) oauthAccess(ctx context.Context, owner, revision string) (string, error) {
	lock := s.credentialLock(owner, "gemini")
	lock.Lock()
	defer lock.Unlock()
	saved, err := s.load(ctx, owner, "gemini")
	if err != nil || saved.Revision != revision || saved.Settings.AuthMethod != "oauth" || !saved.Settings.Connected {
		return "", errors.New("Google connection changed; reconnect in Settings")
	}
	plain, err := s.open(owner, "gemini", saved.Ciphertext)
	if err != nil {
		return "", err
	}
	var old oauth2.Token
	if json.Unmarshal([]byte(plain), &old) != nil {
		return "", errors.New("invalid Google connection")
	}
	if old.Valid() {
		return old.AccessToken, nil
	}
	if s.oauth == nil {
		return "", errors.New("Google account connection is disabled")
	}
	config := s.oauth.config()
	token, err := config.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, s.client), &old).Token()
	if err != nil || token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") {
		return "", errors.New("Google authorization expired; reconnect in Settings")
	}
	if token.RefreshToken == "" {
		token.RefreshToken = old.RefreshToken
	}
	raw, _ := json.Marshal(token)
	saved.Ciphertext = s.seal(owner, "gemini", string(raw))
	encoded, _ := json.Marshal(saved)
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err = s.store.SetSetting(saveCtx, owner, "credential.gemini", string(encoded)); err != nil {
		return "", errors.New("unable to save renewed Google authorization")
	}
	return token.AccessToken, nil
}

type providerOAuthTransport struct {
	base            http.RoundTripper
	origin, project string
	access          func(context.Context) (string, error)
}

func (t *providerOAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	trusted, _ := url.Parse(t.origin)
	if trusted == nil || trusted.Host != req.URL.Host || trusted.Scheme != req.URL.Scheme {
		return nil, errors.New("refusing to forward Google authorization to another host")
	}
	key, err := t.access(req.Context())
	if err != nil {
		return nil, err
	}
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()
	cloned.Header.Del("x-goog-api-key")
	cloned.Header.Set("Authorization", "Bearer "+key)
	cloned.Header.Set("x-goog-user-project", t.project)
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(cloned)
}

func (o *GeminiOAuthOptions) validate() error {
	if o == nil {
		return nil
	}
	callback, err := url.Parse(o.RedirectURL)
	if err != nil || callback.User != nil || callback.RawQuery != "" || callback.Fragment != "" || callback.Path != "/api/tamizhi/providers/gemini/oauth/callback" || callback.Host == "" {
		return errors.New("invalid Gemini OAuth callback URL")
	}
	local := callback.Hostname() == "localhost" || callback.Hostname() == "127.0.0.1" || callback.Hostname() == "::1"
	if callback.Scheme != "https" && !(local && callback.Scheme == "http") {
		return errors.New("Gemini OAuth callback requires HTTPS or loopback HTTP")
	}
	frontend, err := url.Parse(o.FrontendURL)
	if err != nil || frontend.User != nil || frontend.Host == "" || (frontend.Scheme != "https" && frontend.Scheme != "http") || o.ClientID == "" || o.ClientSecret == "" {
		return errors.New("Gemini OAuth requires a client registration and valid frontend URL")
	}
	return nil
}

// Local disconnection always succeeds independently of upstream availability.
func (s *credentialService) revokeGoogle(ctx context.Context, owner string, saved savedProvider) error {
	if saved.Settings.AuthMethod != "oauth" || s.oauth == nil {
		return nil
	}
	plain, err := s.open(owner, "gemini", saved.Ciphertext)
	if err != nil {
		return err
	}
	var token oauth2.Token
	if json.Unmarshal([]byte(plain), &token) != nil {
		return errors.New("invalid Google connection")
	}
	value := token.RefreshToken
	if value == "" {
		value = token.AccessToken
	}
	address := s.oauth.RevokeURL
	if address == "" {
		address = "https://oauth2.googleapis.com/revoke"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, "POST", address, strings.NewReader(url.Values{"token": {value}}.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.client.Do(request)
	if err != nil {
		return errors.New("Google grant revocation failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		return errors.New("Google grant revocation failed")
	}
	return nil
}
