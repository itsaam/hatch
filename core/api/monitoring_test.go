package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestHTTPMonitoringExcludesRequestData(t *testing.T) {
	events := make(chan string, 2)
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		events <- string(payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	dsn := strings.Replace(collector.URL, "http://", "http://public@", 1) + "/1"
	monitor, err := newErrorMonitor(dsn, "test", "test-release")
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Use(monitor.httpErrors)
	fail := func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private response text", http.StatusInternalServerError)
	}
	router.Post("/builds/{id}", fail)
	router.Post("/deployments/{id}", fail)
	for _, path := range []string{"/builds/private-id", "/builds/private-id", "/deployments/private-id"} {
		req := httptest.NewRequest(http.MethodPost, path+"?key=private-query", strings.NewReader("private body"))
		req.Header.Set("Authorization", "Bearer private-token")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("status %d", recorder.Code)
		}
	}
	monitor.flush()
	if len(events) != 2 {
		t.Fatalf("reported %d errors, want one per route", len(events))
	}
	payload := <-events + <-events
	for _, want := range []string{"http_server_error", "/builds/{id}", "/deployments/{id}", "test-release"} {
		if !strings.Contains(payload, want) {
			t.Errorf("missing %q in the reported event", want)
		}
	}
	for _, private := range []string{"private-id", "private-query", "private body", "private-token", "private response text"} {
		if strings.Contains(payload, private) {
			t.Errorf("the event included %q", private)
		}
	}
}

func TestMonitoringPreservesOversizedRequestHandling(t *testing.T) {
	router := chi.NewRouter()
	router.Use((&errorMonitor{}).httpErrors)
	router.Post("/upload", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(maxRequestBody(w, r.Body, 32)); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		}
	})
	server := httptest.NewServer(router)
	defer server.Close()
	resp, err := server.Client().Post(server.URL+"/upload", "text/plain", strings.NewReader(strings.Repeat("x", 128)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge || !resp.Close {
		t.Fatalf("oversized request got status %d and close=%v", resp.StatusCode, resp.Close)
	}
}
