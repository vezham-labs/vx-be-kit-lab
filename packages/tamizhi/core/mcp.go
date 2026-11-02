package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	cordis "github.com/tr1v3r/cordis-go"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

type MCPOptions struct {
	Name       string            `json:"name"`
	URL        string            `json:"url,omitempty"`
	Command    string            `json:"command,omitempty"`
	Args       []string          `json:"args,omitempty"`
	Headers    map[string]string `json:"-"`
	HeaderEnv  map[string]string `json:"headerEnv,omitempty"`
	AllowTools []string          `json:"allowTools"`
}
type headerTransport struct {
	base      http.RoundTripper
	headers   map[string]string
	headerEnv map[string]string
}

func (t headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.Header = request.Header.Clone()
	for key, env := range t.headerEnv {
		copy.Header.Set(key, os.Getenv(env))
	}
	for key, value := range t.headers {
		copy.Header.Set(key, value)
	}
	return t.base.RoundTrip(copy)
}
func MCPPlugin(options MCPOptions) PluginDefinition {
	return PluginDefinition{Name: "mcp." + options.Name, Kind: "mcp", Apply: func(ctx *cordis.Context, r *Runtime) error {
		if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,30}$`).MatchString(options.Name) || len(options.AllowTools) == 0 {
			return errors.New("MCP requires a valid name and explicit tool allowlist")
		}
		lifetime, cancel := context.WithCancel(r.ctx)
		ctx.OnDispose(cancel)
		var transport mcp.Transport
		if options.URL != "" && options.Command == "" {
			transport = &mcp.StreamableClientTransport{Endpoint: options.URL, HTTPClient: &http.Client{Transport: headerTransport{base: http.DefaultTransport, headers: options.Headers, headerEnv: options.HeaderEnv}}, MaxEventSize: 1 << 20, MaxRetries: 2}
		} else if options.Command != "" && options.URL == "" {
			transport = &mcp.CommandTransport{Command: exec.CommandContext(lifetime, options.Command, options.Args...), TerminateDuration: 2 * time.Second}
		} else {
			return errors.New("configure exactly one trusted MCP URL or command")
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "tamizhi", Version: "1"}, nil)
		handshake := time.AfterFunc(15*time.Second, cancel)
		session, err := client.Connect(lifetime, transport, nil)
		handshake.Stop()
		if err != nil {
			cancel()
			return err
		}
		ctx.OnDispose(func() { _ = session.Close() })
		discovery, stop := context.WithTimeout(lifetime, 15*time.Second)
		defer stop()
		tools, err := session.ListTools(discovery, nil)
		if err != nil {
			return err
		}
		for _, remote := range tools.Tools {
			if !contains(options.AllowTools, remote.Name) {
				continue
			}
			remote := remote
			name := "mcp_" + options.Name + "_" + remote.Name
			if len(name) > 64 || !regexp.MustCompile(`^[a-zA-Z0-9_-]+$`).MatchString(name) {
				return fmt.Errorf("unsupported MCP tool name %q", remote.Name)
			}
			if err := r.RegisterTool(ctx, Tool{Name: name, Description: remote.Description, Risk: "external", Schema: c.JSON(remote.InputSchema), Execute: func(call context.Context, args json.RawMessage, _ ToolContext) (any, error) {
				var input any
				if err := json.Unmarshal(args, &input); err != nil {
					return nil, err
				}
				result, err := session.CallTool(call, &mcp.CallToolParams{Name: remote.Name, Arguments: input})
				if err != nil {
					return nil, err
				}
				raw, err := json.Marshal(result)
				if err != nil {
					return nil, err
				}
				if len(raw) > 100000 {
					return nil, errors.New("MCP result exceeds budget")
				}
				var value any
				err = json.Unmarshal(raw, &value)
				return value, err
			}}); err != nil {
				return err
			}
		}
		return nil
	}}
}
