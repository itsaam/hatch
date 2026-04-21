package main

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestParseCompose_Minimal(t *testing.T) {
	t.Parallel()

	data := []byte(`
version: 1
services:
  web:
    build: .
    expose: true
`)
	spec, err := ParseCompose(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if spec.Version != 1 {
		t.Errorf("version=%d", spec.Version)
	}
	web, ok := spec.Services["web"]
	if !ok {
		t.Fatalf("web missing")
	}
	if web.Build != "." || !web.Expose {
		t.Errorf("web mis-parsed: %+v", web)
	}
}

func TestParseCompose_FullStack(t *testing.T) {
	t.Parallel()

	data := []byte(`
version: 1
services:
  web:
    build: .
    port: 3000
    expose: true
    env:
      DATABASE_URL: postgres://app:${DB_PASSWORD}@db:5432/app
      PORT: "3000"
    depends_on: [db]
  db:
    image: postgres:16-alpine
    env:
      POSTGRES_USER: app
      POSTGRES_PASSWORD: ${DB_PASSWORD}
      POSTGRES_DB: app
    healthcheck:
      cmd: pg_isready -U app
      interval_seconds: 3
      retries: 20
seed:
  after: db
  sql: ./seed/preview.sql
`)
	spec, err := ParseCompose(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(spec.Services) != 2 {
		t.Fatalf("want 2 services, got %d", len(spec.Services))
	}
	db := spec.Services["db"]
	if db.Healthcheck == nil || db.Healthcheck.Retries != 20 {
		t.Errorf("healthcheck mis-parsed: %+v", db.Healthcheck)
	}
	if spec.Seed == nil || spec.Seed.After != "db" {
		t.Errorf("seed mis-parsed: %+v", spec.Seed)
	}
}

func TestParseCompose_Invalid(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"wrong version": `version: 2
services:
  web:
    build: .`,
		"no services": `version: 1
services: {}`,
		"both build and image": `version: 1
services:
  web:
    build: .
    image: nginx`,
		"neither build nor image": `version: 1
services:
  web:
    port: 80`,
		"two exposed services": `version: 1
services:
  a:
    build: .
    expose: true
  b:
    image: nginx
    expose: true`,
		"unknown depends_on": `version: 1
services:
  web:
    build: .
    depends_on: [db]`,
		"seed points to unknown service": `version: 1
services:
  web:
    build: .
seed:
  after: db
  sql: ./x.sql`,
	}
	for name, raw := range cases {
		raw := raw
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseCompose([]byte(raw)); err == nil {
				t.Errorf("expected error for %q", name)
			}
		})
	}
}

func TestSubstitute(t *testing.T) {
	t.Parallel()

	spec := &ComposeSpec{
		Version: 1,
		Services: map[string]*ComposeService{
			"web": {
				Build: ".",
				Env: map[string]string{
					"DATABASE_URL": "postgres://app:${DB_PASSWORD}@db:5432/app",
					"PR_NUMBER":    "pr-${PR}",
					"SHA":          "${SHA}",
					"KEEP":         "${UNKNOWN_VAR}",
				},
			},
		},
	}
	sctx := SubstitutionContext{
		PR:         42,
		SHA:        "abcdef1",
		Repo:       "itsaam/hatch",
		Slug:       "itsaam-hatch",
		DBPassword: "secretpw",
	}
	Substitute(spec, sctx)

	env := spec.Services["web"].Env
	if got := env["DATABASE_URL"]; !strings.Contains(got, "secretpw") || strings.Contains(got, "${") {
		t.Errorf("DATABASE_URL not substituted: %q", got)
	}
	if env["PR_NUMBER"] != "pr-42" {
		t.Errorf("PR not substituted: %q", env["PR_NUMBER"])
	}
	if env["SHA"] != "abcdef1" {
		t.Errorf("SHA not substituted: %q", env["SHA"])
	}
	if env["KEEP"] != "${UNKNOWN_VAR}" {
		t.Errorf("unknown var should be preserved: %q", env["KEEP"])
	}
}

func TestDeriveDBPassword_Deterministic(t *testing.T) {
	t.Parallel()

	secret := []byte("sup3r-secret")
	a := DeriveDBPassword(secret, "itsaam/hatch", 3)
	b := DeriveDBPassword(secret, "itsaam/hatch", 3)
	if a != b {
		t.Fatalf("not deterministic: %s vs %s", a, b)
	}
	if len(a) != 24 {
		t.Errorf("want 24 chars, got %d", len(a))
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Errorf("password not hex: %v", err)
	}
	// Different PR → different password.
	if DeriveDBPassword(secret, "itsaam/hatch", 4) == a {
		t.Error("expected different password for different PR")
	}
	// Different repo → different password.
	if DeriveDBPassword(secret, "other/repo", 3) == a {
		t.Error("expected different password for different repo")
	}
}

func TestFallbackCompose(t *testing.T) {
	t.Parallel()
	spec := FallbackCompose()
	if err := validateCompose(spec); err != nil {
		t.Fatalf("fallback invalid: %v", err)
	}
	if ExposedService(spec) != "web" {
		t.Errorf("fallback should expose web, got %q", ExposedService(spec))
	}
}

func TestTopoSortServices(t *testing.T) {
	t.Parallel()

	spec := &ComposeSpec{
		Version: 1,
		Services: map[string]*ComposeService{
			"web":    {Build: ".", DependsOn: []string{"db", "cache"}},
			"db":     {Image: "postgres:16"},
			"cache":  {Image: "redis:7", DependsOn: []string{"db"}},
		},
	}
	order, err := TopoSortServices(spec)
	if err != nil {
		t.Fatalf("toposort: %v", err)
	}
	pos := map[string]int{}
	for i, n := range order {
		pos[n] = i
	}
	if pos["db"] >= pos["cache"] || pos["cache"] >= pos["web"] || pos["db"] >= pos["web"] {
		t.Errorf("bad order: %v", order)
	}
}

func TestTopoSortServices_Cycle(t *testing.T) {
	t.Parallel()

	spec := &ComposeSpec{
		Version: 1,
		Services: map[string]*ComposeService{
			"a": {Build: ".", DependsOn: []string{"b"}},
			"b": {Build: ".", DependsOn: []string{"a"}},
		},
	}
	if _, err := TopoSortServices(spec); err == nil {
		t.Error("expected cycle error")
	}
}
