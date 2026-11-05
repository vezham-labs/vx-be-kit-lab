package core

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"net/http"
	"strings"
	"sync"
	"time"
)

type localSession struct {
	origin  string
	expires time.Time
}
type localRate struct {
	count int
	start time.Time
}

// LocalAuth pairs explicitly allowed UI origins. Browser credentials never use the CLI administrator token.
type LocalAuth struct {
	mu          sync.Mutex
	admin       string
	origins     map[string]bool
	sessions    map[string]localSession
	rates       map[string]localRate
	connections map[string]localConnection
	requests    chan LocalConnectionPrompt
}

func localSecret() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}
func NewLocalAuth(admin string, origins []string) (*LocalAuth, error) {
	if len(admin) < 32 || len(origins) > 32 {
		return nil, errors.New("local companion needs a strong administrator token and at most 32 exact UI origins")
	}
	a := &LocalAuth{admin: authHash(admin), origins: map[string]bool{}, sessions: map[string]localSession{}, rates: map[string]localRate{}, connections: map[string]localConnection{}, requests: make(chan LocalConnectionPrompt, 32)}
	for _, origin := range origins {
		if !ValidLocalOrigin(origin) {
			return nil, errors.New("connection origins must be exact HTTP(S) origins without a path")
		}
		a.origins[origin] = true
	}
	return a, nil
}
func localPrincipal() c.Principal {
	return c.Principal{ID: "local", Name: "Local user", Roles: []string{"admin"}}
}
func (a *LocalAuth) Authenticate(r *http.Request) (c.Principal, error) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return c.Principal{}, ErrForbidden
	}
	hash := authHash(strings.TrimPrefix(header, "Bearer "))
	origin := r.Header.Get("Origin")
	if origin == "" && subtle.ConstantTimeCompare([]byte(hash), []byte(a.admin)) == 1 {
		return localPrincipal(), nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[hash]
	if !ok || time.Now().After(session.expires) || origin != session.origin || !a.origins[origin] {
		return c.Principal{}, ErrForbidden
	}
	return localPrincipal(), nil
}
func (a *LocalAuth) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if connectionPath(r.URL.Path) {
		a.serveConnection(w, r)
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/tamizhi/auth/session":
		info := c.AuthSession{Methods: []c.LoginMethod{}}
		if principal, err := a.Authenticate(r); err == nil {
			info.User = &principal
			info.CanLogout = false
		}
		respond(w, 200, info)
	case r.Method == "POST" && r.URL.Path == "/api/tamizhi/auth/logout":
		if _, err := a.Authenticate(r); err != nil {
			apiError(w, ErrForbidden)
			return
		}
		a.mu.Lock()
		delete(a.sessions, authHash(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
		a.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, r)
	}
}
