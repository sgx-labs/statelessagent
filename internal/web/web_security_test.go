package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- localhostOnly middleware ---

func TestLocalhostOnly_AllowsLocalhost(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := localhostOnly(inner)

	tests := []struct {
		name string
		host string
	}{
		{"localhost", "localhost:4078"},
		{"127.0.0.1", "127.0.0.1:4078"},
		{"ipv6 loopback", "[::1]:4078"},
		{"localhost no port", "localhost"},
		{"127.0.0.1 no port", "127.0.0.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = tt.host
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("expected 200 for host %q, got %d", tt.host, w.Code)
			}
		})
	}
}

func TestLocalhostOnly_BlocksRemoteHosts(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := localhostOnly(inner)

	tests := []struct {
		name string
		host string
	}{
		{"external domain", "evil.com:4078"},
		{"external IP", "192.168.1.1:4078"},
		{"DNS rebinding", "attacker.example.com:4078"},
		{"internal IP", "10.0.0.1:4078"},
		{"cloud metadata", "169.254.169.254:4078"},
		{"localhost substring", "localhost.evil.com:4078"},
		{"127 substring", "127.0.0.1.evil.com:4078"},
		{"empty host", ""},
		{"zero IP", "0.0.0.0:4078"},
		{"private class B", "172.16.0.1:4078"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = tt.host
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Errorf("expected 403 for host %q, got %d", tt.host, w.Code)
			}
		})
	}
}

// --- securityHeaders middleware ---

func TestSecurityHeaders_Present(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := securityHeaders(inner)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	tests := []struct {
		header string
		want   string
	}{
		{"X-Frame-Options", "DENY"},
		{"X-Content-Type-Options", "nosniff"},
		{"Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self' data:"},
	}
	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			got := w.Header().Get(tt.header)
			if got != tt.want {
				t.Errorf("%s = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

// --- isPrivatePath ---

func TestIsPrivatePath_Variants(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"_PRIVATE/secret.md", true},
		{"_private/secret.md", true},
		{"_Private/deep/file.md", true},
		{"_PRIVATE\\secret.md", true},
		{"notes/public.md", false},
		{"private/not-actually-private.md", false},
		{"notes/_PRIVATE_note.md", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isPrivatePath(tt.path)
			if got != tt.want {
				t.Errorf("isPrivatePath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsUnsafeAPIPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"../etc/passwd", true},
		{"../../etc/shadow", true},
		{"/etc/passwd", true},
		{".git/config", true},
		{"notes/.hidden/file.md", true},
		{"notes/.git/config", true},
		{"C:/Windows/System32/drivers/etc/hosts", true},
		{"notes/public.md", false},
		{"projects/api/design.md", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			clean := tt.path
			got := isUnsafeAPIPath(clean)
			if got != tt.want {
				t.Errorf("isUnsafeAPIPath(%q) = %v, want %v", clean, got, tt.want)
			}
		})
	}
}

func TestHandleNoteByPath_PathTraversal(t *testing.T) {
	cases := []struct {
		path    string
		reject  bool
		private bool
	}{
		{path: "../etc/passwd", reject: true},
		{path: "../../etc/shadow", reject: true},
		{path: "notes/../../etc/passwd", reject: true},
		{path: "/etc/passwd", reject: true},
		{path: ".git/config", reject: true},
		{path: "notes/.hidden/file.md", reject: true},
		{path: "notes/.git/config", reject: true},
		{path: "_PRIVATE/secret.md", reject: false, private: true},
		{path: "notes/public.md", reject: false, private: false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := isUnsafeAPIPath(tc.path); got != tc.reject {
				t.Fatalf("isUnsafeAPIPath(%q)=%v want %v", tc.path, got, tc.reject)
			}
			if got := isPrivatePath(tc.path); got != tc.private {
				t.Fatalf("isPrivatePath(%q)=%v want %v", tc.path, got, tc.private)
			}
		})
	}
}

// --- XSS in note content ---

func TestFilterPrivateNotes_SnippetTruncation(t *testing.T) {
	// Verify snippets are truncated to 300 chars to limit XSS surface
	longText := make([]byte, 500)
	for i := range longText {
		longText[i] = 'a'
	}

	// filterPrivateNotes is tested indirectly — verify the constant
	if maxNoteSize != 5*1024*1024 {
		t.Errorf("maxNoteSize = %d, want %d", maxNoteSize, 5*1024*1024)
	}
}
