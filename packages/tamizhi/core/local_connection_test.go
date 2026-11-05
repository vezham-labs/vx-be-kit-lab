package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func connectionRequest(t *testing.T, h http.Handler, origin, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1/api/tamizhi/auth/connect"+path, bytes.NewReader(raw))
	r.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func startTestConnection(t *testing.T, h http.Handler, origin string) c.CompanionConnection {
	t.Helper()
	w := connectionRequest(t, h, origin, "", struct{}{})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var item c.CompanionConnection
	if err := json.Unmarshal(w.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	return item
}
func TestConnectionRequiresPersistedTerminalApproval(t *testing.T) {
	auth, _ := NewLocalAuth(strings.Repeat("a", 40), nil)
	origin := "https://embed.example"
	item := startTestConnection(t, auth, origin)
	prompt := <-auth.ConnectionRequests()
	if prompt.Origin != origin || prompt.Code != item.Code || prompt.RequestID != item.RequestID {
		t.Fatal("terminal prompt does not match browser")
	}
	body := c.CompanionConnectionRequest{RequestID: item.RequestID}
	if w := connectionRequest(t, auth, "https://other.example", "/poll", body); w.Code == 200 {
		t.Fatal("another origin read the request")
	}
	if w := connectionRequest(t, auth, origin, "/poll", body); strings.Contains(w.Body.String(), "token") || !strings.Contains(w.Body.String(), "pending") {
		t.Fatal(w.Body.String())
	}
	if err := auth.DecideConnection(item.RequestID, true, func([]string) error { return errors.New("disk failure") }); err == nil {
		t.Fatal("persistence failure was ignored")
	}
	probe := httptest.NewRequest("GET", "http://127.0.0.1/api/tamizhi/models", nil)
	probe.Header.Set("Origin", origin)
	if auth.AllowRequestOrigin(probe) {
		t.Fatal("invalid request unexpectedly trusted")
	}
	var saved []string
	if err := auth.DecideConnection(item.RequestID, true, func(origins []string) error { saved = origins; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0] != origin {
		t.Fatal(saved)
	}
	w := connectionRequest(t, auth, origin, "/poll", body)
	var result c.CompanionConnectionResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Status != "approved" || result.Session == nil {
		t.Fatal(w.Body.String(), err)
	}
	if w := connectionRequest(t, auth, origin, "/poll", body); w.Code == 200 {
		t.Fatal("session retrieval replayed")
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1/api/tamizhi/models", nil)
	r.Header.Set("Origin", origin)
	r.Header.Set("Authorization", "Bearer "+result.Session.Token)
	if _, err := auth.Authenticate(r); err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Origin", "https://other.example")
	if _, err := auth.Authenticate(r); err == nil {
		t.Fatal("session crossed origins")
	}
	next := startTestConnection(t, auth, origin)
	if w := connectionRequest(t, auth, origin, "/poll", c.CompanionConnectionRequest{RequestID: next.RequestID}); !strings.Contains(w.Body.String(), "approved") {
		t.Fatal(w.Body.String())
	}
	select {
	case <-auth.ConnectionRequests():
		t.Fatal("saved origin prompted again")
	default:
	}
}
func TestConnectionRejectCancelExpireAndLimit(t *testing.T) {
	for _, operation := range []string{"reject", "cancel", "expire"} {
		t.Run(operation, func(t *testing.T) {
			auth, _ := NewLocalAuth(strings.Repeat("a", 40), nil)
			origin := "https://embed.example"
			item := startTestConnection(t, auth, origin)
			body := c.CompanionConnectionRequest{RequestID: item.RequestID}
			switch operation {
			case "reject":
				if err := auth.DecideConnection(item.RequestID, false, nil); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				if w := connectionRequest(t, auth, origin, "/cancel", body); w.Code != 204 {
					t.Fatal(w.Code)
				}
			case "expire":
				p := auth.connections[authHash(item.RequestID)]
				p.expires = time.Now().Add(-time.Second)
				auth.connections[authHash(item.RequestID)] = p
			}
			w := connectionRequest(t, auth, origin, "/poll", body)
			if strings.Contains(w.Body.String(), "token") || strings.Contains(w.Body.String(), "approved") {
				t.Fatal(w.Body.String())
			}
			if auth.DecideConnection(item.RequestID, true, func([]string) error { t.Fatal("stale request persisted trust"); return nil }) == nil {
				t.Fatal("stale request approved")
			}
		})
	}
	auth, _ := NewLocalAuth(strings.Repeat("a", 40), nil)
	startTestConnection(t, auth, "https://embed.example")
	for i := 0; i < 100; i++ {
		connectionRequest(t, auth, "https://embed.example", "", struct{}{})
	}
	if len(auth.connections) != 1 || len(auth.requests) != 1 {
		t.Fatal("duplicate requests flooded terminal")
	}
	for _, origin := range []string{"", "null", "https://embed.example/path", "https://user@embed.example", "https://embed.example#"} {
		if w := connectionRequest(t, auth, origin, "", struct{}{}); w.Code == 200 {
			t.Fatal("invalid origin accepted", origin)
		}
	}
	if w := connectionRequest(t, auth, "https://new.example", "", map[string]string{"origin": "https://trusted.example"}); w.Code == 200 {
		t.Fatal("body origin spoof accepted")
	}
}
func TestUnknownOriginCanOnlyAccessConnectionBootstrap(t *testing.T) {
	runtime, _ := fixture(t, fixtureProvider{stream: func(context.Context, ProviderInput, func(ProviderDelta) error) error { return nil }})
	auth, _ := NewLocalAuth(strings.Repeat("a", 40), nil)
	api := NewAPI(runtime, HTTPOptions{OriginAllowed: auth.AllowRequestOrigin, Authenticate: auth, AuthRoutes: auth})
	origin := "https://embed.example"
	for _, path := range []string{"/api/tamizhi/models", "/api/tamizhi/conversations", "/api/tamizhi/auth/session"} {
		r := httptest.NewRequest("GET", "http://127.0.0.1"+path, nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(path, w.Code)
		}
	}
	r := httptest.NewRequest("OPTIONS", "http://127.0.0.1/api/tamizhi/auth/connect", nil)
	r.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatal(w.Code, w.Header())
	}
	item := startTestConnection(t, api, origin)
	if err := auth.DecideConnection(item.RequestID, true, func([]string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	w = connectionRequest(t, api, origin, "/poll", c.CompanionConnectionRequest{RequestID: item.RequestID})
	var result c.CompanionConnectionResult
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if result.Session == nil {
		t.Fatal(w.Body.String())
	}
	r = httptest.NewRequest("GET", "http://127.0.0.1/api/tamizhi/session", nil)
	r.Header.Set("Origin", origin)
	r.Header.Set("Authorization", "Bearer "+result.Session.Token)
	w = httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != origin {
		t.Fatal(w.Code, w.Body.String())
	}
}
