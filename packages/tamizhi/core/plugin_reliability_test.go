package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	cordis "github.com/tr1v3r/cordis-go"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

func findPlugin(t *testing.T, runtime *Runtime, name string) c.PluginInfo {
	t.Helper()
	for _, item := range runtime.Plugins() {
		if item.Name == name {
			return item
		}
	}
	t.Fatalf("missing plugin %s", name)
	return c.PluginInfo{}
}

func TestOptionalMCPFailureDoesNotBlockStartup(t *testing.T) {
	for _, required := range []bool{false, true} {
		t.Run(map[bool]string{false: "optional", true: "required"}[required], func(t *testing.T) {
			runtime, _ := fixture(t, idleProvider())
			config := Config{MCP: []MCPOptions{{Name: "offline", Command: filepath.Join(t.TempDir(), "missing-server"), AllowTools: []string{"echo"}, Required: required}}}
			err := config.Mount(runtime)
			if (err != nil) != required {
				t.Fatalf("startup error: %v", err)
			}
			item := findPlugin(t, runtime, "mcp.offline")
			if item.Status != "failed" || !item.Enabled || item.Required != required || item.Error == "" {
				t.Fatalf("failure status: %+v", item)
			}
			if !required && findPlugin(t, runtime, "http").Status != "active" {
				t.Fatal("healthy plugin did not remain active")
			}
		})
	}
}

func TestPluginSavedIntentFailureAndExplicitRetry(t *testing.T) {
	runtime, store := fixture(t, idleProvider())
	attempts := 0
	definition := PluginDefinition{Name: "optional.retry", Kind: "test", Optional: true, Apply: func(ctx *cordis.Context, r *Runtime) error {
		attempts++
		if attempts == 1 {
			return errors.New("private activation detail")
		}
		return nil
	}}
	if err := runtime.Mount(definition, false); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ConfigurePlugin(context.Background(), definition.Name, true); err != nil {
		t.Fatal(err)
	}
	value, err := store.Setting(context.Background(), "system", "plugin."+definition.Name)
	if err != nil || value != "true" {
		t.Fatalf("saved intent %q %v", value, err)
	}
	item := findPlugin(t, runtime, definition.Name)
	if !item.Enabled || item.Status != "failed" || item.Error == "private activation detail" {
		t.Fatalf("bad failure status: %+v", item)
	}
	if attempts != 1 {
		t.Fatal("activation was retried automatically")
	}
	if err := runtime.ConfigurePlugin(context.Background(), definition.Name, true); err != nil {
		t.Fatal(err)
	}
	item = findPlugin(t, runtime, definition.Name)
	if item.Status != "active" || item.Error != "" || attempts != 2 {
		t.Fatalf("retry failed: %+v %d", item, attempts)
	}
	if err := runtime.ConfigurePlugin(context.Background(), definition.Name, false); err != nil {
		t.Fatal(err)
	}
	if findPlugin(t, runtime, definition.Name).Status != "disabled" {
		t.Fatal("disable failed")
	}
}

func TestPluginPersistenceFailureDoesNotChangeRuntime(t *testing.T) {
	runtime, store := fixture(t, idleProvider())
	if _, err := store.db.Exec(`CREATE TRIGGER reject_plugin_setting BEFORE INSERT ON settings BEGIN SELECT RAISE(ABORT, 'disk failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ConfigurePlugin(context.Background(), "provider.test", false); err == nil {
		t.Fatal("write failure ignored")
	}
	if item := findPlugin(t, runtime, "provider.test"); !item.Enabled || item.Status != "active" {
		t.Fatalf("runtime changed after failed persistence: %+v", item)
	}
}

func TestPluginAPIPersistenceFailureIsUnconfirmedAndDoesNotMutate(t *testing.T) {
	runtime, store := fixture(t, idleProvider())
	if _, err := store.db.Exec(`CREATE TRIGGER reject_plugin_setting BEFORE INSERT ON settings BEGIN SELECT RAISE(ABORT, 'private database detail'); END`); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(runtime, HTTPOptions{Authenticate: AuthFunc(func(*http.Request) (c.Principal, error) {
		return c.Principal{ID: "admin", Roles: []string{"admin"}}, nil
	})})
	request := httptest.NewRequest(http.MethodPatch, "/api/tamizhi/plugins/provider.test", strings.NewReader(`{"enabled":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status %d: %s", response.Code, response.Body.String())
	}
	var failure c.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "setting_unconfirmed" || strings.Contains(failure.Message, "private database detail") {
		t.Fatalf("invalid error: %+v", failure)
	}
	if !findPlugin(t, runtime, "provider.test").Enabled {
		t.Fatal("runtime changed after failed save")
	}
}
