package core

import (
	"errors"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type localConnection struct {
	origin, code, status string
	expires              time.Time
}

// RequestID is an in-process capability. Display only Origin and Code in the terminal.
type LocalConnectionPrompt struct {
	RequestID, Origin, Code string
	ExpiresAt               time.Time
}

func ValidLocalOrigin(origin string) bool {
	if len(origin) > 2048 {
		return false
	}
	u, err := url.Parse(origin)
	return err == nil && u.User == nil && u.Hostname() != "" && (u.Scheme == "http" || u.Scheme == "https") && origin == u.Scheme+"://"+u.Host
}
func connectionPath(path string) bool {
	return path == "/api/tamizhi/auth/connect" || path == "/api/tamizhi/auth/connect/poll" || path == "/api/tamizhi/auth/connect/cancel"
}

// Only the connection handshake is reachable from an unapproved browser origin.
func (a *LocalAuth) AllowRequestOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if !ValidLocalOrigin(origin) {
		return false
	}
	a.mu.Lock()
	allowed := a.origins[origin]
	a.mu.Unlock()
	if allowed {
		return true
	}
	return (r.Method == "POST" || r.Method == "OPTIONS") && connectionPath(r.URL.Path)
}
func (a *LocalAuth) ConnectionRequests() <-chan LocalConnectionPrompt { return a.requests }
func (a *LocalAuth) ConnectionPending(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	item, ok := a.connections[authHash(id)]
	return ok && item.status == "pending" && time.Now().Before(item.expires)
}

// Persist before permitting this origin or minting any browser credential.
func (a *LocalAuth) DecideConnection(id string, approved bool, save func([]string) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := authHash(id)
	item, ok := a.connections[key]
	if !ok || item.status != "pending" || time.Now().After(item.expires) {
		return ErrNotFound
	}
	if !approved {
		item.status = "rejected"
		a.connections[key] = item
		return nil
	}
	if !a.origins[item.origin] {
		if len(a.origins) >= 32 {
			return ErrConflict
		}
		origins := []string{item.origin}
		for origin := range a.origins {
			origins = append(origins, origin)
		}
		sort.Strings(origins)
		if save == nil {
			return errors.New("approved origins must be persisted before pairing")
		}
		if err := save(origins); err != nil {
			return err
		}
		a.origins[item.origin] = true
	}
	item.status = "approved"
	a.connections[key] = item
	return nil
}
func (a *LocalAuth) startConnection(origin string) (c.CompanionConnection, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for key, item := range a.connections {
		if now.After(item.expires) {
			delete(a.connections, key)
		}
	}
	for key, item := range a.rates {
		if now.Sub(item.start) > time.Minute {
			delete(a.rates, key)
		}
	}
	rate, known := a.rates[origin]
	if rate.start.IsZero() {
		rate.start = now
	}
	if rate.count >= 10 || (!known && len(a.rates) >= 64) || len(a.connections) >= 32 {
		return c.CompanionConnection{}, ErrConflict
	}
	rate.count++
	a.rates[origin] = rate
	if !a.origins[origin] {
		for _, item := range a.connections {
			if item.origin == origin && item.status == "pending" {
				return c.CompanionConnection{}, ErrConflict
			}
		}
	}
	id, err := localSecret()
	if err != nil {
		return c.CompanionConnection{}, err
	}
	item := localConnection{origin: origin, code: strings.ToUpper(id[:6]), status: "pending", expires: now.Add(5 * time.Minute)}
	if a.origins[origin] {
		item.status = "approved"
	}
	a.connections[authHash(id)] = item
	if item.status == "pending" {
		select {
		case a.requests <- LocalConnectionPrompt{RequestID: id, Origin: origin, Code: item.code, ExpiresAt: item.expires}:
		default:
			delete(a.connections, authHash(id))
			return c.CompanionConnection{}, ErrConflict
		}
	}
	return c.CompanionConnection{RequestID: id, Code: item.code, ExpiresAt: item.expires.UTC().Format(time.RFC3339)}, nil
}
func (a *LocalAuth) connectionSessionLocked(origin string) (c.CompanionSession, error) {
	now := time.Now()
	for key, session := range a.sessions {
		if now.After(session.expires) {
			delete(a.sessions, key)
		}
	}
	if len(a.sessions) >= 32 {
		return c.CompanionSession{}, ErrConflict
	}
	token, err := localSecret()
	if err != nil {
		return c.CompanionSession{}, err
	}
	expires := now.Add(8 * time.Hour)
	a.sessions[authHash(token)] = localSession{origin, expires}
	return c.CompanionSession{Token: token, ExpiresAt: expires.UTC().Format(time.RFC3339)}, nil
}
func (a *LocalAuth) serveConnection(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if r.Method != "POST" || !ValidLocalOrigin(origin) {
		apiError(w, ErrForbidden)
		return
	}
	if r.URL.Path == "/api/tamizhi/auth/connect" {
		var body struct{}
		if !decode(w, r, &body) {
			return
		}
		item, err := a.startConnection(origin)
		if err != nil {
			apiError(w, err)
			return
		}
		respond(w, 200, item)
		return
	}
	var input c.CompanionConnectionRequest
	if !decode(w, r, &input) {
		return
	}
	if len(input.RequestID) < 32 || len(input.RequestID) > 128 {
		apiError(w, ErrForbidden)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := authHash(input.RequestID)
	item, ok := a.connections[key]
	if !ok || item.origin != origin || time.Now().After(item.expires) {
		apiError(w, ErrNotFound)
		return
	}
	if r.URL.Path == "/api/tamizhi/auth/connect/cancel" {
		delete(a.connections, key)
		w.WriteHeader(204)
		return
	}
	result := c.CompanionConnectionResult{Status: item.status}
	if item.status == "approved" {
		session, err := a.connectionSessionLocked(origin)
		if err != nil {
			apiError(w, err)
			return
		}
		result.Session = &session
	}
	if item.status != "pending" {
		delete(a.connections, key)
	}
	respond(w, 200, result)
}
