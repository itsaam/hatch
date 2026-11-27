package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestKubeClusterPreview(t *testing.T) {
	ingressURL := os.Getenv("HATCH_TEST_INGRESS_URL")
	if ingressURL == "" {
		t.Skip("set HATCH_TEST_INGRESS_URL and configure the Kubernetes client for a dedicated test slot")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	kube, err := newKubeRuntime()
	if err != nil {
		t.Fatal(err)
	}
	ref := PreviewRef{Repo: "hatch-test/fixtures", PR: int(time.Now().Unix() % 1_000_000), SHA: strings.Repeat("a", 40)}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := kube.destroy(ctx, ref); err != nil {
			t.Error(err)
		}
	})
	spec := &ComposeSpec{
		Version: 1,
		Services: map[string]*ComposeService{
			"db": {
				Image: "postgres:16-alpine", Env: map[string]string{"POSTGRES_USER": "hatch", "POSTGRES_DB": "hatch", "POSTGRES_PASSWORD": "hatch-fixture"},
				Healthcheck: &ComposeHealthcheck{Cmd: "pg_isready -h 127.0.0.1 -U hatch -d hatch", IntervalSeconds: 2, Retries: 10},
			},
			"web": {Image: "nginx:alpine", Port: 80, Expose: true, DependsOn: []string{"db"},
				Healthcheck: &ComposeHealthcheck{Cmd: "nslookup db >/dev/null && wget -q -O /dev/null http://127.0.0.1/", IntervalSeconds: 2, Retries: 10}},
		},
		Seed: &ComposeSeed{After: "db", SQL: "seed.sql"},
	}
	host := kubePreviewHost(ref, "hatch-test.invalid")
	if err := kube.deploy(ctx, ref, spec, host, func(context.Context, string, *ComposeService) (string, int, error) {
		return "", 0, fmt.Errorf("image-only fixture tried to build a Dockerfile")
	}, func(context.Context, string) ([]byte, error) {
		return []byte("CREATE TABLE hatch_fixture (id integer);"), nil
	}); err != nil {
		t.Fatal(err)
	}
	if ok, err := kube.exists(ctx, ref); err != nil || !ok {
		t.Fatalf("deployed preview missing: %t, %v", ok, err)
	}

	client := &http.Client{Timeout: 3 * time.Second}
	served := false
	for i := 0; i < 20 && !served; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ingressURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		if response, err := client.Do(req); err == nil {
			body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
			response.Body.Close()
			served = err == nil && response.StatusCode == http.StatusOK && strings.Contains(string(body), "Welcome to nginx!")
		}
		if !served {
			time.Sleep(time.Second)
		}
	}
	if !served {
		t.Fatalf("ingress did not serve %s", host)
	}
	if logs, err := kube.logs(ctx, ref, 30); err != nil || !strings.Contains(logs, `GET /`) {
		t.Fatalf("preview logs not available: %q, %v", logs, err)
	}
	if err := kube.destroy(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if ok, err := kube.exists(ctx, ref); err != nil || ok {
		t.Fatalf("deleted preview still exists: %t, %v", ok, err)
	}
}
