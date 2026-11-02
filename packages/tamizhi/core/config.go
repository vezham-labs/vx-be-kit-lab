package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/fsnotify/fsnotify"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

type ProviderConfig struct {
	ID                 string        `json:"id"`
	BaseURL            string        `json:"baseURL"`
	APIKeyEnv          string        `json:"apiKeyEnv"`
	Models             []c.ModelInfo `json:"models"`
	TranscriptionModel string        `json:"transcriptionModel,omitempty"`
	SpeechModel        string        `json:"speechModel,omitempty"`
}
type TokenConfig struct {
	Env   string   `json:"env"`
	User  string   `json:"user"`
	Name  string   `json:"name"`
	Roles []string `json:"roles,omitempty"`
}
type AuthConfig struct {
	Connections     []LoginConnection `json:"connections,omitempty"`
	Issuer          string            `json:"issuer,omitempty"`
	ClientID        string            `json:"clientId,omitempty"`
	ClientSecretEnv string            `json:"clientSecretEnv,omitempty"`
	RedirectURL     string            `json:"redirectURL,omitempty"`
	FrontendURL     string            `json:"frontendURL,omitempty"`
	SecureCookies   bool              `json:"secureCookies"`
	AdminSubjects   []string          `json:"adminSubjects,omitempty"`
	Tokens          []TokenConfig     `json:"tokens,omitempty"`
}
type GeminiOAuthConfig struct {
	ClientID        string `json:"clientId"`
	ClientSecretEnv string `json:"clientSecretEnv"`
	RedirectURL     string `json:"redirectURL"`
}

type Config struct {
	GeminiOAuth     *GeminiOAuthConfig `json:"geminiOAuth,omitempty"`
	Address         string             `json:"address"`
	DatabaseDriver  string             `json:"databaseDriver"`
	DatabaseURL     string             `json:"databaseURL"`
	DatabaseURLEnv  string             `json:"databaseURLEnv,omitempty"`
	Origins         []string           `json:"origins"`
	DefaultModel    string             `json:"defaultModel"`
	SystemPrompt    string             `json:"systemPrompt"`
	AllowFullAccess bool               `json:"allowFullAccess"`
	Providers       []ProviderConfig   `json:"providers"`
	Auth            AuthConfig         `json:"auth"`
	WorkspaceRoots  map[string]string  `json:"workspaceRoots"`
	MCP             []MCPOptions       `json:"mcp"`
	Plugins         map[string]bool    `json:"plugins"`
	Profile         string             `json:"profile"`
}

func ReadConfig(path string) (Config, error) {
	var config Config
	data, err := os.ReadFile(path)
	if err != nil {
		return config, err
	}
	if len(data) > 1<<20 {
		return config, errors.New("config exceeds limit")
	}
	if err = json.Unmarshal(data, &config); err != nil {
		return config, err
	}
	if config.Address == "" {
		config.Address = ":8080"
	}
	if config.DatabaseDriver == "" {
		config.DatabaseDriver = "pgx"
	}
	if config.DatabaseURL == "" && config.DatabaseURLEnv == "" {
		if config.DatabaseDriver == "sqlite" {
			config.DatabaseURL = ".vezham/tamizhi/data/tamizhi.sqlite"
		} else {
			config.DatabaseURLEnv = "DATABASE_URL"
		}
	}
	if config.Profile == "" {
		config.Profile = "assistant"
	}
	if config.Profile != "assistant" && config.Profile != "workspace" {
		return config, errors.New("unknown profile")
	}
	return config, nil
}
func (config Config) NewAuth(ctx context.Context, store *Store) (*Auth, error) {
	if config.Auth.ClientSecretEnv != "" && os.Getenv(config.Auth.ClientSecretEnv) == "" {
		return nil, fmt.Errorf("missing OIDC secret: %s", config.Auth.ClientSecretEnv)
	}
	tokens := []TokenCredential{}
	for _, entry := range config.Auth.Tokens {
		token := os.Getenv(entry.Env)
		if token == "" {
			return nil, fmt.Errorf("missing authentication secret: %s", entry.Env)
		}
		tokens = append(tokens, TokenCredential{Token: token, Principal: c.Principal{ID: entry.User, Name: entry.Name, Roles: entry.Roles}})
	}
	return NewAuth(ctx, store, tokens, OIDCOptions{Connections: config.Auth.Connections, Issuer: config.Auth.Issuer, ClientID: config.Auth.ClientID, ClientSecret: os.Getenv(config.Auth.ClientSecretEnv), RedirectURL: config.Auth.RedirectURL, FrontendURL: config.Auth.FrontendURL, ReturnOrigins: config.Origins, SecureCookies: config.Auth.SecureCookies, AdminSubjects: config.Auth.AdminSubjects})
}
func (config Config) Mount(runtime *Runtime) error {
	definitions := []PluginDefinition{MemoryPlugin(), NavigationPlugin(), SubagentPlugin(), HTTPFetchPlugin()}
	if config.Profile == "workspace" && len(config.WorkspaceRoots) > 0 {
		definitions = append(definitions, WorkspacePlugin(config.WorkspaceRoots))
	}
	for _, entry := range config.Providers {
		if entry.ID == "" || len(entry.Models) == 0 {
			return errors.New("provider needs ID and models")
		}
		for i := range entry.Models {
			entry.Models[i].Provider = entry.ID
		}
		definitions = append(definitions, ProviderPlugin(entry.ID, &OpenAIProvider{Options: OpenAIProviderOptions{ID: entry.ID, BaseURL: entry.BaseURL, APIKeyEnv: entry.APIKeyEnv, Models: entry.Models, TranscriptionModel: entry.TranscriptionModel, SpeechModel: entry.SpeechModel}}))
	}
	for _, entry := range config.MCP {
		definitions = append(definitions, MCPPlugin(entry))
	}
	for _, definition := range definitions {
		enabled := true
		if configured, ok := config.Plugins[definition.Name]; ok {
			enabled = configured
		}
		if persisted, err := runtime.store.Setting(runtime.ctx, "system", "plugin."+definition.Name); err == nil {
			if value, err := strconv.ParseBool(persisted); err == nil {
				enabled = value
			}
		}
		if err := runtime.Mount(definition, enabled); err != nil {
			return err
		}
	}
	return nil
}

// WatchPluginConfig hot-reloads enablement for compiled plugins only. Provider,
// authentication and storage changes require a controlled process restart.
func WatchPluginConfig(ctx context.Context, path string, runtime *Runtime) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		watcher.Close()
		return err
	}
	if err = watcher.Add(filepath.Dir(absolute)); err != nil {
		watcher.Close()
		return err
	}
	runtime.wg.Add(1)
	go func() {
		defer runtime.wg.Done()
		defer watcher.Close()
		timer := time.NewTimer(time.Hour)
		if !timer.Stop() {
			<-timer.C
		}
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-runtime.ctx.Done():
				return
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if filepath.Clean(event.Name) == absolute {
					timer.Reset(150 * time.Millisecond)
				}
			case <-timer.C:
				config, err := ReadConfig(absolute)
				if err != nil {
					runtime.options.Logger.Error("config reload rejected", "error", err)
					continue
				}
				for name, enabled := range config.Plugins {
					if err := runtime.SetPlugin(name, enabled); err != nil {
						runtime.options.Logger.Error("plugin reload rejected", "plugin", name, "error", err)
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				runtime.options.Logger.Error("config watch failed", "error", err)
			}
		}
	}()
	return nil
}

// DatabaseDSN resolves server-only database credentials at startup. Never fall
// back to a local database when the configured environment variable is missing.
func (config Config) DatabaseDSN() (string, error) {
	if config.DatabaseURLEnv != "" {
		value := os.Getenv(config.DatabaseURLEnv)
		if value == "" {
			return "", fmt.Errorf("missing database environment variable: %s", config.DatabaseURLEnv)
		}
		return value, nil
	}
	if config.DatabaseURL != "" {
		return config.DatabaseURL, nil
	}
	return "", errors.New("database URL is not configured")
}

func (config Config) GeminiOAuthOptions() (*GeminiOAuthOptions, error) {
	if config.GeminiOAuth == nil {
		return nil, nil
	}
	o := config.GeminiOAuth
	secret := os.Getenv(o.ClientSecretEnv)
	if o.ClientID == "" || secret == "" || o.RedirectURL == "" || config.Auth.FrontendURL == "" {
		return nil, errors.New("Gemini OAuth requires client ID, client-secret environment variable, callback URL and auth frontendURL")
	}
	options := &GeminiOAuthOptions{ClientID: o.ClientID, ClientSecret: secret, RedirectURL: o.RedirectURL, FrontendURL: config.Auth.FrontendURL}
	return options, options.validate()
}
