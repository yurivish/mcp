package proxy

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"github.com/yurivish/mcp/internal/netutil"
)

// Config holds the proxy configuration loaded from a JSON file.
type Config struct {
	BackendURL    string                     `json:"backend_url"`
	ListenAddr    string                     `json:"listen_addr"`
	MaxToolRounds int                        `json:"max_tool_rounds"`
	MCPServers    map[string]MCPServerConfig `json:"mcpServers"`
}

func loadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cfg := &Config{
		BackendURL:    "http://localhost:11434/v1",
		ListenAddr:    ":8080",
		MaxToolRounds: 10,
	}
	if err := json.NewDecoder(f).Decode(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mcpproxy", flag.ContinueOnError)
	configPath := fs.String("config", "config.json", "path to config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	log.Printf("Backend: %s", cfg.BackendURL)
	log.Printf("Listen:  %s", cfg.ListenAddr)

	// Connect to all MCP servers.
	bridge, err := NewMCPBridge(ctx, cfg.MCPServers)
	if err != nil {
		return fmt.Errorf("initializing MCP bridge: %w", err)
	}
	defer bridge.Close()

	// Set up the reverse proxy for pass-through endpoints.
	target, err := url.Parse(cfg.BackendURL)
	if err != nil {
		return fmt.Errorf("parsing backend URL: %w", err)
	}
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
		},
	}

	// HTTP mux: chat completions get the agentic handler, everything else passes through.
	mux := http.NewServeMux()
	mux.Handle("/v1/chat/completions", chatHandler(cfg.BackendURL, bridge, cfg.MaxToolRounds))
	mux.Handle("/", rp)

	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
	}

	// Graceful shutdown when ctx is cancelled.
	go func() {
		<-ctx.Done()
		log.Println("Shutting down...")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP shutdown error: %v", err)
		}
	}()

	log.Printf("Proxy listening on http://%s%s", netutil.LocalIP(), cfg.ListenAddr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return fmt.Errorf("HTTP server error: %w", err)
	}
	log.Println("Shutdown complete.")
	return nil
}
