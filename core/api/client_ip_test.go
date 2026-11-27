package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPRequiresTrustedProxy(t *testing.T) {
	middleware, err := proxyClientIPMiddleware("10.1.0.0/24")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ remote, forwarded, want string }{
		{"192.0.2.2:1234", "203.0.113.10", "192.0.2.2"},
		{"10.1.0.10:1234", "203.0.113.10, 192.0.2.2, 10.1.0.9", "192.0.2.2"},
		{"10.1.0.10:1234", "invalid", "10.1.0.10"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tc.remote
		r.Header.Set("X-Forwarded-For", tc.forwarded)
		r.Header.Set("True-Client-IP", "203.0.113.1")
		middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if got, _ := previewRateKey(r); got != tc.want {
				t.Errorf("client for %q via %q = %q; want %q", tc.forwarded, tc.remote, got, tc.want)
			}
		})).ServeHTTP(httptest.NewRecorder(), r)
	}
}
