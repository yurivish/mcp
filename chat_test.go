package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// --- Test helpers ---

// fakeBridge implements ToolBridge for testing.
type fakeBridge struct {
	tools  []Tool
	callFn func(ctx context.Context, name, argsJSON string) (string, error)
}

func (f *fakeBridge) Tools() []Tool { return f.tools }

func (f *fakeBridge) CallTool(ctx context.Context, name, argsJSON string) (string, error) {
	return f.callFn(ctx, name, argsJSON)
}

// fakeBackend returns an httptest.Server that serves a sequence of canned
// ChatCompletionResponse values, one per request. It also captures each
// decoded request body so tests can inspect what the handler sent.
func fakeBackend(t *testing.T, responses ...ChatCompletionResponse) (*httptest.Server, *[]ChatCompletionRequest) {
	t.Helper()
	var mu sync.Mutex
	var captured []ChatCompletionRequest
	call := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		captured = append(captured, req)
		idx := call
		call++
		mu.Unlock()

		if idx >= len(responses) {
			http.Error(w, "no more canned responses", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(responses[idx])
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

// textResponse builds a ChatCompletionResponse with a plain text message.
func textResponse(content string) ChatCompletionResponse {
	return ChatCompletionResponse{
		ID:    "resp-1",
		Model: "test-model",
		Choices: []Choice{{
			Message:      Message{Role: "assistant", Content: content},
			FinishReason: "stop",
		}},
	}
}

// toolCallResponse builds a ChatCompletionResponse with the given tool calls.
func toolCallResponse(calls ...ToolCall) ChatCompletionResponse {
	return ChatCompletionResponse{
		ID:    "resp-1",
		Model: "test-model",
		Choices: []Choice{{
			Message:      Message{Role: "assistant", ToolCalls: calls},
			FinishReason: "tool_calls",
		}},
	}
}

// postChat fires a ChatCompletionRequest at the handler and returns the
// parsed response and HTTP status code.
func postChat(t *testing.T, handler http.Handler, req ChatCompletionRequest) (ChatCompletionResponse, int) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling request: %v", err)
	}

	rr := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rr, httpReq)

	var resp ChatCompletionResponse
	if rr.Code == http.StatusOK {
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
	}
	return resp, rr.Code
}

func simpleRequest() ChatCompletionRequest {
	return ChatCompletionRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "hello"}},
	}
}

// --- Tests ---

func TestNoToolCalls(t *testing.T) {
	backend, _ := fakeBackend(t, textResponse("Hello!"))
	bridge := &fakeBridge{
		tools: []Tool{{Type: "function", Function: ToolFunction{Name: "greet"}}},
		callFn: func(ctx context.Context, name, argsJSON string) (string, error) {
			t.Fatal("CallTool should not be called")
			return "", nil
		},
	}

	handler := chatHandler(backend.URL, bridge, 5)
	resp, code := postChat(t, handler, simpleRequest())

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.Choices[0].Message.Content != "Hello!" {
		t.Fatalf("expected 'Hello!', got %q", resp.Choices[0].Message.Content)
	}
}

func TestOneToolRound(t *testing.T) {
	backend, captured := fakeBackend(t,
		toolCallResponse(ToolCall{
			ID:       "call-1",
			Type:     "function",
			Function: FunctionCall{Name: "greet", Arguments: `{"name":"World"}`},
		}),
		textResponse("Hello, World!"),
	)

	var calledName, calledArgs string
	bridge := &fakeBridge{
		tools: []Tool{{Type: "function", Function: ToolFunction{Name: "greet"}}},
		callFn: func(ctx context.Context, name, argsJSON string) (string, error) {
			calledName = name
			calledArgs = argsJSON
			return "greeting: Hello, World!", nil
		},
	}

	handler := chatHandler(backend.URL, bridge, 5)
	resp, code := postChat(t, handler, simpleRequest())

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.Choices[0].Message.Content != "Hello, World!" {
		t.Fatalf("expected 'Hello, World!', got %q", resp.Choices[0].Message.Content)
	}
	if calledName != "greet" {
		t.Fatalf("expected tool name 'greet', got %q", calledName)
	}
	if calledArgs != `{"name":"World"}` {
		t.Fatalf("expected args '{\"name\":\"World\"}', got %q", calledArgs)
	}

	// Verify the second request to the backend includes the tool result.
	reqs := *captured
	if len(reqs) != 2 {
		t.Fatalf("expected 2 backend requests, got %d", len(reqs))
	}
	msgs := reqs[1].Messages
	toolMsg := msgs[len(msgs)-1]
	if toolMsg.Role != "tool" {
		t.Fatalf("expected role 'tool', got %q", toolMsg.Role)
	}
	if toolMsg.ToolCallID != "call-1" {
		t.Fatalf("expected tool_call_id 'call-1', got %q", toolMsg.ToolCallID)
	}
	if toolMsg.Content != "greeting: Hello, World!" {
		t.Fatalf("expected tool content 'greeting: Hello, World!', got %q", toolMsg.Content)
	}
}

func TestNonexistentTool(t *testing.T) {
	backend, captured := fakeBackend(t,
		toolCallResponse(ToolCall{
			ID:       "call-1",
			Type:     "function",
			Function: FunctionCall{Name: "nonexistent", Arguments: "{}"},
		}),
		textResponse("Sorry, I couldn't do that."),
	)

	bridge := &fakeBridge{
		tools: []Tool{},
		callFn: func(ctx context.Context, name, argsJSON string) (string, error) {
			return "", fmt.Errorf("unknown tool: %s", name)
		},
	}

	handler := chatHandler(backend.URL, bridge, 5)
	resp, code := postChat(t, handler, simpleRequest())

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.Choices[0].Message.Content != "Sorry, I couldn't do that." {
		t.Fatalf("unexpected response: %q", resp.Choices[0].Message.Content)
	}

	// The error text should have been fed back as the tool result.
	reqs := *captured
	if len(reqs) != 2 {
		t.Fatalf("expected 2 backend requests, got %d", len(reqs))
	}
	msgs := reqs[1].Messages
	toolMsg := msgs[len(msgs)-1]
	if toolMsg.Content != "unknown tool: nonexistent" {
		t.Fatalf("expected error fed back as tool content, got %q", toolMsg.Content)
	}
}

func TestToolError(t *testing.T) {
	backend, captured := fakeBackend(t,
		toolCallResponse(ToolCall{
			ID:       "call-1",
			Type:     "function",
			Function: FunctionCall{Name: "flaky", Arguments: "{}"},
		}),
		textResponse("Recovered from error."),
	)

	bridge := &fakeBridge{
		tools: []Tool{{Type: "function", Function: ToolFunction{Name: "flaky"}}},
		callFn: func(ctx context.Context, name, argsJSON string) (string, error) {
			// Return both a result and an error — the result text should be preferred.
			return "partial output before failure", fmt.Errorf("something went wrong")
		},
	}

	handler := chatHandler(backend.URL, bridge, 5)
	resp, code := postChat(t, handler, simpleRequest())

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.Choices[0].Message.Content != "Recovered from error." {
		t.Fatalf("unexpected response: %q", resp.Choices[0].Message.Content)
	}

	// When CallTool returns both result and error, the result text is used.
	reqs := *captured
	msgs := reqs[1].Messages
	toolMsg := msgs[len(msgs)-1]
	if toolMsg.Content != "partial output before failure" {
		t.Fatalf("expected result text as tool content, got %q", toolMsg.Content)
	}
}

func TestMaxRoundsExceeded(t *testing.T) {
	// Backend always returns tool calls — should hit the limit.
	tc := toolCallResponse(ToolCall{
		ID:       "call-1",
		Type:     "function",
		Function: FunctionCall{Name: "loop", Arguments: "{}"},
	})
	backend, _ := fakeBackend(t, tc, tc, tc)

	bridge := &fakeBridge{
		tools: []Tool{{Type: "function", Function: ToolFunction{Name: "loop"}}},
		callFn: func(ctx context.Context, name, argsJSON string) (string, error) {
			return "looping", nil
		},
	}

	handler := chatHandler(backend.URL, bridge, 2)
	_, code := postChat(t, handler, simpleRequest())

	if code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", code)
	}
}

func TestMultipleToolCalls(t *testing.T) {
	backend, captured := fakeBackend(t,
		toolCallResponse(
			ToolCall{ID: "call-a", Type: "function", Function: FunctionCall{Name: "add", Arguments: `{"x":1}`}},
			ToolCall{ID: "call-b", Type: "function", Function: FunctionCall{Name: "mul", Arguments: `{"x":2}`}},
		),
		textResponse("Done."),
	)

	var calls []string
	bridge := &fakeBridge{
		tools: []Tool{
			{Type: "function", Function: ToolFunction{Name: "add"}},
			{Type: "function", Function: ToolFunction{Name: "mul"}},
		},
		callFn: func(ctx context.Context, name, argsJSON string) (string, error) {
			calls = append(calls, name)
			return "result-" + name, nil
		},
	}

	handler := chatHandler(backend.URL, bridge, 5)
	resp, code := postChat(t, handler, simpleRequest())

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if resp.Choices[0].Message.Content != "Done." {
		t.Fatalf("unexpected response: %q", resp.Choices[0].Message.Content)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}

	// Verify both tool results in the second backend request.
	reqs := *captured
	msgs := reqs[1].Messages
	// Last two messages should be the tool results.
	toolA := msgs[len(msgs)-2]
	toolB := msgs[len(msgs)-1]
	if toolA.ToolCallID != "call-a" || toolA.Content != "result-add" {
		t.Fatalf("unexpected tool result A: id=%q content=%q", toolA.ToolCallID, toolA.Content)
	}
	if toolB.ToolCallID != "call-b" || toolB.Content != "result-mul" {
		t.Fatalf("unexpected tool result B: id=%q content=%q", toolB.ToolCallID, toolB.Content)
	}
}

func TestBackendError(t *testing.T) {
	// Backend that always returns 500.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	t.Cleanup(backend.Close)

	bridge := &fakeBridge{
		tools: []Tool{},
		callFn: func(ctx context.Context, name, argsJSON string) (string, error) {
			return "", nil
		},
	}

	handler := chatHandler(backend.URL, bridge, 5)
	_, code := postChat(t, handler, simpleRequest())

	if code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", code)
	}
}
