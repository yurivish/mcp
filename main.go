package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Config holds the proxy configuration loaded from a JSON file.
type Config struct {
	BackendURL   string                     `json:"backend_url"`
	ListenAddr   string                     `json:"listen_addr"`
	MaxToolRounds int                       `json:"max_tool_rounds"`
	MCPServers   map[string]MCPServerConfig `json:"mcpServers"`
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

func main() {
	configPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	log.Printf("Backend: %s", cfg.BackendURL)
	log.Printf("Listen:  %s", cfg.ListenAddr)

	// Create a cancellable context for MCP server lifetimes.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to all MCP servers.
	bridge, err := NewMCPBridge(ctx, cfg.MCPServers)
	if err != nil {
		log.Fatalf("initializing MCP bridge: %v", err)
	}
	defer bridge.Close()

	// Set up the reverse proxy for pass-through endpoints.
	target, err := url.Parse(cfg.BackendURL)
	if err != nil {
		log.Fatalf("parsing backend URL: %v", err)
	}
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
		},
	}

	// HTTP mux: chat completions get the agentic handler, everything else passes through.
	mux := http.NewServeMux()
	mux.Handle("/v1/chat/completions", chatHandler(cfg.BackendURL, bridge, cfg.MaxToolRounds))
	mux.Handle("/", proxy)

	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("Received %v, shutting down...", sig)
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP shutdown error: %v", err)
		}
	}()

	log.Printf("Proxy listening on %s", cfg.ListenAddr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
	log.Println("Shutdown complete.")
}
