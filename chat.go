package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

// ToolBridge is the interface used by chatHandler to discover and call tools.
// *MCPBridge satisfies this interface.
type ToolBridge interface {
	Tools() []Tool
	CallTool(ctx context.Context, name, argsJSON string) (string, error)
}

// chatHandler returns an http.HandlerFunc that implements the agentic tool loop.
// It intercepts /v1/chat/completions, injects MCP tools, and loops on tool calls
// until the LLM produces a final text response.
func chatHandler(backendURL string, bridge ToolBridge, maxRounds int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Parse the incoming request.
		var req ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Remember if the client wants streaming; we'll use non-streaming for the tool loop.
		clientWantsStream := req.Stream != nil && *req.Stream

		// Inject MCP tools (replaces any client-provided tools).
		req.Tools = bridge.Tools()

		// --- Agentic tool loop ---
		for range maxRounds {
			// Force non-streaming for the loop.
			noStream := false
			req.Stream = &noStream

			resp, err := forwardToBackend(backendURL, &req)
			if err != nil {
				http.Error(w, "backend error: "+err.Error(), http.StatusBadGateway)
				return
			}

			// If no tool calls, this is the final response.
			if len(resp.Choices) == 0 || len(resp.Choices[0].Message.ToolCalls) == 0 {
				if clientWantsStream {
					// Re-issue this final round with streaming to get true token-by-token output.
					req.Stream = new(true)
					streamFromBackend(backendURL, &req, w)
				} else {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(resp)
				}
				return
			}

			// Append the assistant's response (with tool calls) to the conversation.
			assistantMsg := resp.Choices[0].Message
			req.Messages = append(req.Messages, assistantMsg)

			// Execute each tool call and append results.
			for _, tc := range assistantMsg.ToolCalls {
				log.Printf("Tool call: %s(%s)", tc.Function.Name, tc.Function.Arguments)

				result, err := bridge.CallTool(r.Context(), tc.Function.Name, tc.Function.Arguments)
				if err != nil {
					log.Printf("Tool error: %v", err)
					// Send the error text back as the tool result so the LLM can recover.
					if result == "" {
						result = err.Error()
					}
				}

				log.Printf("Tool result: %s", truncate(result, 200))

				req.Messages = append(req.Messages, Message{
					Role:       "tool",
					Content:    result,
					ToolCallID: tc.ID,
				})
			}
		}

		// If we exhaust all rounds, return the last non-streaming response.
		http.Error(w, "max tool rounds exceeded", http.StatusInternalServerError)
	}
}

// forwardToBackend sends a non-streaming chat completion request to the backend
// and returns the parsed response.
func forwardToBackend(backendURL string, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	resp, err := http.Post(backendURL+"/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("posting to backend: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("backend returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &result, nil
}

// streamFromBackend sends a streaming request to the backend and pipes the SSE
// response directly to the client.
func streamFromBackend(backendURL string, req *ChatCompletionRequest, w http.ResponseWriter) {
	body, err := json.Marshal(req)
	if err != nil {
		http.Error(w, "marshaling request: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp, err := http.Post(backendURL+"/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		http.Error(w, "backend error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Forward headers for SSE.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Flush immediately so the client starts receiving chunks.
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Copy the SSE stream, flushing after each write.
	io.Copy(flushWriter{w, flusher}, resp.Body)
}

// flushWriter wraps an http.ResponseWriter and flushes after every write,
// enabling use with io.Copy for streaming responses.
type flushWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	fw.f.Flush()
	return n, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
