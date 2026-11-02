package core

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"golang.org/x/oauth2"
)

type TokenCredential struct {
	Token     string
	Principal c.Principal
}
type LoginConnection struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Params map[string]string `json:"params,omitempty"`
}

type OIDCOptions struct {
	Connections   []LoginConnection
	Issuer        string
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	ReturnOrigins []string
	FrontendURL   string
	SecureCookies bool
	AdminSubjects []string
}
type Auth struct {
	store    *Store
	tokens   []TokenCredential
	options  OIDCOptions
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	routes   *http.ServeMux
}
type loginState struct {
	Verifier  string `json:"verifier"`
	Nonce     string `json:"nonce"`
	ReturnURL string `json:"returnURL,omitempty"`
}

func NewAuth(ctx context.Context, store *Store, tokens []TokenCredential, options OIDCOptions) (*Auth, error) {
	a := &Auth{store: store, tokens: tokens, options: options, routes: http.NewServeMux()}
	seen := map[string]bool{}
	for _, method := range options.Connections {
		if method.ID == "" || method.Name == "" || seen[method.ID] {
			return nil, errors.New("login connections require unique IDs and names")
		}
		seen[method.ID] = true
		for key := range method.Params {
			if key != "connection" && key != "kc_idp_hint" && key != "organization" && key != "domain_hint" {
				return nil, errors.New("unsupported identity broker hint")
			}
		}
	}
	for _, token := range tokens {
		if len(token.Token) < 24 || token.Principal.ID == "" {
			return nil, errors.New("API tokens need at least 24 characters and a principal")
		}
	}
	if options.Issuer != "" {
		if options.ClientID == "" || options.RedirectURL == "" || options.FrontendURL == "" {
			return nil, errors.New("OIDC requires client ID, redirect URL and frontend URL")
		}
		ctx = oidc.ClientContext(ctx, &http.Client{Timeout: 15 * time.Second})
		provider, err := oidc.NewProvider(ctx, options.Issuer)
		if err != nil {
			return nil, err
		}
		a.verifier = provider.Verifier(&oidc.Config{ClientID: options.ClientID})
		a.oauth = oauth2.Config{ClientID: options.ClientID, ClientSecret: options.ClientSecret, Endpoint: provider.Endpoint(), RedirectURL: options.RedirectURL, Scopes: []string{oidc.ScopeOpenID, "profile", "email"}}
	}
	if a.verifier == nil && len(tokens) == 0 {
		return nil, errors.New("configure OIDC or an API token; anonymous access is disabled")
	}
	a.routes.HandleFunc("GET /api/tamizhi/auth/session", func(w http.ResponseWriter, r *http.Request) {
		info := c.AuthSession{LoginEnabled: a.verifier != nil, Methods: []c.LoginMethod{}}
		if info.LoginEnabled {
			for _, method := range a.options.Connections {
				info.Methods = append(info.Methods, c.LoginMethod{ID: method.ID, Name: method.Name})
			}
		}
		if p, err := a.Authenticate(r); err == nil {
			info.User = &p
			info.CanLogout = r.Header.Get("Authorization") == ""
		}
		respond(w, http.StatusOK, info)
	})
	a.routes.HandleFunc("GET /api/tamizhi/auth/login", a.login)
	a.routes.HandleFunc("GET /api/tamizhi/auth/callback", a.callback)
	a.routes.HandleFunc("POST /api/tamizhi/auth/logout", a.logout)
	return a, nil
}
func (a *Auth) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.routes.ServeHTTP(w, r) }
func authHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (a *Auth) Authenticate(r *http.Request) (c.Principal, error) {
	if header := r.Header.Get("Authorization"); header != "" {
		if !strings.HasPrefix(header, "Bearer ") {
			return c.Principal{}, ErrForbidden
		}
		value := authHash(strings.TrimPrefix(header, "Bearer "))
		for _, credential := range a.tokens {
			candidate := authHash(credential.Token)
			if subtle.ConstantTimeCompare([]byte(value), []byte(candidate)) == 1 {
				return credential.Principal, nil
			}
		}
		return c.Principal{}, ErrForbidden
	}
	cookie, err := r.Cookie("tamizhi_session")
	if err != nil {
		return c.Principal{}, ErrForbidden
	}
	var raw string
	err = a.store.db.QueryRowContext(r.Context(), a.store.query(`SELECT principal FROM auth_sessions WHERE hash=? AND expires_at>?`), authHash(cookie.Value), now()).Scan(&raw)
	if err != nil {
		return c.Principal{}, ErrForbidden
	}
	var p c.Principal
	if err = json.Unmarshal([]byte(raw), &p); err != nil {
		return p, ErrForbidden
	}
	return p, nil
}
func (a *Auth) setCookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/api/tamizhi", HttpOnly: true, Secure: a.options.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: age})
}
func (a *Auth) login(w http.ResponseWriter, r *http.Request) {
	if a.verifier == nil {
		apiError(w, ErrNotFound)
		return
	}
	authOptions := []oauth2.AuthCodeOption{}
	if selected := r.URL.Query().Get("connection"); selected != "" {
		found := false
		for _, method := range a.options.Connections {
			if method.ID == selected {
				found = true
				for key, value := range method.Params {
					authOptions = append(authOptions, oauth2.SetAuthURLParam(key, value))
				}
				break
			}
		}
		if !found {
			apiError(w, ErrNotFound)
			return
		}
	}
	state := newID() + newID()
	verifier := oauth2.GenerateVerifier()
	nonce := newID()
	raw, _ := json.Marshal(loginState{Verifier: verifier, Nonce: nonce, ReturnURL: a.returnURL(r.URL.Query().Get("returnTo"))})
	// Expired credentials are removed opportunistically; state is single use.
	_, _ = a.store.db.ExecContext(r.Context(), a.store.query(`DELETE FROM oauth_states WHERE expires_at<?`), now())
	_, _ = a.store.db.ExecContext(r.Context(), a.store.query(`DELETE FROM auth_sessions WHERE expires_at<?`), now())
	if _, err := a.store.db.ExecContext(r.Context(), a.store.query(`INSERT INTO oauth_states(hash,data,expires_at) VALUES(?,?,?)`), authHash(state), string(raw), time.Now().Add(5*time.Minute).UnixMilli()); err != nil {
		apiError(w, err)
		return
	}
	a.setCookie(w, "tamizhi_login", state, 300)
	http.Redirect(w, r, a.oauth.AuthCodeURL(state, append(authOptions, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))...), http.StatusFound)
}
func (a *Auth) callback(w http.ResponseWriter, r *http.Request) {
	if a.verifier == nil {
		apiError(w, ErrNotFound)
		return
	}
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie("tamizhi_login")
	if err != nil || len(state) < 32 || subtle.ConstantTimeCompare([]byte(state), []byte(cookie.Value)) != 1 {
		apiError(w, ErrForbidden)
		return
	}
	a.setCookie(w, "tamizhi_login", "", -1)
	var raw string
	err = a.store.db.QueryRowContext(r.Context(), a.store.query(`DELETE FROM oauth_states WHERE hash=? AND expires_at>? RETURNING data`), authHash(state), now()).Scan(&raw)
	if err != nil {
		apiError(w, ErrForbidden)
		return
	}
	var pending loginState
	if json.Unmarshal([]byte(raw), &pending) != nil {
		apiError(w, ErrForbidden)
		return
	}
	token, err := a.oauth.Exchange(oidc.ClientContext(r.Context(), &http.Client{Timeout: 15 * time.Second}), r.URL.Query().Get("code"), oauth2.VerifierOption(pending.Verifier))
	if err != nil {
		apiError(w, ErrForbidden)
		return
	}
	rawID, ok := token.Extra("id_token").(string)
	if !ok {
		apiError(w, ErrForbidden)
		return
	}
	id, err := a.verifier.Verify(r.Context(), rawID)
	if err != nil || id.Nonce != pending.Nonce {
		apiError(w, ErrForbidden)
		return
	}
	var claims struct {
		Name string `json:"name"`
	}
	if id.Claims(&claims) != nil {
		apiError(w, ErrForbidden)
		return
	}
	p := c.Principal{ID: authHash(id.Issuer + "\x00" + id.Subject), Name: claims.Name}
	if contains(a.options.AdminSubjects, id.Subject) {
		p.Roles = []string{"admin"}
	}
	session := newID() + newID()
	data, _ := json.Marshal(p)
	if _, err = a.store.db.ExecContext(r.Context(), a.store.query(`INSERT INTO auth_sessions(hash,principal,expires_at) VALUES(?,?,?)`), authHash(session), string(data), time.Now().Add(12*time.Hour).UnixMilli()); err != nil {
		apiError(w, err)
		return
	}
	a.setCookie(w, "tamizhi_session", session, 43200)
	http.Redirect(w, r, a.returnURL(pending.ReturnURL), http.StatusFound)
}
func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("tamizhi_session"); err == nil {
		if _, err := a.store.db.ExecContext(r.Context(), a.store.query(`DELETE FROM auth_sessions WHERE hash=?`), authHash(cookie.Value)); err != nil {
			apiError(w, err)
			return
		}
	}
	a.setCookie(w, "tamizhi_session", "", -1)
	w.WriteHeader(204)
}

// returnURL permits navigation only to configured frontend origins.
func (a *Auth) returnURL(candidate string) string {
	fallback := a.options.FrontendURL
	target, err := url.Parse(candidate)
	if err != nil || target.User != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return fallback
	}
	origin := target.Scheme + "://" + target.Host
	defaults, err := url.Parse(fallback)
	if err == nil && origin == defaults.Scheme+"://"+defaults.Host {
		return target.String()
	}
	for _, allowed := range a.options.ReturnOrigins {
		if origin == allowed {
			return target.String()
		}
	}
	return fallback
}
