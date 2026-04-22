package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"
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
