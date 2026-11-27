package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSubscribeReportsUnavailableDatabase(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://localhost/hatch")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()

	request := httptest.NewRequest(http.MethodPost, "/api/subscribe", strings.NewReader(`{"email":"dev@example.com"}`))
	response := httptest.NewRecorder()
	subscribeHandler(pool).ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("subscription status = %d, want 503", response.Code)
	}
}
