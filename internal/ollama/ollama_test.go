package ollama

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newLocalHTTPServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("skipping: cannot bind local test listener: %v", err)
	}

	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = ln
	srv.Start()
	return srv
}

func TestListChatModels_FiltersEmbedding(t *testing.T) {
	srv := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(tagsResponse{
			Models: []Model{
				{Name: "llama3.2:1b", Size: 1000},
				{Name: "nomic-embed-text:latest", Size: 500},
				{Name: "mistral", Size: 4000},
				{Name: "mxbai-embed-large:latest", Size: 600},
			},
		})
	}))
	defer srv.Close()

	c := NewClientWithURL(srv.URL)
	models, err := c.ListChatModels()
	if err != nil {
		t.Fatalf("ListChatModels: %v", err)
	}

	if len(models) != 2 {
		t.Fatalf("expected 2 chat models, got %d", len(models))
	}
	if models[0].Name != "llama3.2:1b" {
		t.Errorf("expected first model llama3.2:1b, got %s", models[0].Name)
	}
	if models[1].Name != "mistral" {
		t.Errorf("expected second model mistral, got %s", models[1].Name)
	}
}

func TestListChatModels_EmptyResponse(t *testing.T) {
	srv := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tagsResponse{Models: []Model{}})
	}))
	defer srv.Close()

	c := NewClientWithURL(srv.URL)
	models, err := c.ListChatModels()
	if err != nil {
		t.Fatalf("ListChatModels: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("expected 0 models, got %d", len(models))
	}
}

func TestPickBestModel_PrefersSmallest(t *testing.T) {
	srv := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tagsResponse{
			Models: []Model{
				{Name: "mistral", Size: 4000},
				{Name: "llama3.2:3b", Size: 3000},
				{Name: "llama3.2:1b", Size: 1000},
			},
		})
	}))
	defer srv.Close()

	c := NewClientWithURL(srv.URL)
	model, err := c.PickBestModel()
	if err != nil {
		t.Fatalf("PickBestModel: %v", err)
	}
	if model != "llama3.2:1b" {
		t.Errorf("expected llama3.2:1b, got %s", model)
	}
}

func TestPickBestModel_FallsBackToFirst(t *testing.T) {
	srv := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tagsResponse{
			Models: []Model{
				{Name: "some-custom-model:7b", Size: 7000},
			},
		})
	}))
	defer srv.Close()

	c := NewClientWithURL(srv.URL)
	model, err := c.PickBestModel()
	if err != nil {
		t.Fatalf("PickBestModel: %v", err)
	}
	if model != "some-custom-model:7b" {
		t.Errorf("expected some-custom-model:7b, got %s", model)
	}
}

func TestPickBestModel_NoModels(t *testing.T) {
	srv := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tagsResponse{Models: []Model{}})
	}))
	defer srv.Close()

	c := NewClientWithURL(srv.URL)
	model, err := c.PickBestModel()
	if err != nil {
		t.Fatalf("PickBestModel: %v", err)
	}
	if model != "" {
		t.Errorf("expected empty string, got %s", model)
	}
}

func TestGenerate_Success(t *testing.T) {
	srv := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req generateRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "test-model" {
			t.Errorf("expected model test-model, got %s", req.Model)
		}
		if req.Stream {
			t.Error("expected stream=false")
		}

		json.NewEncoder(w).Encode(generateResponse{
			Response: "  The answer is 42.  ",
		})
	}))
	defer srv.Close()

	c := NewClientWithURL(srv.URL)
	answer, err := c.Generate("test-model", "What is the answer?")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if answer != "The answer is 42." {
		t.Errorf("expected trimmed answer, got %q", answer)
	}
}

func TestGenerate_Error(t *testing.T) {
	srv := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("model not loaded"))
	}))
	defer srv.Close()

	c := NewClientWithURL(srv.URL)
	_, err := c.Generate("test-model", "hello")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !contains(err.Error(), "500") {
		t.Errorf("expected error to mention 500, got: %s", err.Error())
	}
}

func TestGenerate_ConnectionRefused(t *testing.T) {
	c := NewClientWithURL("http://localhost:1") // port 1 — should fail to connect
	_, err := c.Generate("test-model", "hello")
	if err == nil {
		t.Fatal("expected error for connection refused")
	}
}

func TestListChatModels_ServerError(t *testing.T) {
	srv := newLocalHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := NewClientWithURL(srv.URL)
	_, err := c.ListChatModels()
	if err == nil {
		t.Fatal("expected error for 503 response")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
