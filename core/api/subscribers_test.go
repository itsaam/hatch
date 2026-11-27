package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSubscribeReportsInvalidRequestsAndUnavailableDatabase(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://localhost/hatch")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()

	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"malformed JSON", `{`, http.StatusBadRequest},
		{"invalid email", `{"email":"dev@localhost"}`, http.StatusBadRequest},
		{"database unavailable", `{"email":"dev@example.com"}`, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/subscribe", strings.NewReader(tc.body))
			response := httptest.NewRecorder()
			subscribeHandler(pool).ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("subscription status = %d, want %d", response.Code, tc.status)
			}
		})
	}
}
