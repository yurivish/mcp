# mcphost

A rapid-iteration development environment for building and testing [MCP](https://modelcontextprotocol.io/) servers with interactive UI capabilities (MCP Apps, per the [SEP-1865](https://github.com/nicolo-ribaudo/model-context-protocol/blob/mcp-apps/docs/specification/draft/server-extensions/mcp-apps/index.md) specification).

Given any MCP server command, mcphost launches it as a subprocess, discovers its tools and resources, and serves a web UI where you can invoke tools and interact with server-provided HTML views — all with proper sandboxing and security enforcement.

```
mcphost go run ./cmd/testserver
```

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     MCP Server (subprocess)                 │
│                     stdin/stdout transport                  │
└──────────────────────────┬──────────────────────────────────┘
                           │ JSON-RPC
┌──────────────────────────┴──────────────────────────────────┐
│                     mcphost (Go binary)                    │
│                                                             │
│  ┌──────────────┐   ┌──────────────┐   ┌────────────────┐  │
│  │  MCP Client   │   │ View Manager │   │  CSP Builder   │  │
│  │  (go-sdk)     │   │  (lifecycle) │   │  (security)    │  │
│  └──────────────┘   └──────────────┘   └────────────────┘  │
│                                                             │
│  Host server (:8080)          Sandbox server (:8081)        │
│  ┌─────────────────────┐      ┌──────────────────────┐     │
│  │ /          Index     │      │ /sandbox/{id}        │     │
│  │ /create    Tool call │      │ CSP-restricted HTML  │     │
│  │ /view/{id} Host page │      └──────────────────────┘     │
│  │   └─ /sse  Events   │                                    │
│  │   └─ /rpc  JSON-RPC │                                    │
│  └─────────────────────┘                                    │
└─────────────────────────────────────────────────────────────┘
                           │
                      Browser UI
┌──────────────────────────┴──────────────────────────────────┐
│  Host page (:8080/view/{id})                                │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  <iframe src=":8081/sandbox/{id}">  ← origin boundary │  │
│  │  ┌──────────────────────────────────────────────────┐  │  │
│  │  │  <iframe sandbox>  ← sandboxed inner iframe      │  │  │
│  │  │  Server-provided HTML + MCP Apps SDK             │  │  │
│  │  └──────────────────────────────────────────────────┘  │  │
│  └────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

## Capabilities

- **Tool discovery**: Lists all tools from the MCP server with their JSON schemas and auto-generated example arguments.
- **Direct tool calls**: Non-UI tools return results inline on the index page.
- **Interactive UI views**: Tools with a `resourceUri` open a sandboxed HTML view that receives tool input/result notifications and can call back into the server.
- **CSP enforcement**: Content Security Policy headers are constructed from server-declared domain metadata, with restrictive defaults (`connect-src 'none'`, `frame-src 'none'`).
- **Visibility control**: Tools declare whether they're callable by the model, the app, or both. The host enforces this when UI views attempt `tools/call`.
- **Teardown lifecycle**: On shutdown, all active views receive a `ui/resource-teardown` request and are given time to clean up.
- **Live reload**: When the server command is a binary on disk, mcphost watches it for changes and automatically restarts the MCP session when the binary is rebuilt. Non-binary commands (e.g. `go run`) skip watching.

## File overview

### Core

| File | Purpose |
|------|---------|
| `main.go` | Entry point. Parses CLI args, manages the MCP session via a channel-owning goroutine, discovers tools, wires up the host server, and watches the server binary for live reload. |
| `host.go` | HTTP servers, view lifecycle, and JSON-RPC message routing between browser and MCP server. The bulk of the host implementation. |
| `csp.go` | Builds `Content-Security-Policy` headers from resource metadata. |

### Web UI (embedded via `go:embed`)

| File | Purpose |
|------|---------|
| `static/index.html` | Tool listing page with forms for invoking each tool. |
| `static/host.html` | Host-side view page. Embeds the sandbox iframe, bridges SSE events and `postMessage` RPC. |
| `static/sandbox.html` | Sandbox proxy. Verifies cross-origin isolation, creates an inner sandboxed iframe, relays messages between host and view. |

### Test server

| File | Purpose |
|------|---------|
| `cmd/testserver/main.go` | Demo MCP server with a UI tool (`demo-ui-tool`), a plain tool (`echo`), and an app-callable tool (`count-words`). Builds the view HTML with an inlined SDK. |
| `cmd/testserver/bundle.go` | Uses esbuild to convert the MCP Apps ESM SDK into an IIFE for use in `document.write()`-loaded iframes. |

### Tests

| File | Purpose |
|------|---------|
| `spec_test.go` | SEP-1865 specification compliance tests (~40KB). Covers CSP construction, visibility enforcement, sandbox isolation, initialization sequencing, tool input/result delivery, and display modes. |
| `host_test.go` | HTTP handler behavior tests — tool calls, view creation, SSE streaming, RPC routing. |
| `csp_test.go` | Unit tests for CSP header generation. |

## Key design decisions

**Dual-port sandboxing.** The host (`:8080`) and sandbox (`:8081`) run on separate ports to establish a cross-origin boundary enforced by the browser. A third layer of isolation comes from the inner `<iframe sandbox="allow-scripts allow-same-origin allow-forms">` which confines the server-provided HTML. This is more complex than a single-origin approach but provides defense-in-depth against untrusted view code.

**Subprocess transport.** The MCP server runs as a child process communicating over stdin/stdout via `CommandTransport`. This keeps the host language-agnostic — any MCP server binary works — but means there's no persistent state between runs.

**Channel-based session ownership.** A single goroutine owns the MCP session and processes all operations (tool calls, resource reads, restarts) sequentially via a channel. This eliminates data races by construction rather than by mutex discipline. The serialization trade-off is irrelevant — the stdio transport is effectively serial anyway.

**Async tool results.** For UI tools, the tool call runs in a background goroutine while the view initializes. If the tool completes before the view sends `initialized`, the result is buffered and delivered once the view is ready. This avoids blocking view setup on potentially slow tool execution, at the cost of some state-management complexity.

**Restrictive default CSP.** When a resource omits CSP metadata, the host applies `connect-src 'none'` and `frame-src 'none'`. Servers must explicitly declare which domains their views need access to. This is intentionally strict — it forces servers to be explicit about network dependencies.

**Embedded assets.** All HTML/CSS is compiled into the binary via `go:embed`, producing a single portable executable with no runtime file dependencies.

## Usage

```sh
# Build
go build -o mcphost .

# Run with any MCP server
./mcphost <server-command> [args...]

# Example with the included test server (no live reload)
./mcphost go run ./cmd/testserver

# With live reload: build the server binary, then run mcphost against it
go build -o testserver ./cmd/testserver
./mcphost ./testserver
# Rebuild in another terminal → mcphost detects the change and restarts automatically

# Then open http://localhost:8080
```

## Message flow for a UI tool call

```
User clicks "Call" on index page
  → POST /create: host fetches UI resource, creates view, starts async tool call
  → Redirect to /view/{id}

Host page loads, creates iframe to :8081/sandbox/{id}
  → Sandbox verifies cross-origin isolation
  → Sandbox sends sandbox-proxy-ready

Host receives sandbox-proxy-ready via SSE
  → Sends sandbox-resource-ready with HTML to sandbox
  → Sandbox creates inner iframe, writes HTML via document.write()

View HTML loads, SDK initializes
  → View sends ui/initialize → host responds with capabilities
  → View sends ui/notifications/initialized
  → Host sends ui/notifications/tool-input with arguments

Tool call completes in background
  → Host sends ui/notifications/tool-result

View can call app-visible tools
  → tools/call → host checks visibility → proxies to MCP server → returns result
```

## Running tests

```sh
go test ./...
```
