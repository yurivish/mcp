# mcpproxy

\_Small proxy tool co-written with AI as a learning exercise. Inspired by [mcphost](https://github.com/mark3labs/mcphost).\_

An HTTP reverse proxy that sits between OpenAI-compatible clients and LLM backends (Ollama, llama.cpp, vLLM, etc.), transparently adding MCP tool-calling support. When the backend returns tool calls, the proxy executes them against MCP server subprocesses in an agentic loop, appending results to the conversation until the LLM produces a final text response.

## File Map

| File           | Lines | Purpose                                                         |
| -------------- | ----: | --------------------------------------------------------------- |
| `main.go`      |   100 | Config loading, wiring, HTTP server with graceful shutdown      |
| `chat.go`      |   171 | `ToolBridge` interface, agentic tool loop, backend forwarding   |
| `mcpbridge.go` |   148 | MCP server lifecycle, tool discovery, OpenAI format translation |
| `types.go`     |    73 | OpenAI-compatible request/response structs                      |
| `proxy.go`     |    28 | Reverse proxy for non-chat endpoints                            |
| `chat_test.go` |   361 | Tests with `fakeBridge` and canned backend responses            |

## Information Flow

```
                         ┌─────────────────────────────────────────┐
                         │              mcpproxy                   │
                         │                                         │
  ┌────────┐  POST /v1/  │  ┌────────────┐     ┌───────────────┐  │  ┌─────────┐
  │ Client │─────────────┼─▸│chatHandler │────▸│forwardToBackend│──┼─▸│ Backend │
  │        │◁────────────┼──│  :23       │◁────│  :99           │◁─┼──│ (LLM)   │
  └────────┘  JSON/SSE   │  └─────┬──────┘     └───────────────┘  │  └─────────┘
                         │        │ tool calls?                    │
                         │        ▼                                │
                         │  ┌────────────┐     ┌───────────────┐  │
                         │  │ToolBridge  │────▸│ MCP Server(s) │  │
                         │  │  :15       │◁────│ (subprocesses)│  │
                         │  └────────────┘     └───────────────┘  │
                         │                                         │
  ┌────────┐  all other  │  ┌────────────┐                        │
  │ Client │─────────────┼─▸│reverseProxy│────────────────────────┼─▸ Backend
  │        │◁────────────┼──│  :12       │◁───────────────────────┼── Backend
  └────────┘  endpoints  │  └────────────┘                        │
                         └─────────────────────────────────────────┘
```

**Three communication boundaries:**

1. **Client <-> Proxy** -- standard OpenAI HTTP API (`main.go:71-73`)
2. **Proxy <-> Backend** -- same API, forwarded via `forwardToBackend` / reverse proxy
3. **Proxy <-> MCP servers** -- stdio subprocesses managed by `MCPBridge`

## Core Types

### Containment tree

```
ChatCompletionRequest          types.go:8
├── Messages []Message         types.go:23
│   ├── ToolCalls []ToolCall   types.go:30
│   │   └── Function           types.go:36   FunctionCall{Name, Arguments}
│   └── ToolCallID string                    (for role:"tool" messages)
└── Tools []Tool               types.go:41
    └── Function               types.go:46   ToolFunction{Name, Description, Parameters}

ChatCompletionResponse         types.go:54
└── Choices []Choice           types.go:63
    └── Message                types.go:23   (same Message type)

Config                         main.go:16
├── BackendURL, ListenAddr
├── MaxToolRounds
└── MCPServers map             main.go:20
    └── MCPServerConfig        mcpbridge.go:15  {Command, Args}

MCPBridge                      mcpbridge.go:29
├── sessions []mcpSession      mcpbridge.go:21
│   ├── session *mcp.ClientSession
│   └── tools []*mcp.Tool
└── toolMap map[string]*mcpSession
```

## The ToolBridge Interface

The single interface that decouples the agentic loop from MCP specifics (`chat.go:15-18`):

```go
type ToolBridge interface {
    Tools() []Tool
    CallTool(ctx context.Context, name, argsJSON string) (string, error)
}
```

**Two implementations:**

| Implementation | File              | Purpose                                            |
| -------------- | ----------------- | -------------------------------------------------- |
| `*MCPBridge`   | `mcpbridge.go:29` | Production -- manages real MCP server subprocesses |
| `*fakeBridge`  | `chat_test.go:17` | Testing -- configurable `callFn` callback          |

`chatHandler` (`chat.go:23`) depends only on `ToolBridge`, never on `*MCPBridge` directly. This is what makes the agentic loop independently testable.

## Runtime Coordination

### Startup sequence (`main.go:41-100`)

1. **Load config** -- `loadConfig` parses JSON with defaults (`main.go:23-39`)
2. **Connect MCP servers** -- `NewMCPBridge` launches each subprocess, discovers tools (`mcpbridge.go:36-82`)
3. **Build HTTP mux** -- `/v1/chat/completions` -> `chatHandler`, everything else -> reverse proxy (`main.go:71-73`)
4. **Listen** -- with SIGINT/SIGTERM graceful shutdown (`main.go:80-93`)

### Agentic tool loop (`chat.go:23-94`)

```
 1. Parse client request                        :27
 2. Remember if client wants streaming           :33
 3. Replace tools with MCP tools                 :36
 4. ┌─ Loop (up to maxRounds)                    :39
 5. │  Force non-streaming                       :41-42
 6. │  Forward to backend                        :44
 7. │  No tool calls? ──▸ return final response  :51-63
 8. │  Append assistant message to conversation   :66-67
 9. │  For each tool call:                        :70
10. │    Execute via bridge.CallTool              :73
11. │    On error, feed error text as result      :74-80
12. │    Append tool result message               :84-88
13. └─ Next round
14. Exhausted? ──▸ 500 "max tool rounds"         :93
```

When the final response arrives and the client originally requested streaming, the proxy re-issues that last round with `stream: true` and pipes SSE chunks directly to the client (`chat.go:52-57`, `streamFromBackend` at `chat.go:125-164`).

## Key Design Decisions

- **Non-streaming tool loop** -- Tool rounds always use `stream: false` for simple JSON parsing; only the final response is optionally streamed back to the client (`chat.go:41-42`, `52-57`).
- **Error-as-result recovery** -- When a tool call fails, the error text is fed back as the tool result so the LLM can recover gracefully instead of the whole request failing (`chat.go:74-80`).
- **Stateless proxy** -- No session state between requests; each request carries its full conversation history. The proxy adds tool interactions within a single request only.
- **Tool replacement** -- Client-provided tools are replaced (not merged) with MCP tools (`chat.go:36`). The proxy owns the tool namespace.
- **Subprocess lifecycle** -- MCP servers are launched once at startup and shared across all requests. `MCPBridge.Close()` terminates them on shutdown (`main.go:62`, `mcpbridge.go:142-148`).
- **Tool dispatch by name** -- `toolMap` provides O(1) lookup from tool name to owning MCP session, avoiding linear scans across servers (`mcpbridge.go:32`, `104-108`).
