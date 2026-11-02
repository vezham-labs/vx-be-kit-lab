package core

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	cordis "github.com/tr1v3r/cordis-go"
	c "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/contracts"
)

func objectSchema(properties map[string]any, required ...string) json.RawMessage {
	return c.JSON(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false})
}
func stringSchema(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
func WorkspacePlugin(roots map[string]string) PluginDefinition {
	return PluginDefinition{Name: "workspace", Kind: "tools", Apply: func(ctx *cordis.Context, r *Runtime) error {
		opened := map[string]*os.Root{}
		for owner, path := range roots {
			root, err := os.OpenRoot(path)
			if err != nil {
				for _, prior := range opened {
					_ = prior.Close()
				}
				return err
			}
			opened[owner] = root
		}
		ctx.OnDispose(func() {
			for _, root := range opened {
				_ = root.Close()
			}
		})
		props := map[string]any{"path": stringSchema(512)}
		for _, operation := range []string{"list", "read", "write", "search"} {
			operation := operation
			schemaProps := map[string]any{}
			for key, value := range props {
				schemaProps[key] = value
			}
			required := []string{"path"}
			risk := "read"
			if operation == "write" {
				schemaProps["content"] = stringSchema(100000)
				required = append(required, "content")
				risk = "write"
			}
			if operation == "search" {
				schemaProps["query"] = stringSchema(200)
				required = append(required, "query")
			}
			err := r.RegisterTool(ctx, Tool{Name: "files_" + operation, Description: "Workspace " + operation + " within the authenticated user's configured root", Risk: risk, Schema: objectSchema(schemaProps, required...), Execute: func(call context.Context, args json.RawMessage, tool ToolContext) (any, error) {
				root := opened[tool.Principal.ID]
				if root == nil {
					return nil, ErrForbidden
				}
				var input struct {
					Path    string `json:"path"`
					Content string `json:"content"`
					Query   string `json:"query"`
				}
				if err := json.Unmarshal(args, &input); err != nil {
					return nil, err
				}
				if input.Path == "" {
					input.Path = "."
				}
				if !filepath.IsLocal(input.Path) {
					return nil, errors.New("path must stay inside the workspace")
				}
				switch operation {
				case "read":
					file, err := root.Open(input.Path)
					if err != nil {
						return nil, err
					}
					defer file.Close()
					info, err := file.Stat()
					if err != nil || !info.Mode().IsRegular() {
						return nil, errors.New("not a regular file")
					}
					data, err := io.ReadAll(io.LimitReader(file, 100001))
					if err != nil {
						return nil, err
					}
					if len(data) > 100000 {
						return nil, errors.New("file exceeds 100 KB")
					}
					return map[string]string{"path": input.Path, "content": string(data)}, call.Err()
				case "write":
					info, err := root.Stat(input.Path)
					if err == nil && !info.Mode().IsRegular() {
						return nil, errors.New("not a regular file")
					}
					if err != nil && !errors.Is(err, fs.ErrNotExist) {
						return nil, err
					}
					if err = call.Err(); err != nil {
						return nil, err
					}
					file, err := root.OpenFile(input.Path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
					if err != nil {
						return nil, err
					}
					defer file.Close()
					_, err = file.WriteString(input.Content)
					return map[string]string{"path": input.Path, "status": "written"}, err
				case "list":
					file, err := root.Open(input.Path)
					if err != nil {
						return nil, err
					}
					defer file.Close()
					entries, err := file.ReadDir(201)
					if err != nil && err != io.EOF {
						return nil, err
					}
					items := []map[string]any{}
					for i, entry := range entries {
						if i == 200 {
							break
						}
						items = append(items, map[string]any{"name": entry.Name(), "directory": entry.IsDir()})
					}
					return items, call.Err()
				case "search":
					items := []map[string]any{}
					visited := 0
					err := fs.WalkDir(root.FS(), filepath.ToSlash(input.Path), func(path string, entry fs.DirEntry, err error) error {
						if err != nil {
							return nil
						}
						if call.Err() != nil {
							return call.Err()
						}
						visited++
						if visited > 3000 || len(items) >= 30 {
							return fs.SkipAll
						}
						if entry.IsDir() {
							if strings.HasPrefix(entry.Name(), ".") && path != "." {
								return fs.SkipDir
							}
							if entry.Name() == "node_modules" {
								return fs.SkipDir
							}
							return nil
						}
						if entry.Type()&os.ModeSymlink != 0 {
							return nil
						}
						info, err := entry.Info()
						if err != nil || !info.Mode().IsRegular() || info.Size() > 100000 {
							return nil
						}
						file, err := root.Open(path)
						if err != nil {
							return nil
						}
						data, err := io.ReadAll(io.LimitReader(file, 100001))
						file.Close()
						if err == nil && strings.Contains(strings.ToLower(string(data)), strings.ToLower(input.Query)) {
							items = append(items, map[string]any{"path": path})
						}
						return nil
					})
					return items, err
				}
				return nil, errors.New("unsupported operation")
			}})
			if err != nil {
				return err
			}
		}
		return nil
	}}
}
func publicIP(ip netip.Addr) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() || ip.Is4In6() {
		return false
	}
	for _, block := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/23", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return true
}
func publicHTTPClient() *http.Client {
	transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 15 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, errors.New("private network destinations are blocked")
			}
		}
		var last error
		for _, ip := range ips {
			conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		if last == nil {
			last = errors.New("no destination addresses")
		}
		return nil, last
	}}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
			return errors.New("unsupported scheme")
		}
		return nil
	}}
}
func HTTPFetchPlugin() PluginDefinition {
	return PluginDefinition{Name: "http", Kind: "tools", Apply: func(ctx *cordis.Context, r *Runtime) error {
		client := publicHTTPClient()
		ctx.OnDispose(client.CloseIdleConnections)
		return r.RegisterTool(ctx, Tool{Name: "http_fetch", Description: "Read a public HTTP or HTTPS page; redirects and private network addresses are checked", Risk: "external", Schema: objectSchema(map[string]any{"url": stringSchema(2000)}, "url"), Execute: func(call context.Context, args json.RawMessage, _ ToolContext) (any, error) {
			var input struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, err
			}
			address, err := url.Parse(input.URL)
			if err != nil || address.User != nil || (address.Scheme != "https" && address.Scheme != "http") {
				return nil, errors.New("invalid public URL")
			}
			req, err := http.NewRequestWithContext(call, "GET", address.String(), nil)
			if err != nil {
				return nil, err
			}
			response, err := client.Do(req)
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()
			data, err := io.ReadAll(io.LimitReader(response.Body, 100001))
			if err != nil {
				return nil, err
			}
			if len(data) > 100000 {
				data = data[:100000]
			}
			return map[string]any{"url": response.Request.URL.String(), "status": response.StatusCode, "contentType": response.Header.Get("Content-Type"), "content": string(data)}, nil
		}})
	}}
}
func NavigationPlugin() PluginDefinition {
	return ToolPlugin(Tool{Name: "navigate", Description: "Request navigation to a local application path", Risk: "write", Schema: objectSchema(map[string]any{"path": stringSchema(1000)}, "path"), Execute: func(ctx context.Context, args json.RawMessage, tool ToolContext) (any, error) {
		var action c.ClientAction
		if err := json.Unmarshal(args, &action); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(action.Path, "/") || strings.HasPrefix(action.Path, "//") || strings.ContainsAny(action.Path, "\\\r\n") {
			return nil, errors.New("navigation must use a local path")
		}
		action.Name = "navigate"
		err := tool.Runtime.emit(ctx, tool.Principal.ID, c.StreamEvent{Type: "client-action", RunID: tool.RunID, ConversationID: tool.ConversationID, Output: action})
		return action, err
	}})
}
func SubagentPlugin() PluginDefinition {
	return ToolPlugin(Tool{Name: "delegate_task", Description: "Delegate a bounded independent task to a child agent; inherits this user's permission policy", Risk: "read", Schema: objectSchema(map[string]any{"task": stringSchema(10000), "model": stringSchema(256)}, "task"), Execute: func(ctx context.Context, args json.RawMessage, tool ToolContext) (any, error) {
		if tool.Depth >= tool.Runtime.options.MaxDepth {
			return nil, errors.New("subagent depth limit reached")
		}
		var input struct {
			Task  string `json:"task"`
			Model string `json:"model"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return nil, err
		}
		if strings.TrimSpace(input.Task) == "" {
			return nil, errors.New("empty task")
		}
		run, err := tool.Runtime.startRun(tool.Principal, c.RunRequest{ConversationID: newID(), Title: "Subagent: " + input.Task[:min(len(input.Task), 60)], Messages: []c.Message{{Role: "user", Content: input.Task}}, Model: input.Model, Permission: tool.Permission}, tool.RunID, tool.Depth+1, ctx)
		if err != nil {
			return nil, err
		}
		_ = tool.Runtime.emit(ctx, tool.Principal.ID, c.StreamEvent{Type: "subagent", RunID: tool.RunID, ConversationID: tool.ConversationID, Output: run})
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			saved, _, err := tool.Runtime.store.Run(ctx, tool.Principal.ID, run.ID)
			if err != nil {
				return nil, err
			}
			if saved.Status != "running" {
				item, err := tool.Runtime.store.Conversation(ctx, tool.Principal.ID, saved.ConversationID)
				if err != nil {
					return nil, err
				}
				answer := ""
				for _, message := range item.Messages {
					if message.Role == "assistant" {
						answer = message.Content
					}
				}
				return map[string]any{"runId": run.ID, "conversationId": saved.ConversationID, "status": saved.Status, "answer": answer}, nil
			}
			select {
			case <-ctx.Done():
				_ = tool.Runtime.Cancel(tool.Principal, run.ID)
				return nil, ctx.Err()
			case <-ticker.C:
			}
		}
	}})
}
