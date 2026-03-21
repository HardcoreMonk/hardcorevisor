package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func BenchmarkHealthEndpoint(b *testing.B) {
	router := NewRouter(nil)
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	}
}

func BenchmarkCreateVM(b *testing.B) {
	router := NewRouter(nil)
	body := `{"name":"bench-vm","vcpus":2,"memory_mb":4096}`
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/vms", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	}
}

func BenchmarkListVMs(b *testing.B) {
	router := NewRouter(nil)
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/vms", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	}
}
