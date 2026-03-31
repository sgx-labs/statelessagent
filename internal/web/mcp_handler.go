package web

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MCPOptions configures the Streamable HTTP MCP endpoint.
type MCPOptions struct {
	// Server is the configured MCP server with tools registered.
	Server *mcp.Server

	// Token is the required Bearer token. If empty, all requests are rejected.
	Token string
}

// newMCPHandler creates an http.Handler that serves the MCP Streamable HTTP
// endpoint with Bearer token authentication. The handler wraps the SDK's
// StreamableHTTPHandler with auth middleware.
func newMCPHandler(opts MCPOptions) http.Handler {
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return opts.Server
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})

	return bearerAuth(opts.Token, handler)
}

// bearerAuth wraps an http.Handler with Bearer token authentication.
// Returns 401 Unauthorized if the token is missing or incorrect.
func bearerAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			w.Header().Set("WWW-Authenticate", `Bearer`)
			http.Error(w, `{"error":"missing Authorization header"}`, http.StatusUnauthorized)
			return
		}

		if !strings.HasPrefix(auth, "Bearer ") {
			w.Header().Set("WWW-Authenticate", `Bearer`)
			http.Error(w, `{"error":"invalid Authorization header format, expected: Bearer <token>"}`, http.StatusUnauthorized)
			return
		}

		provided := strings.TrimPrefix(auth, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer`)
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
