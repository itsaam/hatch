package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type kubeTestTransport func(*http.Request) (*http.Response, error)

func (f kubeTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type kubeTestAPI struct {
	t           *testing.T
	mu          sync.Mutex
	objects     map[string]kubeFields
	calls       []string
	uid         int
	holdPods    bool
	onPodList   func()
	logStatus   int
	failCreate  string
	delayedPath string
	delayed     kubeFields
}

func newKubeTestRuntime(t *testing.T) (*kubeRuntime, *kubeTestAPI) {
	t.Helper()
	api := &kubeTestAPI{t: t, objects: make(map[string]kubeFields)}
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("first-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &kubeRuntime{
		namespaces: []string{"slot-a"}, ingressClass: "traefik", tlsSecret: "wildcard-tls", imagePullSecret: "pull-auth",
		client: &kubeClient{
			baseURL: "https://cluster.test", tokenPath: tokenPath,
			http: &http.Client{Transport: kubeTestTransport(api.serve)},
		},
	}, api
}

func (a *kubeTestAPI) serve(r *http.Request) (*http.Response, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := r.URL.Path
	a.calls = append(a.calls, r.Method+" "+p)
	reply := func(code int, value any) (*http.Response, error) {
		var data []byte
		if plain, ok := value.(string); ok {
			data = []byte(plain)
		} else {
			var err error
			data, err = json.Marshal(value)
			if err != nil {
				return nil, err
			}
		}
		return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	}
	switch r.Method {
	case http.MethodPost:
		var body kubeFields
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			return nil, err
		}
		meta := body["metadata"].(map[string]any)
		path := p + "/" + meta["name"].(string)
		if _, present := a.objects[path]; present {
			return reply(409, nil)
		}
		a.uid++
		meta["uid"] = fmt.Sprintf("uid-%d", a.uid)
		if p == a.failCreate {
			a.delayedPath, a.delayed = path, body
			return reply(500, "private server diagnostic")
		}
		a.objects[path] = body
		if body["kind"] == "Deployment" {
			body["status"] = kubeFields{"availableReplicas": 1}
			name := meta["name"].(string) + "-pod"
			pods := kubePods.path("slot-a", name)
			a.objects[pods] = kubeFields{
				"metadata": kubeFields{"name": name, "uid": meta["uid"].(string) + "-pod", "labels": meta["labels"]},
			}
		}
		if body["kind"] == "Job" {
			body["status"] = kubeFields{"conditions": []kubeFields{{"type": "Complete", "status": "True"}}}
		}
		return reply(201, body)

	case http.MethodGet:
		if strings.HasSuffix(p, "/log") {
			if a.logStatus != 0 {
				return reply(a.logStatus, "private server diagnostic")
			}
			if r.Header.Get("Authorization") != "Bearer rotated-token" || r.URL.Query().Get("tailLines") != "500" {
				return nil, fmt.Errorf("log request has unexpected token or tail")
			}
			return reply(200, "app ready")
		}
		selector := r.URL.Query().Get("labelSelector")
		if selector == "" {
			body, ok := a.objects[p]
			if !ok {
				return reply(404, nil)
			}
			return reply(200, body)
		}
		if p == kubePods.path("slot-a") && a.onPodList != nil {
			a.onPodList()
		}
		key, uid, exact := strings.Cut(selector, "=")
		var items []kubeFields
		for path, object := range a.objects {
			if !strings.HasPrefix(path, p+"/") || strings.Contains(strings.TrimPrefix(path, p+"/"), "/") {
				continue
			}
			meta := object["metadata"].(map[string]any)
			labels, _ := meta["labels"].(map[string]any)
			if claim, ok := labels[key]; ok && (!exact || claim == uid) {
				items = append(items, kubeFields{"metadata": meta})
			}
		}
		return reply(200, kubeFields{"items": items})

	case http.MethodDelete:
		object, ok := a.objects[p]
		if !ok {
			return reply(404, nil)
		}
		var options struct {
			PropagationPolicy string
			Preconditions     struct{ UID string }
		}
		if err := json.NewDecoder(r.Body).Decode(&options); err != nil {
			return nil, err
		}
		meta := object["metadata"].(map[string]any)
		policy := "Foreground"
		if name := meta["name"].(string); strings.HasPrefix(name, kubePendingName+".") || strings.HasPrefix(name, kubeSeedPendingName+".") {
			policy = "Background"
		}
		if meta["uid"] != options.Preconditions.UID || options.PropagationPolicy != policy {
			a.t.Error("delete must use the observed UID and the resource's propagation policy")
			return reply(409, nil)
		}
		if object["kind"] == "Deployment" && !a.holdPods {
			delete(a.objects, kubePods.path("slot-a", meta["name"].(string)+"-pod"))
		}
		delete(a.objects, p)
		return reply(200, nil)
	}
	return reply(405, nil)
}

func TestKubeRuntimePreviewLifecycle(t *testing.T) {
	runtime, api := newKubeTestRuntime(t)
	spec, err := ParseCompose([]byte(`version: 1
services:
  web:
    build: .
    expose: true
    depends_on: [db, worker]
  worker:
    image: alpine:3.22
  db:
    image: postgres:16-alpine
    env:
      POSTGRES_PASSWORD: preview-secret
    healthcheck:
      cmd: pg_isready
seed:
  after: db
  sql: seed.sql
`))
	if err != nil {
		t.Fatal(err)
	}
	ref := PreviewRef{Repo: "acme/one", PR: 1, SHA: "revision-one"}
	ctx := t.Context()
	preprovisioned := kubeSecrets.path("slot-a", "db")
	api.objects[preprovisioned] = kubeFields{"metadata": kubeFields{"name": "db", "uid": "preprovisioned-uid"}}
	err = runtime.deploy(ctx, ref, spec, "pr-1.preview.example.com", func(_ context.Context, name string, _ *ComposeService) (string, int, error) {
		if name != "web" {
			t.Errorf("unexpected build for %s", name)
		}
		return "registry.test:5000/acme/web:revision-one", 3000, nil
	}, func(_ context.Context, path string) ([]byte, error) {
		if path != "seed.sql" {
			t.Errorf("unexpected seed path %s", path)
		}
		return []byte("SELECT 1;"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slots, _, err := runtime.locate(ctx, ref)
	if err != nil || len(slots) != 1 {
		t.Fatalf("locate deployed preview: %v", err)
	}
	slot := slots[0]
	calls := strings.Join(api.calls, "\n")
	readyDB := strings.Index(calls, "GET "+kubeDeployments.path("slot-a", "db"))
	seedDone := strings.Index(calls, "GET "+kubeJobs.path("slot-a", kubeSeedName))
	readyWorker := strings.Index(calls, "GET "+kubeDeployments.path("slot-a", "worker"))
	readyWeb := strings.Index(calls, "GET "+kubeDeployments.path("slot-a", "web"))
	ingress := strings.Index(calls, "POST "+kubeIngresses.path("slot-a"))
	if readyDB < 0 || seedDone <= readyDB || readyWorker <= seedDone || readyWeb <= readyWorker || ingress <= readyWeb {
		t.Fatalf("resources were created out of dependency order:\n%s", calls)
	}
	service := api.objects[kubeServices.path("slot-a", "db")]["spec"].(map[string]any)
	port := service["ports"].([]any)[0].(map[string]any)["port"]
	if port != float64(5432) {
		t.Fatalf("expected db:5432, got %v", port)
	}
	for _, name := range []string{"db", "web", "worker"} {
		deploy := api.objects[kubeDeployments.path("slot-a", name)]
		pod := deploy["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
		container := pod["containers"].([]any)[0].(map[string]any)
		_, hasProbe := container["readinessProbe"]
		_, hasStartup := container["startupProbe"]
		if hasProbe != (name != "worker") || hasStartup != (name == "db") || pod["automountServiceAccountToken"] != false || pod["enableServiceLinks"] != false {
			t.Errorf("unexpected readiness or security configuration for %s: %v", name, pod)
		}
	}
	if present, err := runtime.exists(ctx, ref); !present || err != nil {
		t.Fatalf("preview exists = %v, %v", present, err)
	}
	if err := os.WriteFile(runtime.client.tokenPath, []byte("rotated-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := runtime.logs(ctx, ref, 900); output != "app ready" || err != nil || !strings.HasSuffix(api.calls[len(api.calls)-1], "/pods/web-pod/log") {
		t.Fatalf("expected public pod logs, got %q, %v", output, err)
	}
	api.logStatus = 403
	if _, err := runtime.logs(ctx, ref, 500); err == nil || strings.Contains(err.Error(), "private server diagnostic") {
		t.Fatalf("logs leaked an API response body: %v", err)
	}
	stale := ref
	stale.SHA = "older-revision"
	if err := runtime.destroy(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if _, ok := api.objects[kubeDeployments.path("slot-a", "web")]; !ok {
		t.Fatal("stale SHA destroyed the current deployment")
	}

	// The controllers are gone, but Kubernetes can still be draining pods.
	// Cancel that cleanup pass and verify a different preview cannot claim the slot.
	drainCtx, cancel := context.WithCancel(ctx)
	api.holdPods = true
	api.onPodList = cancel
	if err := runtime.destroy(drainCtx, ref); err == nil {
		t.Fatal("destroy succeeded while pods were still present")
	}
	if _, ok := api.objects[kubeSecrets.path("slot-a", slot.envSecretName("db"))]; !ok {
		t.Fatal("db secret removed before its pod finished draining")
	}
	if _, ok := api.objects[kubeConfigMaps.path("slot-a", kubeClaimName)]; !ok {
		t.Fatal("claim released before the namespace drained")
	}
	next := PreviewRef{Repo: "acme/two", PR: 2, SHA: "revision-two"}
	if _, err := runtime.claimSlot(ctx, next); err == nil {
		t.Fatal("another preview acquired a draining namespace")
	}

	api.onPodList = nil
	for _, name := range []string{"db", "web", "worker"} {
		delete(api.objects, kubePods.path("slot-a", name+"-pod"))
	}
	if err := runtime.destroy(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if len(api.objects) != 1 || api.objects[preprovisioned] == nil {
		t.Fatal("cleanup must leave only the preprovisioned Secret")
	}
	if _, err := runtime.claimSlot(ctx, next); err != nil {
		t.Fatal(err)
	}
}

func TestKubeRuntimeClaimIsAtomic(t *testing.T) {
	runtime, _ := newKubeTestRuntime(t)
	results := make(chan error, 2)
	for pr := 1; pr <= 2; pr++ {
		go func() {
			_, err := runtime.claimSlot(t.Context(), PreviewRef{Repo: "acme/race", PR: pr, SHA: "revision"})
			results <- err
		}()
	}
	first, second := <-results, <-results
	if (first == nil) == (second == nil) {
		t.Fatalf("expected one claim winner, got %v and %v", first, second)
	}
}

func TestKubeRuntimeReconcilesLateClaim(t *testing.T) {
	runtime, api := newKubeTestRuntime(t)
	ref := PreviewRef{Repo: "acme/one", PR: 1, SHA: "new-revision"}
	spec := &ComposeSpec{Version: 1, Services: map[string]*ComposeService{
		"worker": {Image: "alpine:3.22"},
	}}
	if err := runtime.deploy(t.Context(), ref, spec, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	// An earlier claim POST was interrupted, then arrived after the retry
	// deployed the preview in another namespace.
	runtime.namespaces = []string{"slot-b", "slot-a"}
	old := ref
	old.SHA = "old-revision"
	api.objects[kubeConfigMaps.path("slot-b", kubeClaimName)] = kubeFields{
		"metadata": kubeFields{"name": kubeClaimName, "uid": "late-uid"},
		"data":     kubeClaimData(old),
	}
	lookup := ref
	lookup.SHA = ""
	if present, err := runtime.exists(t.Context(), lookup); !present || err != nil {
		t.Fatalf("an unused late claim hid a live deployment: %v, %v", present, err)
	}
	if err := runtime.destroy(t.Context(), ref); err != nil || len(api.objects) != 0 {
		t.Fatalf("destroy did not reconcile both claims: %v", err)
	}
}

func TestKubeRuntimeKeepsClaimAfterUnknownCreate(t *testing.T) {
	runtime, api := newKubeTestRuntime(t)
	api.failCreate = kubeDeployments.path("slot-a")
	ref := PreviewRef{Repo: "acme/one", PR: 1, SHA: "revision"}
	spec := &ComposeSpec{Version: 1, Services: map[string]*ComposeService{
		"worker": {Image: "alpine:3.22"},
	}}
	err := runtime.deploy(t.Context(), ref, spec, "", nil, nil)
	if err == nil || strings.Contains(err.Error(), "private server diagnostic") {
		t.Fatalf("expected sanitized deployment error, got %v", err)
	}
	if !strings.HasSuffix(api.calls[len(api.calls)-1], "/deployments") {
		t.Fatal("destructive recovery attempted after an indeterminate create")
	}
	if _, exists := api.objects[kubeConfigMaps.path("slot-a", kubeClaimName)]; !exists {
		t.Fatal("namespace was released after an indeterminate create")
	}
	// A coordinator restart must preserve the same decision until the
	// API shows that the original create has settled.
	restarted := &kubeRuntime{client: runtime.client, namespaces: runtime.namespaces}
	if err := restarted.destroy(t.Context(), ref); err == nil || !strings.Contains(err.Error(), "remains reserved") {
		t.Fatalf("expected unresolved cleanup, got %v", err)
	}
	next := PreviewRef{Repo: "acme/two", PR: 2, SHA: "revision-two"}
	if _, err := restarted.claimSlot(t.Context(), next); err == nil {
		t.Fatal("another preview acquired a namespace with an unresolved create")
	}
	api.objects[api.delayedPath] = api.delayed
	if err := restarted.destroy(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	if len(api.objects) != 0 {
		t.Fatal("late deployment or its claim survived confirmed cleanup")
	}
	if _, err := restarted.claimSlot(t.Context(), next); err != nil {
		t.Fatal(err)
	}
}

func TestKubeImageReference(t *testing.T) {
	for _, image := range []string{
		"postgres:16-alpine", "docker.io/library/redis", "localhost:5000/a_b/web:dev",
		"[2001:db8::1]:5000/web", "ghcr.io/a/web:dev@sha256:" + strings.Repeat("ab", 32),
	} {
		if !validKubeImage(image) {
			t.Errorf("valid image rejected: %s", image)
		}
	}
	for _, image := range []string{
		"docker://postgres", "user@registry.test/image", "ghcr.io/a/WEB", "redis ",
		"registry.test:65536/web", "redis@sha256:abcd", "alpine;echo",
	} {
		if validKubeImage(image) {
			t.Errorf("invalid image accepted: %s", image)
		}
	}
}
