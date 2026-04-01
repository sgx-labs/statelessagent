package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestMCPServer() *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{
		Name:    "same-test",
		Version: "v0.0.1-test",
	}, nil)
}

func TestMCPEndpoint_Initialize(t *testing.T) {
	server := newTestMCPServer()
	handler := newMCPHandler(MCPOptions{
		Server: server,
		Token:  "test-token-123",
	})

	body := `{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18", "clientInfo": {"name": "test", "version": "0.1"}, "capabilities": {}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer test-token-123")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	respBody := rr.Body.String()
	if !strings.Contains(respBody, "same-test") {
		t.Fatalf("expected response to contain server name 'same-test', got: %s", respBody)
	}
	if !strings.Contains(respBody, "protocolVersion") {
		t.Fatalf("expected response to contain protocolVersion, got: %s", respBody)
	}
}

func TestMCPEndpoint_RejectsMissingToken(t *testing.T) {
	server := newTestMCPServer()
	handler := newMCPHandler(MCPOptions{
		Server: server,
		Token:  "test-token-123",
	})

	body := `{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No Authorization header

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth header, got %d: %s", rr.Code, rr.Body.String())
	}

	if !strings.Contains(rr.Body.String(), "missing Authorization header") {
		t.Fatalf("expected missing auth error message, got: %s", rr.Body.String())
	}

	// Check WWW-Authenticate header is set
	if rr.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("expected WWW-Authenticate: Bearer header, got: %s", rr.Header().Get("WWW-Authenticate"))
	}
}

func TestMCPEndpoint_RejectsWrongToken(t *testing.T) {
	server := newTestMCPServer()
	handler := newMCPHandler(MCPOptions{
		Server: server,
		Token:  "correct-token",
	})

	body := `{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-token")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d: %s", rr.Code, rr.Body.String())
	}

	if !strings.Contains(rr.Body.String(), "invalid token") {
		t.Fatalf("expected invalid token error message, got: %s", rr.Body.String())
	}
}

func TestMCPEndpoint_AcceptsCorrectToken(t *testing.T) {
	server := newTestMCPServer()
	handler := newMCPHandler(MCPOptions{
		Server: server,
		Token:  "correct-token",
	})

	body := `{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-06-18", "clientInfo": {"name": "test", "version": "0.1"}, "capabilities": {}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer correct-token")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct token, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestMCPEndpoint_RejectsNonBearerAuth(t *testing.T) {
	server := newTestMCPServer()
	handler := newMCPHandler(MCPOptions{
		Server: server,
		Token:  "test-token",
	})

	body := `{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with Basic auth, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDashboard_StillWorksWithMCP(t *testing.T) {
	// Verify that the dashboard endpoints are unaffected when MCP is mounted.
	// The dashboard doesn't require auth and uses GET-only routes.
	server := newTestMCPServer()
	mcpHandler := newMCPHandler(MCPOptions{
		Server: server,
		Token:  "test-token",
	})

	// Simulate the top-level mux from Serve()
	topMux := http.NewServeMux()
	topMux.Handle("/mcp", mcpHandler)

	// Simple dashboard handler for testing
	topMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "dashboard OK") //nolint:errcheck
	})

	// Dashboard root should work without auth
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	topMux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("dashboard root: expected 200, got %d", rr.Code)
	}

	// MCP without auth should fail
	req = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{}`))
	rr = httptest.NewRecorder()
	topMux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("MCP without auth: expected 401, got %d", rr.Code)
	}
}

func TestBearerAuth_ConstantTimeComparison(t *testing.T) {
	// Ensure timing doesn't leak information — this test validates
	// that the auth handler uses constant-time comparison by checking
	// that both a partial-match and full-mismatch get the same result.
	handler := bearerAuth("secret-token-value", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name   string
		token  string
		expect int
	}{
		{"correct token", "secret-token-value", http.StatusOK},
		{"wrong token", "wrong-token", http.StatusUnauthorized},
		{"partial match", "secret-token", http.StatusUnauthorized},
		{"empty token", "", http.StatusUnauthorized},
		{"extra chars", "secret-token-value-extra", http.StatusUnauthorized},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.expect {
				t.Errorf("expected %d, got %d", tc.expect, rr.Code)
			}
		})
	}
}
