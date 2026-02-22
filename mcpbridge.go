package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPServerConfig describes how to launch an MCP server subprocess.
type MCPServerConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// mcpSession holds a live connection to one MCP server.
type mcpSession struct {
	name    string
	session *mcp.ClientSession
	tools   []*mcp.Tool
}

// MCPBridge manages connections to all configured MCP servers.
// It discovers their tools at startup and dispatches tool calls at runtime.
type MCPBridge struct {
	sessions []mcpSession
	// toolMap maps a tool name directly to the session that owns it.
	toolMap map[string]*mcpSession
}

// NewMCPBridge connects to every configured MCP server and discovers their tools.
func NewMCPBridge(ctx context.Context, servers map[string]MCPServerConfig) (_ *MCPBridge, err error) {
	b := &MCPBridge{
		toolMap: make(map[string]*mcpSession),
	}
	defer func() {
		if err != nil {
			b.Close()
		}
	}()

	for name, cfg := range servers {
		client := mcp.NewClient(&mcp.Implementation{
			Name:    "mcpproxy",
			Version: "v1.0.0",
		}, nil)

		cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
		transport := &mcp.CommandTransport{Command: cmd}

		session, err := client.Connect(ctx, transport, nil)
		if err != nil {
			return nil, fmt.Errorf("connecting to MCP server %q: %w", name, err)
		}

		// Collect all tools (handles pagination).
		var tools []*mcp.Tool
		for tool, err := range session.Tools(ctx, nil) {
			if err != nil {
				return nil, fmt.Errorf("listing tools from %q: %w", name, err)
			}
			tools = append(tools, tool)
		}

		s := mcpSession{
			name:    name,
			session: session,
			tools:   tools,
		}
		b.sessions = append(b.sessions, s)

		log.Printf("MCP server %q: %d tools", name, len(tools))
		for _, t := range tools {
			log.Printf("  - %s: %s", t.Name, t.Description)
			b.toolMap[t.Name] = &b.sessions[len(b.sessions)-1]
		}
	}

	return b, nil
}

// Tools returns MCP tools translated into OpenAI function-calling format.
func (b *MCPBridge) Tools() []Tool {
	var tools []Tool
	for _, s := range b.sessions {
		for _, t := range s.tools {
			tools = append(tools, Tool{
				Type: "function",
				Function: ToolFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			})
		}
	}
	return tools
}

// CallTool dispatches a tool call to the appropriate MCP server.
// argsJSON is the raw JSON arguments string from the OpenAI tool call format.
func (b *MCPBridge) CallTool(ctx context.Context, name, argsJSON string) (string, error) {
	s, ok := b.toolMap[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	// Parse the JSON arguments string into a map.
	var args map[string]any
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("parsing tool arguments: %w", err)
		}
	}

	result, err := s.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return "", fmt.Errorf("calling tool %q on %q: %w", name, s.name, err)
	}

	// Concatenate all text content from the result.
	var parts []string
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}

	text := strings.Join(parts, "\n")
	if result.IsError {
		return text, fmt.Errorf("tool %q returned error: %s", name, text)
	}
	return text, nil
}

// Close shuts down all MCP server connections.
func (b *MCPBridge) Close() {
	for _, s := range b.sessions {
		if err := s.session.Close(); err != nil {
			log.Printf("closing MCP session %q: %v", s.name, err)
		}
	}
}
