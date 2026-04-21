package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestDeployer returns a Deployer whose Docker API requests are routed to
// the given httptest server.
func newTestDeployer(t *testing.T, srv *httptest.Server) *Deployer {
	t.Helper()
	return &Deployer{
		http:       srv.Client(),
		network:    "hatch_public",
		domain:     "localhost",
		notifier:   noopNotifier{},
		dockerBase: srv.URL,
	}
}

func TestPruneOldImages(t *testing.T) {
	t.Parallel()

	const (
		slug       = "itsaam-demo"
		pr         = 7
		currentTag = "hatch-preview-itsaam-demo-7:abcd123"
		oldTag1    = "hatch-preview-itsaam-demo-7:0000001"
		oldTag2    = "hatch-preview-itsaam-demo-7:deadbee"
		otherRepo  = "hatch-preview-itsaam-other-3:999aaaa"
		unrelated  = "nginx:latest"
	)

	var deleted sync.Map

	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/images/json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("images/json method=%s", r.Method)
		}
		payload := []map[string]any{
			{"Id": "img1", "RepoTags": []string{currentTag}},
			{"Id": "img2", "RepoTags": []string{oldTag1}},
			{"Id": "img3", "RepoTags": []string{oldTag2}},
			{"Id": "img4", "RepoTags": []string{otherRepo}},
			{"Id": "img5", "RepoTags": []string{unrelated}},
		}
		_ = json.NewEncoder(w).Encode(payload)
	})
	mux.HandleFunc("/v1.43/images/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method %s on %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Extract the ref after /v1.43/images/ and before the query
		// portion. Paths look like /v1.43/images/<escaped ref>.
		ref := strings.TrimPrefix(r.URL.Path, "/v1.43/images/")
		if ref == currentTag {
			// Simulate docker refusing to delete an image currently in use.
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"image is in use"}`))
			return
		}
		deleted.Store(ref, true)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := newTestDeployer(t, srv)
	if err := d.pruneOldImages(context.Background(), slug, pr, currentTag); err != nil {
		t.Fatalf("pruneOldImages: %v", err)
	}

	wantDeleted := []string{oldTag1, oldTag2}
	for _, tag := range wantDeleted {
		if _, ok := deleted.Load(tag); !ok {
			t.Errorf("expected %s to be deleted", tag)
		}
	}
	for _, tag := range []string{currentTag, otherRepo, unrelated} {
		if _, ok := deleted.Load(tag); ok {
			t.Errorf("tag %s should NOT have been deleted", tag)
		}
	}
}

func TestPruneOldImages_IgnoresInUse(t *testing.T) {
	t.Parallel()

	const currentTag = "hatch-preview-foo-1:abc1234"
	const oldTag = "hatch-preview-foo-1:def5678"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/images/json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"Id": "a", "RepoTags": []string{oldTag}},
		})
	})
	mux.HandleFunc("/v1.43/images/", func(w http.ResponseWriter, _ *http.Request) {
		// Force a 409 on every delete — mimic "image in use" for all.
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"conflict"}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := newTestDeployer(t, srv)
	if err := d.pruneOldImages(context.Background(), "foo", 1, currentTag); err != nil {
		t.Fatalf("pruneOldImages should swallow 409s: %v", err)
	}
}

// --- Reconcile ---------------------------------------------------------------

type fakeStore struct {
	mu sync.Mutex

	active    map[previewKey]bool
	zombies   []previewLocator
	failed    map[string]bool // keyed by repo#pr
	expired   map[string]bool
	expiring  []PreviewRef
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		active:  map[previewKey]bool{},
		failed:  map[string]bool{},
		expired: map[string]bool{},
	}
}

func keyOf(repo string, pr int) string { return fmt.Sprintf("%s#%d", repo, pr) }

func (s *fakeStore) activePreviewKeys(_ context.Context) (map[previewKey]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make(map[previewKey]bool, len(s.active))
	for k, v := range s.active {
		cp[k] = v
	}
	return cp, nil
}

func (s *fakeStore) zombieCandidates(_ context.Context) ([]previewLocator, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]previewLocator(nil), s.zombies...), nil
}

func (s *fakeStore) markFailed(_ context.Context, repo string, pr int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed[keyOf(repo, pr)] = true
	return nil
}

func (s *fakeStore) expiredCandidates(_ context.Context, _ time.Time) ([]PreviewRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]PreviewRef(nil), s.expiring...), nil
}

func (s *fakeStore) markExpired(_ context.Context, repo string, pr int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expired[keyOf(repo, pr)] = true
	return nil
}

func TestReconcile_OrphanContainer(t *testing.T) {
	t.Parallel()

	// Two containers report as hatch.managed:
	//  - hatch-preview-acme-api-4 : NOT in active previews → orphan → must DELETE.
	//  - hatch-preview-acme-api-9 : active → must KEEP.
	var deletedNames sync.Map

	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"Id": "c1", "Names": []string{"/hatch-preview-acme-api-4"}, "Labels": map[string]string{"hatch.managed": "true"}},
			{"Id": "c2", "Names": []string{"/hatch-preview-acme-api-9"}, "Labels": map[string]string{"hatch.managed": "true"}},
		})
	})
	mux.HandleFunc("/v1.43/containers/", func(w http.ResponseWriter, r *http.Request) {
		// Expected: DELETE /v1.43/containers/<name>?force=1&v=1
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method %s on %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/v1.43/containers/")
		deletedNames.Store(name, true)
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := newTestDeployer(t, srv)
	store := newFakeStore()
	store.active[previewKey{slug: "acme-api", pr: 9}] = true

	if err := d.reconcileWithStore(context.Background(), store); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if _, ok := deletedNames.Load("hatch-preview-acme-api-4"); !ok {
		t.Errorf("expected orphan to be deleted")
	}
	if _, ok := deletedNames.Load("hatch-preview-acme-api-9"); ok {
		t.Errorf("active container must NOT be deleted")
	}
}

func TestReconcile_ZombiePreview(t *testing.T) {
	t.Parallel()

	// One preview is reported running in the DB but Docker says 404 for
	// its container → must be flipped to failed.
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})
	mux.HandleFunc("/v1.43/containers/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/json") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := newTestDeployer(t, srv)
	store := newFakeStore()
	store.zombies = []previewLocator{
		{repo: "itsaam/hatch", pr: 42},
	}

	if err := d.reconcileWithStore(context.Background(), store); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if !store.failed[keyOf("itsaam/hatch", 42)] {
		t.Errorf("expected zombie to be marked failed")
	}
}

func TestReconcile_ZombieSkippedWhenContainerExists(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})
	mux.HandleFunc("/v1.43/containers/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/json") {
			_, _ = w.Write([]byte(`{"Id":"abc"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := newTestDeployer(t, srv)
	store := newFakeStore()
	store.zombies = []previewLocator{{repo: "itsaam/hatch", pr: 1}}

	if err := d.reconcileWithStore(context.Background(), store); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if store.failed[keyOf("itsaam/hatch", 1)] {
		t.Errorf("healthy preview should not be marked failed")
	}
}

// --- TTL Reaper --------------------------------------------------------------

type recordingNotifier struct {
	calls atomic.Int32
	last  PreviewRef
	days  int
}

func (r *recordingNotifier) OnStatusChange(context.Context, PreviewRef, string, string) {}

func (r *recordingNotifier) OnHibernated(_ context.Context, ref PreviewRef, days int) {
	r.calls.Add(1)
	r.last = ref
	r.days = days
}

func TestTTLReaper_ExpiresOldPreviews(t *testing.T) {
	t.Parallel()

	// Docker mock accepts GET for listing and DELETE for removal. Listing
	// is used by the stack-aware cleanup path; it returns an empty array
	// (nothing to destroy beyond the legacy single container).
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})
	mux.HandleFunc("/v1.43/containers/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s on %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1.43/networks/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := newTestDeployer(t, srv)
	notifier := &recordingNotifier{}
	d.SetNotifier(notifier)

	store := newFakeStore()
	store.expiring = []PreviewRef{
		{
			Repo:           "itsaam/hatch",
			PR:             99,
			Branch:         "feat/x",
			SHA:            "deadbee",
			InstallationID: 111,
			CommentID:      222,
		},
	}

	ttl := 7 * 24 * time.Hour
	d.reapOnce(context.Background(), store, ttl)

	if !store.expired[keyOf("itsaam/hatch", 99)] {
		t.Fatalf("expected preview to be marked expired")
	}
	if got := notifier.calls.Load(); got != 1 {
		t.Fatalf("want 1 hibernate notify, got %d", got)
	}
	if notifier.days != 7 {
		t.Errorf("want days=7, got %d", notifier.days)
	}
	if notifier.last.PR != 99 {
		t.Errorf("wrong ref notified: %+v", notifier.last)
	}
}

func TestStartTTLReaper_StopsOnContextCancel(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1.43/containers/json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})
	mux.HandleFunc("/v1.43/containers/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/v1.43/networks/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	d := newTestDeployer(t, srv)
	store := newFakeStore()

	ctx, cancel := context.WithCancel(context.Background())
	d.startTTLReaperWithStore(ctx, store, time.Hour, 50*time.Millisecond)

	// Let the goroutine spin up.
	time.Sleep(100 * time.Millisecond)
	cancel()
	// If the reaper didn't exit, this test would leak. We give it a beat
	// and trust -race/-leakcheck infra on CI to catch any regression.
	time.Sleep(100 * time.Millisecond)
}

// --- Parsing helpers ---------------------------------------------------------

func TestParsePreviewName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in       string
		wantSlug string
		wantPR   int
		wantOK   bool
	}{
		{"hatch-preview-acme-api-4", "acme-api", 4, true},
		{"hatch-preview-itsaam-hatch-99", "itsaam-hatch", 99, true},
		{"hatch-preview-nodash", "", 0, false},
		{"other-name", "", 0, false},
		{"hatch-preview-x-", "", 0, false},
		{"hatch-preview-x-abc", "", 0, false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			slug, pr, ok := parsePreviewName(tc.in)
			if ok != tc.wantOK || slug != tc.wantSlug || pr != tc.wantPR {
				t.Fatalf("parsePreviewName(%q)=(%q,%d,%v), want (%q,%d,%v)",
					tc.in, slug, pr, ok, tc.wantSlug, tc.wantPR, tc.wantOK)
			}
		})
	}
}

func TestSlugifyRoundTripsPreviewKey(t *testing.T) {
	t.Parallel()
	// We rely on slugify to produce the same slug the container naming uses.
	got := slugify("Itsaam/Hatch")
	want := "itsaam-hatch"
	if got != want {
		t.Fatalf("slugify: want %q, got %q", want, got)
	}
}
