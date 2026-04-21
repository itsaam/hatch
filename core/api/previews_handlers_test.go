package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// --- demuxDockerStream -----------------------------------------------------

func TestDemuxDockerStream_SingleFrame(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	payload := []byte("hello world\n")
	hdr := make([]byte, 8)
	hdr[0] = 1 // stdout
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(payload)))
	buf.Write(hdr)
	buf.Write(payload)

	got, err := demuxDockerStream(&buf, 1024)
	if err != nil {
		t.Fatalf("demux: %v", err)
	}
	if got != string(payload) {
		t.Errorf("got %q, want %q", got, payload)
	}
}

func TestDemuxDockerStream_MultipleFramesMixedStreams(t *testing.T) {
	t.Parallel()

	frames := []struct {
		kind    byte
		payload string
	}{
		{1, "line stdout 1\n"},
		{2, "line stderr 1\n"},
		{1, "line stdout 2\n"},
	}
	var buf bytes.Buffer
	var want strings.Builder
	for _, f := range frames {
		hdr := make([]byte, 8)
		hdr[0] = f.kind
		binary.BigEndian.PutUint32(hdr[4:8], uint32(len(f.payload)))
		buf.Write(hdr)
		buf.WriteString(f.payload)
		want.WriteString(f.payload)
	}

	got, err := demuxDockerStream(&buf, 1024)
	if err != nil {
		t.Fatalf("demux: %v", err)
	}
	if got != want.String() {
		t.Errorf("got %q, want %q", got, want.String())
	}
}

func TestDemuxDockerStream_EmptyStream(t *testing.T) {
	t.Parallel()
	got, err := demuxDockerStream(bytes.NewReader(nil), 1024)
	if err != nil {
		t.Fatalf("demux empty: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// --- parsePreviewPath validation -------------------------------------------

func TestParsePreviewPath_Rejects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		owner, repo    string
		pr             string
		wantStatus     int
	}{
		{"bad owner slash", "bad/owner", "repo", "1", http.StatusBadRequest},
		{"bad repo quote", "owner", `r"epo`, "1", http.StatusBadRequest},
		{"pr zero", "owner", "repo", "0", http.StatusBadRequest},
		{"pr negative", "owner", "repo", "-3", http.StatusBadRequest},
		{"pr non-numeric", "owner", "repo", "abc", http.StatusBadRequest},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			// Build a chi RouteContext so URLParam returns our values.
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("owner", c.owner)
			rctx.URLParams.Add("repo", c.repo)
			rctx.URLParams.Add("pr", c.pr)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			rr := httptest.NewRecorder()

			_, _, ok := parsePreviewPath(rr, req)
			if ok {
				t.Fatalf("expected parse failure, ok=true")
			}
			if rr.Code != c.wantStatus {
				t.Errorf("status=%d want %d", rr.Code, c.wantStatus)
			}
		})
	}
}

// --- Route-level auth check (confirms requireAdminToken is wired) ----------

func TestPreviewsRoutes_Unauthorized(t *testing.T) {
	// Not parallel: t.Setenv.
	t.Setenv("HATCH_ADMIN_TOKEN", "sekret")

	r := chi.NewRouter()
	r.Route("/api/previews", func(r chi.Router) {
		r.Use(requireAdminToken)
		r.Get("/", listPreviewsHandler(nil))
		r.Get("/{owner}/{repo}/{pr}/logs", previewLogsHandler(nil, nil))
		r.Post("/{owner}/{repo}/{pr}/redeploy", previewRedeployHandler(nil, nil))
		r.Delete("/{owner}/{repo}/{pr}", previewDestroyHandler(nil, nil))
	})

	cases := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/previews/"},
		{http.MethodGet, "/api/previews/acme/widget/42/logs"},
		{http.MethodPost, "/api/previews/acme/widget/42/redeploy"},
		{http.MethodDelete, "/api/previews/acme/widget/42"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			req := httptest.NewRequest(c.method, c.path, nil)
			// No Authorization header.
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != http.StatusUnauthorized {
				t.Errorf("status=%d want 401", rr.Code)
			}
		})
	}
}

