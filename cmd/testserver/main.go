package main

import (
	"context"
	"embed"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed app-with-deps.js
var sdkFS embed.FS

func main() {
	// Re-bundle the ESM SDK as an IIFE with exports on a global object
	iifeScript, err := bundleEntryPoints(sdkFS, []string{"app-with-deps.js"}, "McpApps")
	if err != nil {
		log.Fatalf("Failed to bundle SDK: %v", err)
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "test-ui-server",
		Version: "v0.1.0",
	}, nil)

	// Build the view HTML with the IIFE SDK inlined
	viewHTML := buildViewHTML(iifeScript)

	// Register a UI resource that returns the interactive HTML page
	server.AddResource(&mcp.Resource{
		URI:      "ui://demo/view",
		Name:     "demo-view",
		MIMEType: "text/html;profile=mcp-app",
		Meta: mcp.Meta{
			"ui": map[string]any{
				"csp": map[string]any{},
			},
		},
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      "ui://demo/view",
				MIMEType: "text/html;profile=mcp-app",
				Text:     viewHTML,
				Meta: mcp.Meta{
					"ui": map[string]any{
						"csp": map[string]any{},
					},
				},
			}},
		}, nil
	})

	// Register a tool with a UI resource
	type DemoInput struct {
		Message string `json:"message" jsonschema:"a message to process"`
	}
	type DemoOutput struct {
		Processed string `json:"processed"`
		Length    int    `json:"length"`
	}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "demo-ui-tool",
		Description: "A demo tool with a UI view",
		Meta: mcp.Meta{
			"ui": map[string]any{
				"resourceUri": "ui://demo/view",
				"visibility":  []string{"model", "app"},
			},
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DemoInput) (*mcp.CallToolResult, DemoOutput, error) {
		output := DemoOutput{
			Processed: "Processed: " + input.Message,
			Length:    len(input.Message),
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: output.Processed},
			},
		}, output, nil
	})

	// Register a plain tool (no UI) for contrast
	mcp.AddTool(server, &mcp.Tool{
		Name:        "echo",
		Description: "Echoes the input back",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DemoInput) (*mcp.CallToolResult, DemoOutput, error) {
		return nil, DemoOutput{
			Processed: input.Message,
			Length:    len(input.Message),
		}, nil
	})

	// Register a tool that the view can call (app-visible)
	type CountInput struct {
		Text string `json:"text" jsonschema:"text to count words in"`
	}
	type CountOutput struct {
		WordCount int `json:"wordCount"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "count-words",
		Description: "Counts words in text",
		Meta: mcp.Meta{
			"ui": map[string]any{
				"visibility": []string{"app"},
			},
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CountInput) (*mcp.CallToolResult, CountOutput, error) {
		words := 0
		inWord := false
		for _, r := range input.Text {
			if r == ' ' || r == '\t' || r == '\n' {
				inWord = false
			} else if !inWord {
				inWord = true
				words++
			}
		}
		return nil, CountOutput{WordCount: words}, nil
	})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// buildViewHTML constructs the view HTML with the ext-apps SDK inlined as an IIFE.
// The IIFE exposes exports on a global object (McpApps), so it works in regular
// <script> tags that document.write() will execute.
func buildViewHTML(iifeScript string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<meta name="color-scheme" content="light dark">
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    font-family: system-ui, -apple-system, sans-serif;
    padding: 16px;
    color: var(--text-color, #333);
    background: var(--bg-color, #f8f9fa);
  }
  h2 { margin: 0 0 12px 0; font-size: 18px; font-weight: 600; }
  .section {
    background: var(--card-bg, white);
    border-radius: 8px;
    padding: 12px;
    margin-bottom: 12px;
    box-shadow: 0 1px 3px rgba(0,0,0,0.1);
  }
  .label {
    font-weight: 600;
    color: var(--label-color, #666);
    font-size: 12px;
    text-transform: uppercase;
    margin-bottom: 4px;
  }
  pre {
    background: var(--code-bg, #f0f0f0);
    padding: 8px;
    border-radius: 4px;
    overflow-x: auto;
    font-size: 13px;
    white-space: pre-wrap;
    font-family: ui-monospace, monospace;
  }
  button {
    background: var(--button-bg, #0066cc);
    color: var(--button-text, white);
    border: none;
    padding: 8px 16px;
    border-radius: 4px;
    cursor: pointer;
    font-size: 14px;
  }
  button:hover { opacity: 0.9; }
  #status { color: var(--muted-color, #999); font-style: italic; }
  @media (prefers-color-scheme: dark) {
    body { --text-color: #e0e0e0; --bg-color: #1a1a1a; }
    .section { --card-bg: #2a2a2a; }
    pre { --code-bg: #333; }
    .label { --label-color: #aaa; }
  }
</style>
</head>
<body>
<h2>Demo UI View</h2>
<div id="status">Loading SDK...</div>

<div id="tool-input" class="section" style="display:none">
  <div class="label">Tool Input</div>
  <pre id="input-data"></pre>
</div>

<div id="tool-result" class="section" style="display:none">
  <div class="label">Tool Result</div>
  <pre id="result-data"></pre>
</div>

<div class="section">
  <div class="label">Call Tool from View</div>
  <button id="count-btn">Count Words in Input</button>
  <pre id="count-result" style="display:none"></pre>
</div>

<!-- Inlined IIFE SDK (exports available as McpApps.*) -->
<script>%s</script>
<script>
(function() {
  var statusEl = document.getElementById("status");

  if (typeof McpApps === "undefined" || typeof McpApps.App === "undefined") {
    statusEl.textContent = "Error: SDK failed to load (App not defined)";
    return;
  }

  var App = McpApps.App;
  var applyDocumentTheme = McpApps.applyDocumentTheme;
  var applyHostStyleVariables = McpApps.applyHostStyleVariables;
  var applyHostFonts = McpApps.applyHostFonts;

  statusEl.textContent = "Connecting to host...";

  var app = new App(
    { name: "DemoView", version: "1.0.0" },
    { availableDisplayModes: ["inline"] },
    { autoResize: true }
  );

  var lastInput = "";

  // Register handlers BEFORE connecting
  app.ontoolinput = function(params) {
    statusEl.style.display = "none";
    document.getElementById("tool-input").style.display = "block";
    var args = params.arguments || {};
    lastInput = JSON.stringify(args, null, 2);
    document.getElementById("input-data").textContent = lastInput;
  };

  app.ontoolresult = function(result) {
    document.getElementById("tool-result").style.display = "block";
    document.getElementById("result-data").textContent =
      JSON.stringify(result, null, 2);
  };

  app.ontoolcancelled = function(params) {
    statusEl.textContent = "Tool cancelled: " + (params.reason || "unknown");
    statusEl.style.display = "block";
  };

  app.onteardown = function() {
    console.info("App is being torn down");
    return {};
  };

  app.onerror = function(err) { console.error("App error:", err); };

  app.onhostcontextchanged = function(ctx) {
    if (ctx.theme) applyDocumentTheme(ctx.theme);
    if (ctx.styles && ctx.styles.variables) applyHostStyleVariables(ctx.styles.variables);
    if (ctx.styles && ctx.styles.css && ctx.styles.css.fonts) applyHostFonts(ctx.styles.css.fonts);
  };

  // Button handler: call count-words tool through the host
  document.getElementById("count-btn").addEventListener("click", function() {
    var resultEl = document.getElementById("count-result");
    resultEl.style.display = "block";
    resultEl.textContent = "Counting...";
    app.callServerTool({
      name: "count-words",
      arguments: { text: lastInput },
    }).then(function(result) {
      resultEl.textContent = JSON.stringify(result, null, 2);
    }).catch(function(err) {
      resultEl.textContent = "Error: " + (err.message || JSON.stringify(err));
    });
  });

  // Connect to host
  app.connect().then(function() {
    var ctx = app.getHostContext();
    if (ctx && ctx.theme) applyDocumentTheme(ctx.theme);
    if (ctx && ctx.styles && ctx.styles.variables) applyHostStyleVariables(ctx.styles.variables);
    statusEl.textContent = "Connected. Waiting for tool input...";
  }).catch(function(err) {
    statusEl.textContent = "Connection error: " + (err.message || err);
  });
})();
</script>
</body>
</html>
`, iifeScript)
}
