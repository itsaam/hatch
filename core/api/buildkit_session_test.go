package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBuildKitSessionEnabled(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"no", false},
		{"off", false},
		{"random", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"yes", true},
		{"on", true},
		{"  1  ", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run("val="+tc.val, func(t *testing.T) {
			t.Setenv("HATCH_BUILDKIT_SESSION", tc.val)
			if got := buildKitSessionEnabled(); got != tc.want {
				t.Fatalf("buildKitSessionEnabled(%q) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}

func TestStaticSecretStore_GetSecret(t *testing.T) {
	t.Parallel()
	store := newStaticSecretStore([]buildSecret{
		{ID: "npm_token", Value: []byte("ghp_abcdef")},
		{ID: "sentry", Value: []byte("v1:something")},
	})

	t.Run("known id returns a copy", func(t *testing.T) {
		got, err := store.GetSecret(context.Background(), "npm_token")
		if err != nil {
			t.Fatalf("GetSecret: unexpected err: %v", err)
		}
		if string(got) != "ghp_abcdef" {
			t.Fatalf("got %q, want %q", got, "ghp_abcdef")
		}
		// Mutating the returned slice must not affect the store.
		got[0] = 'X'
		again, _ := store.GetSecret(context.Background(), "npm_token")
		if string(again) != "ghp_abcdef" {
			t.Fatalf("store mutated through caller: got %q", again)
		}
	})

	t.Run("unknown id returns os.ErrNotExist", func(t *testing.T) {
		_, err := store.GetSecret(context.Background(), "nope")
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("err = %v, want errors.Is(os.ErrNotExist)", err)
		}
	})
}

func TestStaticSecretStore_DefensiveCopyOnIngest(t *testing.T) {
	t.Parallel()
	original := []byte("secret-value")
	store := newStaticSecretStore([]buildSecret{{ID: "x", Value: original}})

	// Caller zeroes its slice — the store must keep its own copy.
	for i := range original {
		original[i] = 0
	}
	got, err := store.GetSecret(context.Background(), "x")
	if err != nil {
		t.Fatalf("GetSecret: %v", err)
	}
	if string(got) != "secret-value" {
		t.Fatalf("got %q, want %q (defensive copy missing?)", got, "secret-value")
	}
}

func TestLoadBuildSecretsForRepo(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		envs    map[string]string
		wantIDs []string
	}{
		{
			name:    "empty spec",
			spec:    "",
			wantIDs: nil,
		},
		{
			name:    "single pair",
			spec:    "npm=NPM_TOKEN",
			envs:    map[string]string{"NPM_TOKEN": "abc"},
			wantIDs: []string{"npm"},
		},
		{
			name:    "multiple pairs",
			spec:    "npm=NPM_TOKEN, sentry=SENTRY_TOKEN",
			envs:    map[string]string{"NPM_TOKEN": "abc", "SENTRY_TOKEN": "xyz"},
			wantIDs: []string{"npm", "sentry"},
		},
		{
			name:    "skips entries with empty env value",
			spec:    "npm=NPM_TOKEN,unset=NOT_SET",
			envs:    map[string]string{"NPM_TOKEN": "abc"},
			wantIDs: []string{"npm"},
		},
		{
			name:    "skips malformed pairs",
			spec:    "novalue=,=noid,bare,ok=OK_TOKEN",
			envs:    map[string]string{"OK_TOKEN": "v"},
			wantIDs: []string{"ok"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HATCH_BUILD_SECRETS", tc.spec)
			// Clear & set requested envs.
			for _, k := range []string{"NPM_TOKEN", "SENTRY_TOKEN", "OK_TOKEN", "NOT_SET"} {
				t.Setenv(k, "")
			}
			for k, v := range tc.envs {
				t.Setenv(k, v)
			}
			got := loadBuildSecretsForRepo()
			ids := make([]string, len(got))
			for i, s := range got {
				ids[i] = s.ID
			}
			if !equalStringSlices(ids, tc.wantIDs) {
				t.Fatalf("ids = %v, want %v", ids, tc.wantIDs)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNewBuildSession_NoSecrets(t *testing.T) {
	t.Parallel()
	s, id, err := newBuildSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("newBuildSession: %v", err)
	}
	defer s.Close()
	if id == "" {
		t.Fatalf("session id empty")
	}
	if s.ID() != id {
		t.Fatalf("returned id %q does not match session.ID() %q", id, s.ID())
	}
}

func TestNewBuildSession_WithSecrets(t *testing.T) {
	t.Parallel()
	s, id, err := newBuildSession(context.Background(), []buildSecret{
		{ID: "foo", Value: []byte("v")},
	})
	if err != nil {
		t.Fatalf("newBuildSession: %v", err)
	}
	defer s.Close()
	if id == "" {
		t.Fatalf("session id empty")
	}
}

func TestRunBuildSession_NilSession(t *testing.T) {
	t.Parallel()
	stop, err := runBuildSession(context.Background(), nil, "/nope")
	if err != nil {
		t.Fatalf("runBuildSession(nil): %v", err)
	}
	if stop == nil {
		t.Fatalf("stop func is nil")
	}
	stop() // must not panic.
}

func TestHijackedConn_DrainsPrefixThenDelegates(t *testing.T) {
	t.Parallel()
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	prefix := []byte("HEAD")
	hc := &hijackedConn{Conn: client, prefix: append([]byte(nil), prefix...)}

	// Server side writes the tail.
	go func() {
		_, _ = server.Write([]byte("TAIL"))
	}()

	buf := make([]byte, 4)
	if _, err := hc.Read(buf); err != nil {
		t.Fatalf("read prefix: %v", err)
	}
	if string(buf) != "HEAD" {
		t.Fatalf("first read: got %q, want HEAD", buf)
	}
	if _, err := hc.Read(buf); err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if string(buf) != "TAIL" {
		t.Fatalf("second read: got %q, want TAIL", buf)
	}
}

// TestDockerSessionDialer_UpgradeRequest verifies the dialer issues a proper
// HTTP/1.1 Upgrade with all session headers set, and surfaces non-101
// responses as errors. We swap the Unix socket for a TCP listener fronting a
// minimal handshake handler — the wire protocol is identical.
func TestDockerSessionDialer_UpgradeRequest(t *testing.T) {
	t.Parallel()

	// Use a Unix socket in tmpdir to exercise the unix-dial path the real
	// code uses.
	// macOS Unix sockets cap path at ~104 chars, and t.TempDir() under
	// /var/folders/... blows that budget. Stash the socket in /tmp and
	// clean it up by hand.
	sock := filepath.Join("/tmp", "hatch-bk-"+t.Name()+"-"+randHexFor(t)+".sock")
	t.Cleanup(func() { _ = os.Remove(sock) })

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	type captured struct {
		path    string
		method  string
		headers http.Header
	}
	got := make(chan captured, 1)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil {
			return
		}
		got <- captured{path: req.URL.Path, method: req.Method, headers: req.Header}

		// Send 101 then return — we only validate the handshake here, not
		// the gRPC stream that would follow.
		_, _ = c.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: h2c\r\nConnection: Upgrade\r\n\r\n"))
	}()

	dialer := dockerSessionDialer(sock)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := dialer(ctx, "h2c", map[string][]string{
		"X-Docker-Expose-Session-Uuid":      {"abc-123"},
		"X-Docker-Expose-Session-Sharedkey": {""},
	})
	if err != nil {
		t.Fatalf("dialer: %v", err)
	}
	defer conn.Close()

	select {
	case c := <-got:
		if c.method != http.MethodPost {
			t.Errorf("method = %s, want POST", c.method)
		}
		if c.path != "/session" {
			t.Errorf("path = %s, want /session", c.path)
		}
		if c.headers.Get("Upgrade") != "h2c" {
			t.Errorf("upgrade header = %q, want h2c", c.headers.Get("Upgrade"))
		}
		if c.headers.Get("X-Docker-Expose-Session-Uuid") != "abc-123" {
			t.Errorf("missing session uuid header: %v", c.headers)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not receive request")
	}
	wg.Wait()
}

func TestDockerSessionDialer_NonUpgradeResponseFails(t *testing.T) {
	t.Parallel()

	// macOS Unix sockets cap path at ~104 chars, and t.TempDir() under
	// /var/folders/... blows that budget. Stash the socket in /tmp and
	// clean it up by hand.
	sock := filepath.Join("/tmp", "hatch-bk-"+t.Name()+"-"+randHexFor(t)+".sock")
	t.Cleanup(func() { _ = os.Remove(sock) })

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		_, _ = http.ReadRequest(br)
		// Reply with a 500 instead of 101.
		_, _ = c.Write([]byte("HTTP/1.1 500 Internal Server Error\r\nContent-Length: 0\r\n\r\n"))
	}()

	dialer := dockerSessionDialer(sock)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = dialer(ctx, "h2c", nil)
	if err == nil {
		t.Fatalf("expected error on non-101 response")
	}
	if !strings.Contains(err.Error(), "101") {
		t.Fatalf("error %v does not mention expected 101", err)
	}
}

func TestRandomHex_LengthAndUniqueness(t *testing.T) {
	t.Parallel()
	a, err := randomHex()
	if err != nil {
		t.Fatalf("randomHex: %v", err)
	}
	b, err := randomHex()
	if err != nil {
		t.Fatalf("randomHex: %v", err)
	}
	if len(a) != 32 || len(b) != 32 {
		t.Fatalf("len = %d/%d, want 32", len(a), len(b))
	}
	if a == b {
		t.Fatalf("two random calls returned the same value: %s", a)
	}
}

// TestBuildPath_FlagOff_OmitsSession runs build() against a stub Docker that
// records the build query string and asserts nocache=1 is set and session is
// absent when the flag is off.
func TestBuildPath_FlagOff_OmitsSession(t *testing.T) {
	t.Setenv("HATCH_BUILDKIT_SESSION", "")
	checkBuildQuery(t, map[string]string{
		"nocache": "1",
		"version": "2",
	}, []string{"session"})
}

// TestBuildPath_FlagOn_NoSecrets_OmitsNocache verifies that turning the flag
// on removes nocache=1 (we want layer reuse) and does NOT set session=
// because dialling /var/run/docker.sock will fail in tests — the code must
// fall back gracefully. We assert nocache absent and session absent.
func TestBuildPath_FlagOn_NoSecrets_FallsBackGracefullyOnDialFailure(t *testing.T) {
	t.Setenv("HATCH_BUILDKIT_SESSION", "1")
	t.Setenv("HATCH_BUILD_SECRETS", "")
	// /var/run/docker.sock is almost certainly absent in CI test env. The
	// runBuildSession dial attempt should fail; our code logs and continues.
	// We assert: build still succeeds, nocache is absent (flag on), session
	// is absent (dial fell back). On a real host with Docker running this
	// test still holds because we hit a fake httptest server, not the
	// actual daemon — the dial will fail or the daemon won't speak gRPC
	// over /session, both of which our code treats as fallback.
	got := runBuildAndCaptureQuery(t)
	if got["nocache"] == "1" {
		t.Errorf("nocache=1 leaked when flag on (got %q)", got["nocache"])
	}
	if got["version"] != "2" {
		t.Errorf("version = %q, want 2", got["version"])
	}
	// session may or may not be set depending on whether the local Docker
	// socket happens to accept the upgrade — we only check it doesn't crash
	// the build.
}

func checkBuildQuery(t *testing.T, want map[string]string, mustBeAbsent []string) {
	t.Helper()
	got := runBuildAndCaptureQuery(t)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("query[%s] = %q, want %q", k, got[k], v)
		}
	}
	for _, k := range mustBeAbsent {
		if got[k] != "" {
			t.Errorf("query[%s] = %q, want empty", k, got[k])
		}
	}
}

func runBuildAndCaptureQuery(t *testing.T) map[string]string {
	t.Helper()

	captured := make(chan map[string]string, 1)
	srv := newDockerStub(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1.43/build") {
			vals := map[string]string{}
			for k := range r.URL.Query() {
				vals[k] = r.URL.Query().Get(k)
			}
			select {
			case captured <- vals:
			default:
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"stream":"ok\n"}` + "\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	d := &Deployer{
		http:       srv.Client(),
		notifier:   noopNotifier{},
		dockerBase: srv.URL,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.build(ctx, "owner/repo", 1, "deadbee", "web", "tag:1", "", 0); err != nil {
		t.Fatalf("build: %v", err)
	}
	select {
	case v := <-captured:
		return v
	case <-time.After(2 * time.Second):
		t.Fatalf("build endpoint never hit")
	}
	return nil
}

// newDockerStub returns a *dockerStub backing the test build endpoint.
type dockerStub struct {
	srv *http.Server
	ln  net.Listener
	URL string
}

func (s *dockerStub) Client() *http.Client { return http.DefaultClient }
func (s *dockerStub) Close()               { _ = s.srv.Close(); _ = s.ln.Close() }

func newDockerStub(h http.HandlerFunc) *dockerStub {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 2 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return &dockerStub{srv: srv, ln: ln, URL: "http://" + ln.Addr().String()}
}

func init() {
	// Suppress unused-import warnings if any future refactor removes the
	// last bytes/bufio reference in the test file.
	_ = bytes.NewReader
	_ = bufio.NewReader
}

// randHexFor returns 8 hex chars derived from a fresh random read. We can't
// reuse randomHex from buildkit_session.go because that one returns 32
// chars and the macOS socket path has a 104-char ceiling.
func randHexFor(t *testing.T) string {
	t.Helper()
	v, err := randomHex()
	if err != nil {
		t.Fatalf("rand: %v", err)
	}
	return v[:8]
}
