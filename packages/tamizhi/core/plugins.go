package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
	cordis "github.com/tr1v3r/cordis-go"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

type ProviderDelta struct {
	Text  string
	Calls []c.ToolCall
	Usage c.Usage
}
type ProviderInput struct {
	Model    string
	Messages []c.Message
	Tools    []Tool
	Fast     bool
}
type Provider interface {
	Models() []c.ModelInfo
	Stream(context.Context, ProviderInput, func(ProviderDelta) error) error
}
type AudioProvider interface {
	Transcribe(context.Context, string, string, []byte) (string, error)
	Speech(context.Context, c.SpeechRequest) ([]byte, string, error)
}
type ToolContext struct {
	Principal      c.Principal
	ConversationID string
	RunID          string
	Depth          int
	Permission     c.PermissionMode
	Runtime        *Runtime
}
type Tool struct {
	Name        string
	Description string
	Risk        string
	Schema      json.RawMessage
	Execute     func(context.Context, json.RawMessage, ToolContext) (any, error)
	validator   *jsonschema.Resolved
	lifetime    context.Context
}
type PluginDefinition struct {
	Name     string
	Kind     string
	Requires []string
	Apply    func(*cordis.Context, *Runtime) error
}
type pluginEntry struct {
	definition PluginDefinition
	fiber      *cordis.Fiber
	enabled    bool
}
type providerEntry struct {
	provider Provider
	ctx      context.Context
	cancel   context.CancelFunc
}

func (r *Runtime) Mount(definition PluginDefinition, enabled bool) error {
	if definition.Name == "" || definition.Apply == nil {
		return errors.New("invalid plugin definition")
	}
	r.mu.Lock()
	if _, exists := r.plugins[definition.Name]; exists {
		r.mu.Unlock()
		return fmt.Errorf("duplicate plugin: %s", definition.Name)
	}
	r.plugins[definition.Name] = &pluginEntry{definition: definition}
	r.mu.Unlock()
	if enabled {
		return r.SetPlugin(definition.Name, true)
	}
	return nil
}
func (r *Runtime) SetPlugin(name string, enabled bool) error {
	r.pluginMu.Lock()
	defer r.pluginMu.Unlock()
	r.mu.RLock()
	entry := r.plugins[name]
	closed := r.closed
	r.mu.RUnlock()
	if closed {
		return errors.New("runtime is shutting down")
	}
	if entry == nil {
		return ErrNotFound
	}
	if entry.enabled == enabled {
		return nil
	}
	if !enabled {
		if entry.fiber != nil {
			entry.fiber.Dispose()
		}
		r.mu.Lock()
		entry.enabled = false
		entry.fiber = nil
		r.mu.Unlock()
		return nil
	}
	plugin := cordis.Define[struct{}](name, func(ctx *cordis.Context, _ struct{}) error { return entry.definition.Apply(ctx, r) }).WithInject(entry.definition.Requires...)
	fiber, err := r.root.Load(plugin, struct{}{})
	if err != nil {
		if fiber != nil {
			fiber.Dispose()
		}
		return err
	}
	r.mu.Lock()
	entry.fiber = fiber
	entry.enabled = true
	r.mu.Unlock()
	return nil
}
func (r *Runtime) Plugins() []c.PluginInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := []c.PluginInfo{}
	for _, entry := range r.plugins {
		status := "disabled"
		if entry.fiber != nil {
			status = entry.fiber.State().String()
		}
		requires := entry.definition.Requires
		if requires == nil {
			requires = []string{}
		}
		items = append(items, c.PluginInfo{Name: entry.definition.Name, Kind: entry.definition.Kind, Enabled: entry.enabled, Status: status, Dependencies: requires})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}
func (r *Runtime) RegisterProvider(ctx *cordis.Context, name string, provider Provider) error {
	r.mu.Lock()
	if _, exists := r.providers[name]; exists {
		r.mu.Unlock()
		return errors.New("duplicate provider")
	}
	models := provider.Models()
	seen := map[string]bool{}
	for _, model := range models {
		if model.ID == "" || len(model.ID) > 256 || seen[model.ID] {
			r.mu.Unlock()
			return errors.New("invalid or duplicate model ID")
		}
		seen[model.ID] = true
		for _, existing := range r.providers {
			for _, prior := range existing.provider.Models() {
				if prior.ID == model.ID {
					r.mu.Unlock()
					return errors.New("model IDs must be unique across providers")
				}
			}
		}
	}
	lifetime, cancel := context.WithCancel(r.ctx)
	r.providers[name] = providerEntry{provider: provider, ctx: lifetime, cancel: cancel}
	r.mu.Unlock()
	ctx.OnDispose(func() { cancel(); r.mu.Lock(); delete(r.providers, name); r.mu.Unlock() })
	_, err := ctx.Provide("provider."+name, provider)
	return err
}
func (r *Runtime) RegisterTool(ctx *cordis.Context, tool Tool) error {
	if tool.Name == "" || tool.Execute == nil {
		return errors.New("invalid tool")
	}
	if tool.Risk != "read" && tool.Risk != "write" && tool.Risk != "external" {
		return errors.New("tool needs an explicit risk")
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(tool.Schema, &schema); err != nil {
		return err
	}
	validator, err := schema.Resolve(nil)
	if err != nil {
		return err
	}
	tool.validator = validator
	r.mu.Lock()
	if _, exists := r.tools[tool.Name]; exists {
		r.mu.Unlock()
		return errors.New("duplicate tool")
	}
	lifetime, cancel := context.WithCancel(r.ctx)
	tool.lifetime = lifetime
	r.tools[tool.Name] = tool
	r.mu.Unlock()
	ctx.OnDispose(func() { cancel(); r.mu.Lock(); delete(r.tools, tool.Name); r.mu.Unlock() })
	return nil
}
func (r *Runtime) Models() []c.ModelInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := []c.ModelInfo{}
	for _, entry := range r.providers {
		items = append(items, entry.provider.Models()...)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}
func (r *Runtime) resolveProvider(id string, fast bool) (providerEntry, c.ModelInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if id == "" || id == "Auto" {
		id = r.options.DefaultModel
	}
	for _, entry := range r.providers {
		for _, model := range entry.provider.Models() {
			if model.ID == id || (id == "" && model.ID != "") {
				if fast {
					for _, candidate := range entry.provider.Models() {
						if candidate.Fast {
							return entry, candidate, nil
						}
					}
				}
				return entry, model, nil
			}
		}
	}
	return providerEntry{}, c.ModelInfo{}, errors.New("no provider is configured for the selected model")
}
func (r *Runtime) toolList(depth int) []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := []Tool{}
	for _, tool := range r.tools {
		if tool.Name == "delegate_task" && depth >= r.options.MaxDepth {
			continue
		}
		items = append(items, tool)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}
func ProviderPlugin(name string, provider Provider) PluginDefinition {
	return PluginDefinition{Name: "provider." + name, Kind: "provider", Apply: func(ctx *cordis.Context, r *Runtime) error { return r.RegisterProvider(ctx, name, provider) }}
}
func ToolPlugin(tool Tool) PluginDefinition {
	return PluginDefinition{Name: "tool." + tool.Name, Kind: "tool", Apply: func(ctx *cordis.Context, r *Runtime) error { return r.RegisterTool(ctx, tool) }}
}
