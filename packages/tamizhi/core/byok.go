package core

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

type BYOKDefinition struct {
	ID      string
	Name    string
	Kind    string
	BaseURL string
}

var defaultBYOKProviders = []BYOKDefinition{
	{ID: "openai", Name: "OpenAI", Kind: "openai", BaseURL: "https://api.openai.com/v1"},
	{ID: "anthropic", Name: "Anthropic (Claude)", Kind: "anthropic", BaseURL: "https://api.anthropic.com/v1"},
	{ID: "gemini", Name: "Google Gemini", Kind: "gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta"},
	{ID: "deepseek", Name: "DeepSeek", Kind: "openai", BaseURL: "https://api.deepseek.com"},
	{ID: "kimi", Name: "Moonshot (Kimi)", Kind: "openai", BaseURL: "https://api.moonshot.ai/v1"},
	{ID: "xai", Name: "xAI (Grok)", Kind: "openai", BaseURL: "https://api.x.ai/v1"},
}

var credentialFileMu sync.Mutex

type savedProvider struct {
	Revision   string             `json:"revision,omitempty"`
	Settings   c.ProviderSettings `json:"settings"`
	Ciphertext []byte             `json:"ciphertext"`
}
type credentialService struct {
	locks       [64]sync.Mutex
	oauth       *GeminiOAuthOptions
	store       *Store
	aead        cipher.AEAD
	definitions []BYOKDefinition
	client      *http.Client
}

func credentialKey(options RuntimeOptions) ([]byte, error) {
	if len(options.CredentialKey) > 0 {
		if len(options.CredentialKey) != 32 {
			return nil, errors.New("credential encryption key must be 32 bytes")
		}
		return append([]byte{}, options.CredentialKey...), nil
	}
	if value := os.Getenv("TAMIZHI_ENCRYPTION_KEY"); value != "" {
		key, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(key) != 32 {
			return nil, errors.New("TAMIZHI_ENCRYPTION_KEY must encode 32 random bytes as base64")
		}
		return key, nil
	}
	credentialFileMu.Lock()
	defer credentialFileMu.Unlock()
	path := options.CredentialKeyFile
	if path == "" {
		path = ".vezham/tamizhi/credential-key"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("credential key file must be regular and owner-only")
		}
		key, err = os.ReadFile(path)
		if err != nil || len(key) != 32 {
			return nil, errors.New("invalid credential key file")
		}
		return key, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if _, err = file.Write(key); err != nil {
		return nil, err
	}
	return key, file.Sync()
}
func (r *Runtime) credentials() (*credentialService, error) {
	r.credentialOnce.Do(func() {
		if err := r.options.GeminiOAuth.validate(); err != nil {
			r.credentialError = err
			return
		}
		key, err := credentialKey(r.options)
		if err != nil {
			r.credentialError = err
			return
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			r.credentialError = err
			return
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			r.credentialError = err
			return
		}
		definitions := r.options.BYOKProviders
		if definitions == nil {
			definitions = defaultBYOKProviders
		}
		client := r.options.BYOKHTTPClient
		if client == nil {
			client = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		}
		r.credentialStore = &credentialService{oauth: r.options.GeminiOAuth, store: r.store, aead: aead, definitions: definitions, client: client}
	})
	return r.credentialStore, r.credentialError
}
func (s *credentialService) definition(id string) (BYOKDefinition, error) {
	for _, item := range s.definitions {
		if item.ID == id {
			return item, nil
		}
	}
	return BYOKDefinition{}, ErrNotFound
}
func (s *credentialService) load(ctx context.Context, owner, id string) (savedProvider, error) {
	raw, err := s.store.Setting(ctx, owner, "credential."+id)
	if err != nil {
		return savedProvider{}, err
	}
	if raw == "" {
		return savedProvider{}, sql.ErrNoRows
	}
	var item savedProvider
	err = json.Unmarshal([]byte(raw), &item)
	return item, err
}
func (s *credentialService) seal(owner, id, key string) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return s.aead.Seal(nonce, nonce, []byte(key), []byte(owner+"\x00"+id))
}
func (s *credentialService) open(owner, id string, value []byte) (string, error) {
	n := s.aead.NonceSize()
	if len(value) < n {
		return "", errors.New("invalid encrypted credential")
	}
	plain, err := s.aead.Open(nil, value[:n], value[n:], []byte(owner+"\x00"+id))
	if err != nil {
		return "", errors.New("unable to decrypt provider key")
	}
	return string(plain), nil
}
func (s *credentialService) list(ctx context.Context, owner string) []c.ProviderSettings {
	items := []c.ProviderSettings{}
	for _, definition := range s.definitions {
		item := c.ProviderSettings{ID: definition.ID, Name: definition.Name, Models: []c.ModelInfo{}}
		if saved, err := s.load(ctx, owner, definition.ID); err == nil {
			item = saved.Settings
		}
		item.OAuthAvailable = definition.ID == "gemini" && s.oauth != nil
		items = append(items, item)
	}
	return items
}
func (s *credentialService) discover(ctx context.Context, definition BYOKDefinition, key string) (c.ProviderSettings, error) {
	return s.discoverWithClient(ctx, definition, key, s.client)
}
func (s *credentialService) discoverWithClient(ctx context.Context, definition BYOKDefinition, key string, client *http.Client) (c.ProviderSettings, error) {
	result := c.ProviderSettings{ID: definition.ID, Name: definition.Name, Connected: true, Models: []c.ModelInfo{}}
	address := strings.TrimRight(definition.BaseURL, "/") + "/models"
	seen := map[string]bool{}
	for page := 0; page < 5; page++ {
		req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
		if err != nil {
			return result, errors.New("invalid provider configuration")
		}
		switch definition.Kind {
		case "anthropic":
			req.Header.Set("x-api-key", key)
			req.Header.Set("anthropic-version", "2023-06-01")
		case "gemini":
			req.Header.Set("x-goog-api-key", key)
		default:
			req.Header.Set("Authorization", "Bearer "+key)
		}
		response, err := client.Do(req)
		if err != nil {
			return result, errors.New("unable to reach provider")
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			return result, fmt.Errorf("provider rejected the connection (HTTP %d)", response.StatusCode)
		}
		var body struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"display_name"`
			} `json:"data"`
			Models []struct {
				Name        string   `json:"name"`
				DisplayName string   `json:"displayName"`
				Actions     []string `json:"supportedGenerationMethods"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
			HasMore       bool   `json:"has_more"`
			LastID        string `json:"last_id"`
		}
		err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&body)
		response.Body.Close()
		if err != nil {
			return result, errors.New("provider returned an invalid model catalog")
		}
		add := func(id, name string, image bool) {
			if id == "" || seen[id] || len(result.Models) >= 300 {
				return
			}
			seen[id] = true
			if name == "" {
				name = id
			}
			modalities := []string{"text"}
			if image {
				modalities = append(modalities, "image")
			}
			result.Models = append(result.Models, c.ModelInfo{ID: definition.ID + "/" + id, Name: name, Provider: definition.ID, Modalities: modalities, Fast: strings.Contains(id, "mini") || strings.Contains(id, "flash") || strings.Contains(id, "haiku")})
		}
		for _, model := range body.Data {
			lower := strings.ToLower(model.ID)
			if definition.ID == "openai" {
				if strings.Contains(lower, "transcribe") || strings.HasPrefix(lower, "whisper") {
					if result.TranscriptionModel == "" {
						result.TranscriptionModel = model.ID
					}
					continue
				}
				if strings.Contains(lower, "tts") {
					if result.SpeechModel == "" {
						result.SpeechModel = model.ID
					}
					continue
				}
				if strings.Contains(lower, "audio") || strings.Contains(lower, "realtime") || strings.Contains(lower, "image") || strings.Contains(lower, "search") || strings.Contains(lower, "codex") {
					continue
				}
				if !strings.HasPrefix(lower, "gpt-") && !strings.HasPrefix(lower, "chatgpt-") && !strings.HasPrefix(lower, "o1") && !strings.HasPrefix(lower, "o3") && !strings.HasPrefix(lower, "o4") {
					continue
				}
			}
			image := definition.Kind == "anthropic" || definition.ID == "openai" && (strings.HasPrefix(lower, "gpt-4o") || strings.HasPrefix(lower, "gpt-4.1") || strings.HasPrefix(lower, "gpt-5") || strings.HasPrefix(lower, "gpt-6") || strings.HasPrefix(lower, "o3") || strings.HasPrefix(lower, "o4"))
			add(model.ID, model.Name, image)
		}
		for _, model := range body.Models {
			if !contains(model.Actions, "generateContent") || strings.Contains(model.Name, "tts") || strings.Contains(model.Name, "image") || strings.Contains(model.Name, "live") {
				continue
			}
			add(strings.TrimPrefix(model.Name, "models/"), model.DisplayName, true)
		}
		if definition.Kind == "gemini" && body.NextPageToken != "" {
			address = strings.TrimRight(definition.BaseURL, "/") + "/models?pageToken=" + url.QueryEscape(body.NextPageToken)
			continue
		}
		if definition.Kind == "anthropic" && body.HasMore && body.LastID != "" {
			address = strings.TrimRight(definition.BaseURL, "/") + "/models?after_id=" + url.QueryEscape(body.LastID)
			continue
		}
		break
	}
	if len(result.Models) == 0 {
		return result, errors.New("no supported chat models are available for this key")
	}
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].ID < result.Models[j].ID })
	return result, nil
}
func (r *Runtime) ProviderSettings(ctx context.Context, principal c.Principal) ([]c.ProviderSettings, error) {
	if principal.ID == "" {
		return nil, ErrForbidden
	}
	service, err := r.credentials()
	if err != nil {
		return nil, err
	}
	return service.list(ctx, principal.ID), nil
}
func (r *Runtime) SaveProviderKey(ctx context.Context, principal c.Principal, id, key string) (c.ProviderSettings, error) {
	if principal.ID == "" {
		return c.ProviderSettings{}, ErrForbidden
	}
	key = strings.TrimSpace(key)
	if len(key) < 8 || len(key) > 8192 || strings.ContainsAny(key, "\r\n") {
		return c.ProviderSettings{}, errors.New("invalid provider key")
	}
	service, err := r.credentials()
	if err != nil {
		return c.ProviderSettings{}, err
	}
	definition, err := service.definition(id)
	if err != nil {
		return c.ProviderSettings{}, err
	}
	settings, err := service.discover(ctx, definition, key)
	if err != nil {
		return settings, err
	}
	settings.AuthMethod = "api-key"
	settings.OAuthAvailable = id == "gemini" && service.oauth != nil
	lock := service.credentialLock(principal.ID, id)
	lock.Lock()
	defer lock.Unlock()
	raw, _ := json.Marshal(savedProvider{Revision: newID(), Settings: settings, Ciphertext: service.seal(principal.ID, id, key)})
	if err = service.store.SetSetting(ctx, principal.ID, "credential."+id, string(raw)); err != nil {
		return settings, err
	}
	r.cancelOwnerRuns(principal.ID)
	return settings, nil
}
func (r *Runtime) DeleteProviderKey(ctx context.Context, principal c.Principal, id string) error {
	if principal.ID == "" {
		return ErrForbidden
	}
	service, err := r.credentials()
	if err != nil {
		return err
	}
	if _, err = service.definition(id); err != nil {
		return err
	}
	definition, _ := service.definition(id)
	lock := service.credentialLock(principal.ID, id)
	lock.Lock()
	previous, _ := service.load(ctx, principal.ID, id)
	tombstone, _ := json.Marshal(savedProvider{Revision: newID(), Settings: c.ProviderSettings{ID: id, Name: definition.Name, Models: []c.ModelInfo{}}})
	if err = service.store.SetSetting(ctx, principal.ID, "credential."+id, string(tombstone)); err != nil {
		lock.Unlock()
		return err
	}
	r.cancelOwnerRuns(principal.ID)
	if id == "gemini" {
		if err := service.revokeGoogle(ctx, principal.ID, previous); err != nil {
			r.options.Logger.Warn("Google grant revocation failed after local disconnect; review Google account permissions")
		}
	}
	lock.Unlock()
	return nil
}
func (r *Runtime) cancelOwnerRuns(owner string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, run := range r.runs {
		if run.owner == owner {
			run.cancel()
		}
	}
}
func (r *Runtime) ModelsFor(ctx context.Context, principal c.Principal) ([]c.ModelInfo, error) {
	items := []c.ModelInfo{}
	configured, err := r.ProviderSettings(ctx, principal)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, provider := range configured {
		if provider.Connected {
			for _, model := range provider.Models {
				items = append(items, model)
				seen[model.ID] = true
			}
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, entry := range r.providers {
		if provider, ok := entry.provider.(*OpenAIProvider); ok && provider.Options.ResolveSecret == nil && os.Getenv(provider.Options.APIKeyEnv) == "" {
			continue
		}
		for _, model := range entry.provider.Models() {
			if !seen[model.ID] {
				items = append(items, model)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if (items[i].ID == LeoModelID) != (items[j].ID == LeoModelID) {
			return items[i].ID == LeoModelID
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}
func (r *Runtime) resolveFor(ctx context.Context, principal c.Principal, id string, fast bool) (providerEntry, c.ModelInfo, error) {
	if id == LeoModelID {
		return r.resolveProvider(id, fast)
	}
	if (id == "" || id == "Auto") && (r.options.DefaultModel == "" || r.options.DefaultModel == LeoModelID) {
		if entry, model, err := r.resolveProvider(LeoModelID, fast); err == nil {
			return entry, model, nil
		}
	}

	service, err := r.credentials()
	if err != nil {
		return providerEntry{}, c.ModelInfo{}, err
	}
	configured := service.list(ctx, principal.ID)
	if id == "" || id == "Auto" {
		for _, item := range configured {
			if item.Connected && len(item.Models) > 0 {
				id = item.Models[0].ID
				break
			}
		}
	}
	for _, item := range configured {
		if !item.Connected {
			continue
		}
		for _, model := range item.Models {
			if model.ID != id {
				continue
			}
			if fast {
				for _, candidate := range item.Models {
					if candidate.Fast {
						model = candidate
						break
					}
				}
			}
			saved, err := service.load(ctx, principal.ID, item.ID)
			if err != nil {
				return providerEntry{}, model, errors.New("provider key is unavailable")
			}
			if saved.Settings.AuthMethod == "oauth" {
				if service.oauth == nil {
					return providerEntry{}, model, errors.New("Google account connection is disabled")
				}
				definition, _ := service.definition(item.ID)
				executionClient := *service.client
				executionClient.Timeout = 0
				executionClient.Transport = &providerOAuthTransport{base: service.client.Transport, origin: definition.BaseURL, project: saved.Settings.ProjectID, access: func(ctx context.Context) (string, error) {
					return service.oauthAccess(ctx, principal.ID, saved.Revision)
				}}
				provider := &GeminiProvider{Definition: definition, Catalog: item.Models, HTTPClient: &executionClient, OAuth: true}
				return providerEntry{provider: safeBYOKProvider{Provider: provider}, ctx: r.ctx}, model, nil
			}
			key, err := service.open(principal.ID, item.ID, saved.Ciphertext)
			if err != nil {
				return providerEntry{}, model, err
			}
			definition, _ := service.definition(item.ID)
			executionClient := *service.client
			executionClient.Timeout = 0
			var provider Provider
			switch definition.Kind {
			case "anthropic":
				provider = &AnthropicProvider{Definition: definition, Key: key, Catalog: item.Models, HTTPClient: &executionClient}
			case "gemini":
				provider = &GeminiProvider{Definition: definition, Key: key, Catalog: item.Models, HTTPClient: &executionClient}
			default:
				provider = &OpenAIProvider{Options: OpenAIProviderOptions{ID: item.ID, BaseURL: definition.BaseURL, Models: item.Models, TranscriptionModel: item.TranscriptionModel, SpeechModel: item.SpeechModel, HTTPClient: &executionClient, ResolveSecret: func(context.Context) (string, error) { return key, nil }}}
			}
			return providerEntry{provider: safeBYOKProvider{Provider: provider}, ctx: r.ctx}, model, nil
		}
	}
	return r.resolveProvider(id, fast)
}

type safeBYOKProvider struct{ Provider }

func (p safeBYOKProvider) Stream(ctx context.Context, input ProviderInput, emit func(ProviderDelta) error) error {
	if err := p.Provider.Stream(ctx, input, emit); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("provider request failed; review the key and model in Settings")
	}
	return nil
}
func (r *Runtime) audioFor(ctx context.Context, principal c.Principal) AudioProvider {
	service, err := r.credentials()
	if err == nil {
		for _, item := range service.list(ctx, principal.ID) {
			if item.Connected && (item.TranscriptionModel != "" || item.SpeechModel != "") {
				entry, _, err := r.resolveFor(ctx, principal, item.Models[0].ID, false)
				if err == nil {
					if safe, ok := entry.provider.(safeBYOKProvider); ok {
						if provider, ok := safe.Provider.(AudioProvider); ok {
							return provider
						}
					}
				}
			}
		}
	}
	return r.audio()
}

// Search is a separate paid capability; connecting a model key never enables it.
func (r *Runtime) SetProviderSearch(ctx context.Context, principal c.Principal, id string, enabled bool) (c.ProviderSettings, error) {
	if principal.ID == "" {
		return c.ProviderSettings{}, ErrForbidden
	}
	if id != "deepseek" {
		return c.ProviderSettings{}, errors.New("web search is supported only through DeepSeek")
	}
	service, err := r.credentials()
	if err != nil {
		return c.ProviderSettings{}, err
	}
	if _, err = service.definition(id); err != nil {
		return c.ProviderSettings{}, err
	}
	lock := service.credentialLock(principal.ID, id)
	lock.Lock()
	defer lock.Unlock()
	saved, err := service.load(ctx, principal.ID, id)
	if err != nil || !saved.Settings.Connected || len(saved.Ciphertext) == 0 {
		return c.ProviderSettings{}, errors.New("connect your DeepSeek API key before enabling web search")
	}
	saved.Settings.SearchEnabled = enabled
	saved.Revision = newID()
	raw, err := json.Marshal(saved)
	if err != nil {
		return c.ProviderSettings{}, err
	}
	if err = service.store.SetSetting(ctx, principal.ID, "credential."+id, string(raw)); err != nil {
		return c.ProviderSettings{}, err
	}
	r.cancelOwnerRuns(principal.ID)
	return saved.Settings, nil
}
