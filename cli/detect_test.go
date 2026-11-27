package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// memFS is an in-memory FS implementation for tests.
type memFS struct {
	files map[string]string
}

func newMemFS(files map[string]string) memFS {
	return memFS{files: files}
}

func (m memFS) ReadFile(path string) ([]byte, error) {
	v, ok := m.files[path]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	return []byte(v), nil
}

func (m memFS) Exists(path string) bool {
	_, ok := m.files[path]
	return ok
}

func (m memFS) ListDir(path string) ([]string, error) {
	out := map[string]struct{}{}
	prefix := path
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	for p := range m.files {
		if strings.HasPrefix(p, prefix) {
			rest := strings.TrimPrefix(p, prefix)
			if i := strings.Index(rest, "/"); i >= 0 {
				rest = rest[:i]
			}
			out[rest] = struct{}{}
		}
	}
	names := make([]string, 0, len(out))
	for n := range out {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name              string
		files             map[string]string
		wantStackContains string
		wantServices      []string // names, sorted
		wantErr           bool
	}{
		{
			name: "docker-compose with 2 services",
			files: map[string]string{
				"docker-compose.yml": `
services:
  web:
    build: .
    ports: ["3000:3000"]
    environment:
      NODE_ENV: production
      API_SECRET_KEY: supersecret
      DATABASE_URL: postgres://app:hunter2@db:5432/app
    depends_on: [db]
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_PASSWORD: hunter2
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "app"]
      interval: 3s
      retries: 20
`,
			},
			wantStackContains: "docker-compose",
			wantServices:      []string{"db", "web"},
		},
		{
			name: "package.json Next.js + Prisma",
			files: map[string]string{
				"package.json": `{
          "dependencies": {"next": "14.0.0", "@prisma/client": "5.0.0"}
        }`,
				"Dockerfile": "FROM node:20\n",
			},
			wantStackContains: "Next.js",
			wantServices:      []string{"db", "web"},
		},
		{
			name: "package.json Next.js only",
			files: map[string]string{
				"package.json": `{"dependencies": {"next": "14.0.0"}}`,
				"Dockerfile":   "FROM node:20\n",
			},
			wantStackContains: "Next.js",
			wantServices:      []string{"web"},
		},
		{
			name: "Gemfile Rails + Sidekiq + pg + redis",
			files: map[string]string{
				"Gemfile": `
source "https://rubygems.org"
gem "rails"
gem "pg"
gem "redis"
gem "sidekiq"
`,
				"Dockerfile": "FROM ruby:3.3\n",
			},
			wantStackContains: "Rails",
			wantServices:      []string{"db", "redis", "web", "worker"},
		},
		{
			name: "Dockerfile only",
			files: map[string]string{
				"Dockerfile": "FROM alpine\n",
			},
			wantStackContains: "Dockerfile",
			wantServices:      []string{"web"},
		},
		{
			name: "Hatch repo edge case",
			files: map[string]string{
				"landing/package.json": `{"dependencies":{"vite":"5"}}`,
				"core/api/main.go":     `package main`,
			},
			wantStackContains: "Hatch",
			wantServices:      []string{"landing"},
		},
		{
			name: "Python FastAPI + psycopg + redis",
			files: map[string]string{
				"requirements.txt": "fastapi==0.100\npsycopg[binary]==3.1\nredis==5.0\n",
				"Dockerfile":       "FROM python:3.12\n",
			},
			wantStackContains: "FastAPI",
			wantServices:      []string{"api", "db", "redis"},
		},
		{
			name:    "empty project",
			files:   map[string]string{},
			wantErr: true,
		},
		{
			name: "Node server with pg only (no framework)",
			files: map[string]string{
				"package.json": `{
          "dependencies": {"pg": "^8.11.0"},
          "scripts": {"start": "node server.js"}
        }`,
				"Dockerfile": "FROM node:20\n",
			},
			wantStackContains: "Node",
			wantServices:      []string{"api", "db"},
		},
		{
			name: "Express + redis",
			files: map[string]string{
				"package.json": `{
          "dependencies": {"express": "^4", "ioredis": "^5"}
        }`,
				"Dockerfile": "FROM node:20\n",
			},
			wantStackContains: "Express",
			wantServices:      []string{"api", "redis"},
		},
		{
			name: "NestJS + prisma",
			files: map[string]string{
				"package.json": `{
          "dependencies": {"@nestjs/core": "^10", "@prisma/client": "^5"}
        }`,
				"Dockerfile": "FROM node:20\n",
			},
			wantStackContains: "NestJS",
			wantServices:      []string{"api", "db"},
		},
		{
			name: "package.json without server indicators → static",
			files: map[string]string{
				"package.json": `{"dependencies": {"lodash": "^4"}}`,
			},
			wantStackContains: "Static",
			wantServices:      []string{"web"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := Detect(newMemFS(tc.files))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(r.StackName, tc.wantStackContains) {
				t.Errorf("stack = %q, want to contain %q", r.StackName, tc.wantStackContains)
			}
			got := make([]string, 0, len(r.Services))
			for _, s := range r.Services {
				got = append(got, s.Name)
			}
			sort.Strings(got)
			if !equalStrings(got, tc.wantServices) {
				t.Errorf("services = %v, want %v", got, tc.wantServices)
			}
		})
	}
}

func TestScrubSensitiveEnv(t *testing.T) {
	files := map[string]string{
		"docker-compose.yml": `
services:
  web:
    build: .
    ports: ["3000:3000"]
    environment:
      NODE_ENV: production
      API_SECRET_KEY: shouldNotLeak
      STRIPE_TOKEN: sk_live_xxx
      DATABASE_URL: postgres://user:realpwd@db/app
`,
	}
	r, err := Detect(newMemFS(files))
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	var web Service
	for _, s := range r.Services {
		if s.Name == "web" {
			web = s
		}
	}
	if v := web.Env["API_SECRET_KEY"]; v != "${SECRET_API_SECRET_KEY}" {
		t.Errorf("API_SECRET_KEY = %q, want placeholder", v)
	}
	if v := web.Env["STRIPE_TOKEN"]; v != "${SECRET_STRIPE_TOKEN}" {
		t.Errorf("STRIPE_TOKEN = %q, want placeholder", v)
	}
	if v := web.Env["DATABASE_URL"]; !strings.Contains(v, "${DB_PASSWORD}") {
		t.Errorf("DATABASE_URL = %q, want ${DB_PASSWORD}", v)
	}
	if v := web.Env["NODE_ENV"]; v != "production" {
		t.Errorf("NODE_ENV = %q, want production", v)
	}
}

func TestGenerateRoundTrip(t *testing.T) {
	r, err := Detect(newMemFS(map[string]string{
		"package.json": `{"dependencies":{"next":"14","@prisma/client":"5"}}`,
		"Dockerfile":   "FROM node:20\n",
	}))
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	out := Generate(r)
	for _, needle := range []string{
		"version: 1",
		"services:",
		"  web:",
		"    build: .",
		"    port: 3000",
		"    expose: true",
		"  db:",
		"    image: postgres:16-alpine",
		"${DB_PASSWORD}",
	} {
		if !strings.Contains(out, needle) {
			t.Errorf("output missing %q\n---\n%s", needle, out)
		}
	}
}

func TestGeneratePreservesComposeDockerfileAndEnv(t *testing.T) {
	r, err := Detect(newMemFS(map[string]string{
		"package.json": `{"dependencies":{"express":"4"}}`,
		"compose.yaml": `services:
  web:
    build:
      context: .
      dockerfile: Dockerfile.preview
    ports: ["8080:80"]
    command: ["serve"]
    environment:
      PATTERN: "[one,two]"
      FRACTION: "0.125"
      LEADING_SPACE: " value"
`,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if r.Manifest != "compose.yaml" {
		t.Fatalf("manifest = %q, want compose.yaml", r.Manifest)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "startup command") {
		t.Fatalf("warnings = %v, want Compose startup command warning", r.Warnings)
	}
	var config struct {
		Services map[string]struct {
			Dockerfile string
			Port       int
			Env        map[string]any
		}
	}
	if err := yaml.Unmarshal([]byte(Generate(r)), &config); err != nil {
		t.Fatal(err)
	}
	web := config.Services["web"]
	if web.Dockerfile != "Dockerfile.preview" || web.Port != 80 {
		t.Fatalf("generated service = %+v", web)
	}
	for key, want := range map[string]string{"PATTERN": "[one,two]", "FRACTION": "0.125", "LEADING_SPACE": " value"} {
		if got, ok := web.Env[key].(string); !ok || got != want {
			t.Errorf("env[%q] = %#v, want string %q", key, web.Env[key], want)
		}
	}
}

func TestDetectDockerfileOnlyLetsRuntimeInferPort(t *testing.T) {
	r, err := Detect(newMemFS(map[string]string{"Dockerfile": "FROM nginx:alpine\nEXPOSE 80\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if r.Services[0].Port != 0 || !r.Services[0].Expose {
		t.Fatalf("Dockerfile-only service = %+v", r.Services[0])
	}
}

func TestGenerateQuotesAmbiguousServiceNamesAndValues(t *testing.T) {
	r := &DetectResult{
		Services: []Service{
			{Name: "web", Build: ".", DependsOn: []string{"null"}, Env: map[string]string{"MODE": "off", "DURATION": "1:12"}},
			{Name: "null", Image: "postgres:16-alpine"},
		},
		Seed: &Seed{After: "null", SQL: "seed.sql"},
	}
	y := Generate(r)
	for _, expected := range []string{
		`  "null":`, `depends_on: ["null"]`, `after: "null"`, `MODE: "off"`, `DURATION: "1:12"`,
	} {
		if !strings.Contains(y, expected) {
			t.Errorf("generated config missing %q:\n%s", expected, y)
		}
	}
}

func TestComposeWarnsAboutNestedContextAndRejectsInvalidDockerfile(t *testing.T) {
	const compose = "services:\n  web:\n    build:\n      context: ./app\n      dockerfile: %s\n    ports: [\"8080:80\"]\n"
	r, err := Detect(newMemFS(map[string]string{"compose.yaml": fmt.Sprintf(compose, "Dockerfile.preview")}))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "repository root") {
		t.Fatalf("warnings = %v, want Docker build context warning", r.Warnings)
	}
	if _, err := Detect(newMemFS(map[string]string{"compose.yaml": fmt.Sprintf(compose, "../Dockerfile")})); err == nil {
		t.Fatal("expected the unsupported Dockerfile path to be rejected")
	}
}

func TestDetectNodeDoesNotSubstitutePostgresForMongo(t *testing.T) {
	r, err := Detect(newMemFS(map[string]string{
		"package.json": `{"dependencies":{"express":"4","mongoose":"8"}}`,
		"Dockerfile":   "FROM node:20\n",
		".env.example": "DATABASE_URL=mongodb://user:password@localhost:27017/app\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Services) != 1 || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "MongoDB") {
		t.Fatalf("unexpected detected stack: %+v", r)
	}
	output := Generate(r)
	if !strings.Contains(output, "DATABASE_URL: ${SECRET_DATABASE_URL}") || strings.Contains(output, "password") {
		t.Fatalf("unexpected generated database URL:\n%s", output)
	}
}

func TestLocalDatabaseURLRemainsLiteral(t *testing.T) {
	const localURL = "file:./preview.db"
	if got := scrubValue("DATABASE_URL", localURL); got != localURL {
		t.Errorf("Compose DATABASE_URL = %q, want %q", got, localURL)
	}
	if got := classifyEnv("DATABASE_URL", localURL).Value; got != localURL {
		t.Errorf("example DATABASE_URL = %q, want %q", got, localURL)
	}
}

func TestRemoteDatabaseCredentialsAreReplaced(t *testing.T) {
	for key, source := range map[string]string{
		"DATABASE_URL": " MYSQL://user:demo-password@db/app",
		"MONGO_URL":    " MONGODB+SRV://user:demo-password@cluster/app",
	} {
		want := "${SECRET_" + key + "}"
		if got := scrubValue(key, source); got != want {
			t.Errorf("Compose %s = %q, want %q", key, got, want)
		}
		if got := classifyEnv(key, source).Value; got != want {
			t.Errorf("example %s = %q, want %q", key, got, want)
		}
	}
}

func equalStrings(a, b []string) bool {
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
