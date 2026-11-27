package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func signBody(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// newPREvent builds a JSON payload matching prEvent.
func newPREvent(action, baseRepo, headRepo string) []byte {
	obj := map[string]any{
		"action": action,
		"number": 42,
		"pull_request": map[string]any{
			"html_url": "https://github.com/x/y/pull/42",
			"head": map[string]any{
				"ref":  "feature/x",
				"sha":  "deadbeefdeadbeef",
				"repo": map[string]any{"full_name": headRepo},
			},
		},
		"repository":   map[string]any{"full_name": baseRepo},
		"installation": map[string]any{"id": 123},
	}
	b, _ := json.Marshal(obj)
	return b
}

func postWebhook(t *testing.T, h http.HandlerFunc, secret, body []byte) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/github/webhook", bytes.NewReader(body))
	req.Header.Set(signatureHeader, signBody(secret, body))
	req.Header.Set(eventHeader, "pull_request")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec.Result()
}

func TestParseAllowedOwners(t *testing.T) {
	got := parseAllowedOwners(" Alice , bob, ,CHARLIE , ")
	for _, want := range []string{"alice", "bob", "charlie"} {
		if !got[want] {
			t.Errorf("want %q allowed, got %v", want, got)
		}
	}
	if got[""] {
		t.Errorf("empty owner must not be allowed")
	}
	if len(got) != 3 {
		t.Errorf("want 3 owners, got %d: %v", len(got), got)
	}
}

func TestIsForkPR(t *testing.T) {
	tests := []struct {
		name, base, head string
		want             bool
	}{
		{"same repo → not fork", "itsaam/hatch", "itsaam/hatch", false},
		{"fork", "itsaam/hatch", "attacker/hatch", true},
		{"case-insensitive same", "ItsAam/Hatch", "itsaam/hatch", false},
		{"deleted fork (empty head) → treat as fork", "itsaam/hatch", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev := prEvent{}
			ev.Repository.FullName = tc.base
			ev.PullRequest.Head.Repo.FullName = tc.head
			if got := isForkPR(ev); got != tc.want {
				t.Fatalf("isForkPR = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestWebhookAuthz exercises the handler's authorization gates without
// touching Postgres or the Docker daemon. We only need the signature +
// parsed event to reach the gates, then assert we never fall through.
func TestWebhookAuthz(t *testing.T) {
	secret := []byte("testsecret")
	allowed := parseAllowedOwners("itsaam")

	// The handler passes nil pool/deployer/app. We rely on the gates
	// returning *before* any of those are dereferenced.
	h := githubWebhookHandler(nil, secret, nil, nil, allowed, "hatchpr", false)

	tests := []struct {
		name       string
		base, head string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "unknown owner → 202 skipped",
			base:       "attacker/evil",
			head:       "attacker/evil",
			wantStatus: http.StatusAccepted,
			wantBody:   "unauthorized owner",
		},
		{
			name:       "case-insensitive owner match → fork-check still applies, here same → 202 no, fork PR → 202 skipped",
			base:       "ItsAam/repo",
			head:       "attacker/repo",
			wantStatus: http.StatusAccepted,
			wantBody:   "fork PR",
		},
		{
			name:       "allowed owner but fork PR → 202 skipped",
			base:       "itsaam/hatch-demo",
			head:       "attacker/hatch-demo",
			wantStatus: http.StatusAccepted,
			wantBody:   "fork PR",
		},
		{
			name:       "malformed repo name (no slash) → 202 skipped",
			base:       "malformed",
			head:       "malformed",
			wantStatus: http.StatusAccepted,
			wantBody:   "unauthorized owner",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := newPREvent("opened", tc.base, tc.head)
			resp := postWebhook(t, h, secret, body)
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if tc.wantBody != "" {
				b, _ := io.ReadAll(resp.Body)
				if !bytes.Contains(b, []byte(tc.wantBody)) {
					t.Fatalf("body %q missing %q", string(b), tc.wantBody)
				}
			}
		})
	}
}

func TestWebhookAuthz_BadSig(t *testing.T) {
	secret := []byte("testsecret")
	allowed := parseAllowedOwners("itsaam")
	h := githubWebhookHandler(nil, secret, nil, nil, allowed, "hatchpr", false)

	body := newPREvent("opened", "itsaam/hatch-demo", "itsaam/hatch-demo")
	req := httptest.NewRequest(http.MethodPost, "/api/github/webhook", bytes.NewReader(body))
	req.Header.Set(signatureHeader, "sha256=deadbeef")
	req.Header.Set(eventHeader, "pull_request")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad sig must 401, got %d", rec.Code)
	}
}

func TestWebhookAuthz_WrongEvent(t *testing.T) {
	secret := []byte("testsecret")
	allowed := parseAllowedOwners("itsaam")
	h := githubWebhookHandler(nil, secret, nil, nil, allowed, "hatchpr", false)

	body := []byte(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/github/webhook", bytes.NewReader(body))
	req.Header.Set(signatureHeader, signBody(secret, body))
	req.Header.Set(eventHeader, "issues")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unrelated event must 200, got %d", rec.Code)
	}
}

func TestWebhookOwnerCommandOnly(t *testing.T) {
	secret := []byte("testsecret")
	h := githubWebhookHandler(nil, secret, nil, nil, parseAllowedOwners("itsaam"), "hatchpr", true)
	for _, action := range []string{"opened", "reopened", "synchronize"} {
		t.Run(action, func(t *testing.T) {
			body := newPREvent(action, "itsaam/demo", "itsaam/demo")
			resp := postWebhook(t, h, secret, body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s = %d, want 200 without a deployer", action, resp.StatusCode)
			}
		})
	}

	body := []byte(`{
		"action":"created","installation":{"id":123},"repository":{"full_name":"itsaam/demo"},
		"issue":{"number":42,"pull_request":{},"user":{"login":"collaborator"}},
		"comment":{"id":1,"body":"@hatchpr preview","user":{"login":"collaborator","type":"User"}}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/github/webhook", bytes.NewReader(body))
	req.Header.Set(signatureHeader, signBody(secret, body))
	req.Header.Set(eventHeader, "issue_comment")
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusAccepted || !bytes.Contains(rec.Body.Bytes(), []byte("owner command required")) {
		t.Fatalf("non-owner command = %d %q, want 202 owner command required", rec.Code, rec.Body.String())
	}
}

func TestOwnerPreviewRequiresCurrentCommit(t *testing.T) {
	const current = "f2daa6382a9064a8c1086b5f669f113eb84cf719"
	const previous = "a7cafdd5af8e7ccbf70b1efdb9d5f85a06bcc796"
	for _, tc := range []struct {
		name, command, reply string
	}{
		{"owner preview requires a SHA", "@hatchpr preview", "@hatchpr preview " + current},
		{"older explicit SHA is rejected", "@hatchpr preview " + previous, "no longer the head"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replies := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/itsaam/demo/pulls/42":
					fmt.Fprintf(w, `{"state":"open","user":{"login":"itsaam"},"head":{"sha":%q,"ref":"test","repo":{"full_name":"itsaam/demo"}}}`, current)
				case "/repos/itsaam/demo/issues/42/comments":
					var comment struct{ Body string }
					if err := json.NewDecoder(r.Body).Decode(&comment); err != nil {
						t.Error(err)
					}
					replies <- comment.Body
					w.WriteHeader(http.StatusCreated)
					io.WriteString(w, `{"id":123}`)
				default:
					t.Errorf("unexpected request to %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			secret := []byte("testsecret")
			app := newTestAppClient(t)
			app.baseURL = server.URL
			app.tokens[123] = cachedToken{token: "test-token", expiresAt: time.Now().Add(time.Hour)}
			h := githubWebhookHandler(nil, secret, nil, app, parseAllowedOwners("itsaam"), "hatchpr", true)
			body := []byte(fmt.Sprintf(`{
				"action":"created","installation":{"id":123},"repository":{"full_name":"itsaam/demo"},
				"issue":{"number":42,"pull_request":{},"user":{"login":"itsaam"}},
				"comment":{"id":1,"body":%q,"user":{"login":"itsaam","type":"User"}}
			}`, tc.command))
			req := httptest.NewRequest(http.MethodPost, "/api/github/webhook", bytes.NewReader(body))
			req.Header.Set(signatureHeader, signBody(secret, body))
			req.Header.Set(eventHeader, "issue_comment")
			rec := httptest.NewRecorder()
			h(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			select {
			case reply := <-replies:
				if !bytes.Contains([]byte(reply), []byte(tc.reply)) {
					t.Fatalf("reply %q does not contain %q", reply, tc.reply)
				}
			default:
				t.Fatal("expected Hatch to reply without queuing a build")
			}
		})
	}
}
