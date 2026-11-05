package core

import (
	"context"
	"encoding/json"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLocalSessionsAreOriginBoundAndRevocable(t *testing.T) {
	for _, operation := range []string{"logout", "expire"} {
		t.Run(operation, func(t *testing.T) {
			admin := strings.Repeat("admin", 8)
			origin := "https://tamizhi.example"
			auth, err := NewLocalAuth(admin, []string{origin, "https://other.example"})
			if err != nil {
				t.Fatal(err)
			}
			connection := startTestConnection(t, auth, origin)
			response := connectionRequest(t, auth, origin, "/poll", c.CompanionConnectionRequest{RequestID: connection.RequestID})
			var result c.CompanionConnectionResult
			if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Session == nil {
				t.Fatal(response.Body.String(), err)
			}
			token := result.Session.Token
			request := httptest.NewRequest("GET", "http://127.0.0.1/api/tamizhi/session", nil)
			request.Header.Set("Origin", origin)
			request.Header.Set("Authorization", "Bearer "+token)
			if _, err = auth.Authenticate(request); err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", "https://other.example")
			if _, err = auth.Authenticate(request); err == nil {
				t.Fatal("session crossed app origins")
			}
			request.Header.Set("Authorization", "Bearer "+admin)
			if _, err = auth.Authenticate(request); err == nil {
				t.Fatal("browser used CLI admin secret")
			}
			request.Header.Del("Origin")
			if _, err = auth.Authenticate(request); err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", origin)
			request.Header.Set("Authorization", "Bearer "+token)
			if operation == "logout" {
				logout := request.Clone(context.Background())
				logout.Method = http.MethodPost
				logout.URL.Path = "/api/tamizhi/auth/logout"
				response := httptest.NewRecorder()
				auth.ServeHTTP(response, logout)
				if response.Code != http.StatusNoContent {
					t.Fatal(response.Code, response.Body.String())
				}
			} else {
				entry := auth.sessions[authHash(token)]
				entry.expires = time.Now().Add(-time.Second)
				auth.sessions[authHash(token)] = entry
			}
			if _, err = auth.Authenticate(request); err == nil {
				t.Fatal("inactive session still works")
			}
		})
	}
}

func TestLocalAuthenticationHonorsExistingAPICORS(t *testing.T) {
	runtime, _ := fixture(t, fixtureProvider{stream: func(context.Context, ProviderInput, func(ProviderDelta) error) error { return nil }})
	auth, _ := NewLocalAuth(strings.Repeat("a", 40), []string{"https://tamizhi.example"})
	api := NewAPI(runtime, HTTPOptions{Origins: []string{"https://tamizhi.example"}, Authenticate: auth, AuthRoutes: auth})
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/tamizhi/auth/logout", nil)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatal(response.Code)
	}
}
