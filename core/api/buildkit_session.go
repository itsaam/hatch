// BuildKit gRPC session support.
//
// When the feature flag HATCH_BUILDKIT_SESSION=1 is set, build() opens a real
// BuildKit session against the Docker daemon before kicking off /build, and
// passes its session ID through the build request. This unlocks two things
// that the raw HTTP path cannot offer:
//
//  1. Build secrets via `RUN --mount=type=secret,id=foo` — the Dockerfile
//     reads from /run/secrets/foo at build time, the value never lands in a
//     layer.
//  2. The session establishes a control channel that BuildKit uses to surface
//     a richer trace stream. Cache mounts (RUN --mount=type=cache,target=...)
//     work without a session in BuildKit v2; this implementation does NOT add
//     a new cache layer cache shared between PRs — it only enables session
//     features. See the long comment in (Deployer).build for the rationale on
//     cache invalidation.
//
// We deliberately import only `session` and `secretsprovider` from
// github.com/moby/buildkit. We do NOT import `client.Client` (the high-level
// BuildKit client) — driving /build over raw HTTP keeps Hatch's existing
// streaming-log code path unchanged. We also skip `authprovider` because base
// images are already pre-pulled by ensureBaseImagesForService before any
// build runs.
//
// Cache invalidation note: enabling sessions does NOT change how BuildKit
// hashes layers. Layer cache keys are still derived from file content and
// instruction text in the Dockerfile, so adding a new dependency in a
// lockfile (package-lock.json, go.sum, requirements.txt, Gemfile.lock) still
// busts the relevant layer. Cache mounts (when the user opts into them in
// their Dockerfile) live OUTSIDE the layer cache — they store package
// download caches, not install results — so they cannot serve a stale binary.

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
)

// buildKitSessionEnabled reports whether the feature flag is on.
//
// Default: OFF. Setting HATCH_BUILDKIT_SESSION=1|true|yes turns it on. Any
// other value (including unset) leaves the legacy raw-HTTP build path
// unchanged. Off is safe — the worst that happens is `--mount=type=secret`
// Dockerfiles fail with the same error they fail with today.
func buildKitSessionEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("HATCH_BUILDKIT_SESSION"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// buildSecret is one (id, value) pair exposed to RUN --mount=type=secret.
type buildSecret struct {
	ID    string
	Value []byte
}

// newBuildSession constructs a BuildKit session pre-loaded with the given
// build secrets. The caller is responsible for calling Run() in a goroutine
// and Close() when the build returns.
//
// secrets may be nil. Returns the *session.Session and its short
// (32-char hex) id, suitable to pass to Docker /build via the `session`
// query parameter.
func newBuildSession(ctx context.Context, secrets []buildSecret) (*session.Session, string, error) {
	s, err := session.NewSession(ctx, "")
	if err != nil {
		return nil, "", fmt.Errorf("buildkit: new session: %w", err)
	}
	if len(secrets) > 0 {
		store := newStaticSecretStore(secrets)
		s.Allow(secretsprovider.NewSecretProvider(store))
	}
	return s, s.ID(), nil
}

// staticSecretStore implements buildkit's secrets.SecretStore over an
// in-memory map. Lookup is O(1); we never log values. Used by
// secretsprovider.NewSecretProvider.
type staticSecretStore struct {
	mu sync.RWMutex
	m  map[string][]byte
}

func newStaticSecretStore(secrets []buildSecret) *staticSecretStore {
	store := &staticSecretStore{m: make(map[string][]byte, len(secrets))}
	for _, s := range secrets {
		// Defensive copy — caller may zero its slice once Run returns.
		buf := make([]byte, len(s.Value))
		copy(buf, s.Value)
		store.m[s.ID] = buf
	}
	return store
}

// GetSecret implements secretsprovider.SecretStore. Returning os.ErrNotExist
// is what the upstream provider expects for unknown ids — it surfaces a
// clean "secret not available" error to the build instead of a 500.
func (s *staticSecretStore) GetSecret(_ context.Context, id string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[id]
	if !ok {
		return nil, fmt.Errorf("buildkit secret %q: %w", id, os.ErrNotExist)
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, nil
}

// dockerSessionDialer returns a session.Dialer that opens an HTTP/1.1
// Upgrade request against /session on the Docker daemon's Unix socket and
// hijacks the underlying connection so BuildKit's gRPC server can speak
// h2c on it.
//
// It mirrors what the official Docker SDK does in client/session.go but
// stays standalone: we don't import docker/docker.
func dockerSessionDialer(socketPath string) session.Dialer {
	return func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
		var d net.Dialer
		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		conn, err := d.DialContext(dialCtx, "unix", socketPath)
		if err != nil {
			return nil, fmt.Errorf("buildkit dial %s: %w", socketPath, err)
		}

		// Build the HTTP/1.1 Upgrade request manually. We can't reuse the
		// shared http.Client because we need ownership of the raw conn after
		// the 101 — http.Client closes it on response read.
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/session", nil)
		if err != nil {
			conn.Close()
			return nil, err
		}
		req.Host = "docker"
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", proto)
		for k, vals := range meta {
			for _, v := range vals {
				req.Header.Add(k, v)
			}
		}
		if err := req.Write(conn); err != nil {
			conn.Close()
			return nil, fmt.Errorf("buildkit write upgrade: %w", err)
		}

		// Read the 101 response off the wire ourselves.
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, req)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("buildkit read upgrade resp: %w", err)
		}
		if resp.StatusCode != http.StatusSwitchingProtocols {
			conn.Close()
			return nil, fmt.Errorf("buildkit upgrade: docker replied %s (want 101)", resp.Status)
		}

		// If the bufio.Reader has already pulled some bytes off the wire we
		// must not lose them. Wrap conn in a hijackedConn that drains the
		// reader first.
		if br.Buffered() > 0 {
			peek, _ := br.Peek(br.Buffered())
			conn = &hijackedConn{Conn: conn, prefix: append([]byte(nil), peek...)}
		}
		return conn, nil
	}
}

// hijackedConn replays bytes that were buffered before the connection was
// handed back to BuildKit. Once prefix is drained it transparently delegates
// to the underlying net.Conn.
type hijackedConn struct {
	net.Conn
	prefix []byte
}

func (h *hijackedConn) Read(p []byte) (int, error) {
	if len(h.prefix) > 0 {
		n := copy(p, h.prefix)
		h.prefix = h.prefix[n:]
		return n, nil
	}
	return h.Conn.Read(p)
}

// runBuildSession launches the session against the Docker daemon and returns
// a stop function. The session runs until stop() is called or ctx is
// cancelled. stop() blocks until Run returns so the daemon-side state is
// fully cleaned up before /build is finalised.
func runBuildSession(ctx context.Context, sess *session.Session, socketPath string) (stop func(), err error) {
	if sess == nil {
		return func() {}, nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	dialer := dockerSessionDialer(socketPath)

	done := make(chan error, 1)
	go func() {
		done <- sess.Run(runCtx, dialer)
	}()

	stop = func() {
		_ = sess.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			// Daemon is wedged. We've already cancelled — leak the goroutine
			// rather than block the deploy.
		}
	}
	return stop, nil
}

// loadBuildSecretsForRepo returns the build secrets that should be exposed
// via --mount=type=secret. Source: HATCH_BUILD_SECRETS, a comma-separated
// list of "id=ENV_VAR" pairs. Empty / unset means "no build secrets".
//
// Example:
//
//	HATCH_BUILD_SECRETS=npm_token=NPM_TOKEN,sentry=SENTRY_AUTH_TOKEN
//
// We deliberately keep build secrets out of the per-repo secret store: the
// per-repo store injects values as plain env vars at runtime, which would
// leak into the runtime container. Build secrets must be runtime-invisible.
func loadBuildSecretsForRepo() []buildSecret {
	raw := strings.TrimSpace(os.Getenv("HATCH_BUILD_SECRETS"))
	if raw == "" {
		return nil
	}
	var out []buildSecret
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eq := strings.IndexByte(pair, '=')
		if eq <= 0 || eq == len(pair)-1 {
			continue
		}
		id := strings.TrimSpace(pair[:eq])
		envName := strings.TrimSpace(pair[eq+1:])
		if id == "" || envName == "" {
			continue
		}
		v := os.Getenv(envName)
		if v == "" {
			continue
		}
		out = append(out, buildSecret{ID: id, Value: []byte(v)})
	}
	return out
}

// dockerSocketPath returns the path the deployer dials. We keep it
// hard-coded to match NewDeployer's transport, but expose the indirection
// here so tests can override it without touching the http.Transport.
const dockerSocketPath = "/var/run/docker.sock"

// randomHex returns a 16-byte hex string. Used as a fallback shared key when
// callers don't care about cross-build cache sharing, which is our case
// (every PR build is independent).
func randomHex() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", errors.New("buildkit: crypto rand failed")
	}
	return hex.EncodeToString(b[:]), nil
}
