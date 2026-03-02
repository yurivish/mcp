// app.go is the user-facing HTTP layer: the tool index page, the form
// submission endpoint, and the SSE event stream for live reload.
//
// App combines a HostServer (which owns tool calls and views) with a
// PubSub (which delivers reload signals from the binary watcher).
// It serves the single-page tool index and delegates MCP protocol
// routes (/view/*) back to the HostServer.
package host

import (
	"embed"
	"encoding/json"
	"log"
	"net/http"

	"github.com/starfederation/datastar-go/datastar"
	"github.com/yurivish/mcp/internal/host/templates"
	"github.com/yurivish/toolkit/pubsub"
	"github.com/yurivish/toolkit/req"
	"github.com/yurivish/toolkit/syncmap"
)

//go:embed static/*
var appStaticFS embed.FS

// ── App struct and constructor ──

type App struct {
	hs      *HostServer
	ps      *pubsub.PubSub
	results syncmap.Map[string, *templates.ToolResult]
}

func NewApp(hs *HostServer, ps *pubsub.PubSub) *App {
	return &App{hs: hs, ps: ps}
}

// ── Route table ──

func (a *App) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	// User-facing routes (owned by App)
	mux.HandleFunc("GET /{$}", a.handleIndex)
	mux.HandleFunc("POST /call", a.handleCall)
	mux.HandleFunc("GET /events", a.handleEvents)
	mux.Handle("GET /static/", req.StaticHandler("static", "/static/", appStaticFS))
	mux.HandleFunc("GET /hot-reload", req.HotReloadHandler)
	// MCP protocol routes (delegated to HostServer)
	mux.HandleFunc("GET /view/{id}", a.hs.handleViewPage)
	mux.HandleFunc("GET /view/{id}/sse", a.hs.handleViewSSE)
	mux.HandleFunc("POST /view/{id}/rpc", a.hs.handleViewRPC)
	return mux
}

// ── Handlers ──

// handleIndex renders the tool index page with a form per tool.
func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	templates.Index(a.hs.tools(), &a.results).Render(r.Context(), w)
}

// handleEvents is an SSE endpoint that pushes page updates:
// - "reload": full page reload when the MCP server restarts
// - "rerender": re-render the page body with updated tool results
func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	reloadCh := pubsub.SubChan[struct{}](a.ps, r.Context(), "reload", 1)
	rerenderCh := pubsub.SubChan[struct{}](a.ps, r.Context(), "rerender", 8)
	for {
		select {
		case _, ok := <-reloadCh:
			if !ok {
				return
			}
			sse.ExecuteScript("window.location.reload()")
			return
		case _, ok := <-rerenderCh:
			if !ok {
				return
			}
			sse.PatchElementTempl(templates.IndexBody(a.hs.tools(), &a.results))
		}
	}
}

// handleCall receives a POST with tool name + JSON args as query params,
// invokes the tool, stores the result in app-level state, and publishes
// a rerender event so the SSE stream pushes an updated page body.
func (a *App) handleCall(w http.ResponseWriter, r *http.Request) {
	toolName := r.URL.Query().Get("tool")
	argsStr := r.URL.Query().Get("arguments")
	if argsStr == "" {
		argsStr = "{}"
	}

	var args json.RawMessage
	if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
		a.results.Store(toolName, &templates.ToolResult{Arguments: args, Error: "invalid JSON: " + err.Error()})
		pubsub.Pub(a.ps, "rerender", struct{}{})
		datastar.NewSSE(w, r)
		return
	}

	res, err := a.hs.CreateToolCall(toolName, args)
	if err != nil {
		a.results.Store(toolName, &templates.ToolResult{Arguments: args, Error: err.Error()})
		pubsub.Pub(a.ps, "rerender", struct{}{})
		datastar.NewSSE(w, r)
		return
	}

	a.results.Store(toolName, &templates.ToolResult{Arguments: args, ViewURL: res.ViewURL, Result: res.Result})
	pubsub.Pub(a.ps, "rerender", struct{}{})
	datastar.NewSSE(w, r)
}

// refreshResults re-executes all stored tool calls against the current MCP
// session, replacing stale results. Called after an MCP server restart.
func (a *App) refreshResults() {
	a.results.Range(func(toolName string, prev *templates.ToolResult) bool {
		log.Printf("Refreshing tool call: %s", toolName)
		res, err := a.hs.CreateToolCall(toolName, prev.Arguments)
		if err != nil {
			a.results.Store(toolName, &templates.ToolResult{Arguments: prev.Arguments, Error: err.Error()})
		} else {
			a.results.Store(toolName, &templates.ToolResult{Arguments: prev.Arguments, ViewURL: res.ViewURL, Result: res.Result})
		}
		return true
	})
}
