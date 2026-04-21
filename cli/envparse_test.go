package main

import (
	"strings"
	"testing"
)

func findEntry(entries []EnvEntry, key string) (EnvEntry, bool) {
	for _, e := range entries {
		if e.Key == key {
			return e, true
		}
	}
	return EnvEntry{}, false
}

// TestParseEnvExample_MixedSensitiveUrlsAndNormal covers the three categories
// simultaneously: URL dynamiques, secrets, bool/email, value normale.
func TestParseEnvExample_MixedSensitiveUrlsAndNormal(t *testing.T) {
	raw := `
# comment line
NODE_ENV=development
NEXT_PUBLIC_APP_URL=http://localhost:3000
NEXTAUTH_URL=http://localhost:3000
NEXTAUTH_SECRET=your-secret-key
DATABASE_URL=postgresql://user:password@localhost:5432/mydb?schema=public
REDIS_URL=redis://localhost:6379
STRIPE_SECRET_KEY=sk_test_xxx
GOOGLE_CLIENT_ID=dummyid
PORT=3000
EMAILS_ENABLED=true
COMPANY_NAME=Ma Societe SAS
`
	entries := ParseEnvExample(raw)

	checks := map[string]string{
		"NODE_ENV":            "production",
		"NEXT_PUBLIC_APP_URL": "${PREVIEW_URL}",
		"NEXTAUTH_URL":        "${PREVIEW_URL}",
		"NEXTAUTH_SECRET":     "${DB_PASSWORD}",
		"DATABASE_URL":        "postgresql://app:${DB_PASSWORD}@db:5432/app?schema=public",
		"REDIS_URL":           "redis://redis:6379",
		"STRIPE_SECRET_KEY":   "sk_test_preview_dummy",
		"GOOGLE_CLIENT_ID":    "preview-dummy-client-id",
		"PORT":                "3000",
		"EMAILS_ENABLED":      "false",
		"COMPANY_NAME":        "Ma Societe SAS",
	}
	for k, want := range checks {
		e, ok := findEntry(entries, k)
		if !ok {
			t.Fatalf("missing entry %s", k)
		}
		if e.Value != want {
			t.Errorf("%s = %q, want %q", k, e.Value, want)
		}
	}

	// Stripe secret must carry a comment.
	e, _ := findEntry(entries, "STRIPE_SECRET_KEY")
	if !strings.Contains(e.Comment, "SECRET_STRIPE_SECRET_KEY") {
		t.Errorf("STRIPE_SECRET_KEY comment = %q, want mention ${SECRET_STRIPE_SECRET_KEY}", e.Comment)
	}

	// NextAuth secret has no comment (stable secret path).
	e, _ = findEntry(entries, "NEXTAUTH_SECRET")
	if e.Comment != "" {
		t.Errorf("NEXTAUTH_SECRET should have no comment, got %q", e.Comment)
	}
}

// TestDetectRedisAndNextAuthTogether ensures the new package.json detection
// picks up ioredis + next-auth and wires the right env + services.
func TestDetectRedisAndNextAuthTogether(t *testing.T) {
	files := map[string]string{
		"package.json": `{
		  "dependencies": {
		    "next": "16.0.0",
		    "@prisma/client": "6.0.0",
		    "next-auth": "4.24.0",
		    "ioredis": "5.8.0"
		  }
		}`,
		"Dockerfile": "FROM node:20\n",
	}
	r, err := Detect(newMemFS(files))
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	names := []string{}
	for _, s := range r.Services {
		names = append(names, s.Name)
	}
	hasAll := func(want ...string) bool {
		for _, w := range want {
			found := false
			for _, n := range names {
				if n == w {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	if !hasAll("web", "db", "redis") {
		t.Fatalf("expected services web/db/redis, got %v", names)
	}

	var web Service
	for _, s := range r.Services {
		if s.Name == "web" {
			web = s
		}
	}
	if web.Env["NEXTAUTH_URL"] != "${PREVIEW_URL}" {
		t.Errorf("NEXTAUTH_URL = %q, want ${PREVIEW_URL}", web.Env["NEXTAUTH_URL"])
	}
	if web.Env["NEXTAUTH_SECRET"] != "${DB_PASSWORD}" {
		t.Errorf("NEXTAUTH_SECRET = %q, want ${DB_PASSWORD}", web.Env["NEXTAUTH_SECRET"])
	}
	if web.Env["REDIS_URL"] != "redis://redis:6379" {
		t.Errorf("REDIS_URL = %q, want redis://redis:6379", web.Env["REDIS_URL"])
	}

	// DetectedDeps contient les bonnes labels
	got := strings.Join(r.DetectedDeps, ",")
	for _, needle := range []string{"prisma", "redis", "next-auth"} {
		if !strings.Contains(got, needle) {
			t.Errorf("DetectedDeps missing %q, got %q", needle, got)
		}
	}
}

// TestDetectStripeOnlyAddsDummyEnv ensures stripe-only detection doesn't
// create redis / auth machinery but does flag dummy env via the entries path.
func TestDetectStripeOnlyAddsDummyEnv(t *testing.T) {
	files := map[string]string{
		"package.json": `{"dependencies":{"next":"16","stripe":"20.0.0"}}`,
		"Dockerfile":   "FROM node:20\n",
		".env.example": "STRIPE_SECRET_KEY=sk_test_real\nSTRIPE_WEBHOOK_SECRET=whsec_x\nNODE_ENV=development\n",
	}
	r, err := Detect(newMemFS(files))
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	names := []string{}
	for _, s := range r.Services {
		names = append(names, s.Name)
	}
	for _, n := range names {
		if n == "redis" {
			t.Fatalf("unexpected redis service in stripe-only detection: %v", names)
		}
	}

	// DetectedDeps should mention stripe.
	if !strings.Contains(strings.Join(r.DetectedDeps, ","), "stripe") {
		t.Errorf("DetectedDeps missing stripe: %v", r.DetectedDeps)
	}

	// EnvSource should be .env.example and entries should be present and dummied.
	if r.EnvSource != ".env.example" {
		t.Errorf("EnvSource = %q, want .env.example", r.EnvSource)
	}
	e, ok := findEntry(r.EnvEntries, "STRIPE_SECRET_KEY")
	if !ok {
		t.Fatalf("missing STRIPE_SECRET_KEY entry")
	}
	if e.Value != "sk_test_preview_dummy" {
		t.Errorf("STRIPE_SECRET_KEY = %q, want sk_test_preview_dummy", e.Value)
	}
	e, _ = findEntry(r.EnvEntries, "STRIPE_WEBHOOK_SECRET")
	if e.Value != "whsec_preview_dummy" {
		t.Errorf("STRIPE_WEBHOOK_SECRET = %q, want whsec_preview_dummy", e.Value)
	}

	// Generated YAML must contain the provenance comment + SECRET_ hint.
	yaml := Generate(r)
	if !strings.Contains(yaml, "généré depuis .env.example") {
		t.Errorf("yaml missing provenance comment\n%s", yaml)
	}
	if !strings.Contains(yaml, "${SECRET_STRIPE_SECRET_KEY}") {
		t.Errorf("yaml missing ${SECRET_STRIPE_SECRET_KEY} comment\n%s", yaml)
	}
}
