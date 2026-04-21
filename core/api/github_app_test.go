package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestAppClient(t *testing.T) *AppClient {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &AppClient{
		appID:      42,
		privateKey: key,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		baseURL:    "http://unused",
		tokens:     make(map[int64]cachedToken),
	}
}

func TestAppClient_AppJWT_FormatAndClaims(t *testing.T) {
	tests := []struct {
		name  string
		appID int64
	}{
		{"small id", 1},
		{"realistic id", 3446252},
		{"max int32", 1<<31 - 1},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newTestAppClient(t)
			c.appID = tc.appID

			tok, err := c.appJWT()
			if err != nil {
				t.Fatalf("appJWT: %v", err)
			}

			parts := strings.Split(tok, ".")
			if len(parts) != 3 {
				t.Fatalf("want 3 JWT parts, got %d", len(parts))
			}

			headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
			if err != nil {
				t.Fatalf("decode header: %v", err)
			}
			var header map[string]string
			if err := json.Unmarshal(headerRaw, &header); err != nil {
				t.Fatalf("header unmarshal: %v", err)
			}
			if header["alg"] != "RS256" || header["typ"] != "JWT" {
				t.Fatalf("unexpected header: %+v", header)
			}

			claimsRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err != nil {
				t.Fatalf("decode claims: %v", err)
			}
			var claims struct {
				Iat int64 `json:"iat"`
				Exp int64 `json:"exp"`
				Iss int64 `json:"iss"`
			}
			if err := json.Unmarshal(claimsRaw, &claims); err != nil {
				t.Fatalf("claims unmarshal: %v", err)
			}
			if claims.Iss != tc.appID {
				t.Fatalf("iss: want %d, got %d", tc.appID, claims.Iss)
			}
			if claims.Exp-claims.Iat > int64((10 * time.Minute).Seconds()) {
				t.Fatalf("TTL > 10m: %d", claims.Exp-claims.Iat)
			}
			if claims.Exp <= claims.Iat {
				t.Fatalf("exp must be > iat")
			}

			// Verify RS256 signature.
			sig, err := base64.RawURLEncoding.DecodeString(parts[2])
			if err != nil {
				t.Fatalf("decode sig: %v", err)
			}
			signingInput := parts[0] + "." + parts[1]
			digest := sha256.Sum256([]byte(signingInput))
			if err := rsa.VerifyPKCS1v15(&c.privateKey.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
				t.Fatalf("verify sig: %v", err)
			}
		})
	}
}

func TestAppClient_InstallationToken_CachesAndRefreshes(t *testing.T) {
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/123/access_tokens" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing bearer auth")
		}
		if ua := r.Header.Get("User-Agent"); ua != githubUserAgent {
			t.Errorf("bad UA: %q", ua)
		}
		if r.Header.Get("Accept") != githubAcceptHeader {
			t.Errorf("bad accept")
		}
		if r.Header.Get("X-GitHub-Api-Version") != githubAPIVersion {
			t.Errorf("bad api version")
		}
		hits.Add(1)
		// Return an expiry far enough ahead so the first token is cacheable.
		resp := map[string]any{
			"token":      fmt.Sprintf("ghs_hit%d", hits.Load()),
			"expires_at": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestAppClient(t)
	c.baseURL = srv.URL

	ctx := context.Background()
	t1, err := c.installationToken(ctx, 123)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	t2, err := c.installationToken(ctx, 123)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if t1 != t2 {
		t.Fatalf("tokens should match from cache, got %q vs %q", t1, t2)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("want 1 HTTP call, got %d", got)
	}

	// Force expiry and expect a refresh.
	c.mu.Lock()
	c.tokens[123] = cachedToken{token: "stale", expiresAt: time.Now().Add(-time.Second)}
	c.mu.Unlock()

	t3, err := c.installationToken(ctx, 123)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if t3 == "stale" {
		t.Fatalf("should have refreshed")
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("want 2 HTTP calls after refresh, got %d", got)
	}
}

func TestAppClient_InstallationToken_ConcurrentSafe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"token":      "ghs_concurrent",
			"expires_at": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestAppClient(t)
	c.baseURL = srv.URL

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.installationToken(context.Background(), 777); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent fetch: %v", err)
	}
}

func TestAppClient_CommentPR(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/app/installations/9/access_tokens" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      "ghs_x",
				"expires_at": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
			})
		case r.URL.Path == "/repos/octo/hello/issues/7/comments" && r.Method == http.MethodPost:
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("bad content-type: %s", ct)
			}
			if auth := r.Header.Get("Authorization"); auth != "token ghs_x" {
				t.Errorf("bad auth: %s", auth)
			}
			var body commentBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode: %v", err)
			}
			if body.Body != "hello" {
				t.Errorf("body=%q", body.Body)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]int64{"id": 99})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestAppClient(t)
	c.baseURL = srv.URL

	id, err := c.CommentPR(context.Background(), 9, "octo", "hello", 7, "hello")
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if id != 99 {
		t.Fatalf("id=%d", id)
	}
}

func TestAppClient_UpdateComment(t *testing.T) {
	var patched atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/app/installations/9/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      "ghs_x",
				"expires_at": time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339),
			})
		case r.URL.Path == "/repos/octo/hello/issues/comments/123" && r.Method == http.MethodPatch:
			var body commentBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode: %v", err)
			}
			if body.Body != "updated" {
				t.Errorf("body=%q", body.Body)
			}
			patched.Store(true)
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"id":123}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := newTestAppClient(t)
	c.baseURL = srv.URL

	if err := c.UpdateComment(context.Background(), 9, "octo", "hello", 123, "updated"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !patched.Load() {
		t.Fatalf("PATCH not hit")
	}
}

func TestAppClient_InstallationToken_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
	}))
	defer srv.Close()

	c := newTestAppClient(t)
	c.baseURL = srv.URL

	if _, err := c.installationToken(context.Background(), 1); err == nil {
		t.Fatalf("expected error on 401")
	}
}

func TestSplitRepo(t *testing.T) {
	tests := []struct {
		in         string
		wantOwner  string
		wantRepo   string
		wantOK     bool
	}{
		{"octo/hello", "octo", "hello", true},
		{"", "", "", false},
		{"no-slash", "", "", false},
		{"/leading", "", "", false},
		{"trailing/", "", "", false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			o, r, ok := splitRepo(tc.in)
			if ok != tc.wantOK || o != tc.wantOwner || r != tc.wantRepo {
				t.Fatalf("splitRepo(%q)=(%q,%q,%v), want (%q,%q,%v)",
					tc.in, o, r, ok, tc.wantOwner, tc.wantRepo, tc.wantOK)
			}
		})
	}
}
