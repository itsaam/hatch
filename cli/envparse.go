package main

import (
	"bufio"
	"regexp"
	"strings"
)

// EnvEntry is a parsed .env line with classification metadata.
type EnvEntry struct {
	Key     string
	Value   string // rewritten value for preview
	Comment string // optional comment line to emit after the entry (e.g. provide via hatch secrets)
}

// envURLKeys matches variable names that refer to the public URL of the app —
// these must resolve to ${PREVIEW_URL} so each PR preview sees its own URL.
var envURLKeys = regexp.MustCompile(`(?i)(NEXTAUTH_URL|NEXT_PUBLIC_APP_URL|SITE_URL|APP_URL|VERCEL_URL|PUBLIC_URL|CALLBACK_URL|PRODUCTION_URL)`)

// envSensitiveKey matches secret-looking variable names.
var envSensitiveKey = regexp.MustCompile(`(?i)(secret|token|api[_-]?key|password|passwd|private[_-]?key|client[_-]?secret|webhook[_-]?secret|client[_-]?id)`)

// envStableSecretKeys are auth secrets that should be deterministic per PR,
// reusing the already-generated ${DB_PASSWORD}.
var envStableSecretKeys = map[string]bool{
	"NEXTAUTH_SECRET": true,
	"AUTH_SECRET":     true,
	"JWT_SECRET":      true,
}

// envBoolPattern detects env values that are plain booleans.
var envBoolPattern = regexp.MustCompile(`^(?i)(true|false)$`)

// envPGURLPattern matches a postgres URL so we can rewrite host/password.
var envPGURL = regexp.MustCompile(`^postgres(?:ql)?://`)

// envRedisURL detects a redis URL.
var envRedisURL = regexp.MustCompile(`^redis://`)

// ParseEnvExample reads raw .env.example content and returns
// a deterministic list of preview-safe entries.
func ParseEnvExample(raw string) []EnvEntry {
	out := []EnvEntry{}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// strip optional `export ` prefix
		line = strings.TrimPrefix(line, "export ")
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		rawVal := strings.TrimSpace(line[eq+1:])
		// strip surrounding quotes
		rawVal = stripQuotes(rawVal)
		// strip inline comment
		if i := strings.Index(rawVal, " #"); i >= 0 {
			rawVal = strings.TrimSpace(rawVal[:i])
		}

		entry := classifyEnv(key, rawVal)
		out = append(out, entry)
	}
	return out
}

func stripQuotes(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// classifyEnv turns a raw (KEY, VAL) pair into a preview-ready entry.
func classifyEnv(key, val string) EnvEntry {
	upper := strings.ToUpper(key)

	// DATABASE_URL → normalize to internal postgres
	if upper == "DATABASE_URL" || envPGURL.MatchString(val) {
		return EnvEntry{Key: key, Value: "postgresql://app:${DB_PASSWORD}@db:5432/app?schema=public"}
	}

	// REDIS_URL → internal redis
	if upper == "REDIS_URL" || envRedisURL.MatchString(val) {
		return EnvEntry{Key: key, Value: "redis://redis:6379"}
	}

	// URLs dynamiques → preview URL
	if envURLKeys.MatchString(upper) {
		return EnvEntry{Key: key, Value: "${PREVIEW_URL}"}
	}

	// NODE_ENV / APP_ENV
	if upper == "NODE_ENV" || upper == "APP_ENV" || upper == "RAILS_ENV" {
		return EnvEntry{Key: key, Value: "production"}
	}

	// PORT
	if upper == "PORT" {
		return EnvEntry{Key: key, Value: "3000"}
	}

	// Auth stable secrets
	if envStableSecretKeys[upper] {
		return EnvEntry{Key: key, Value: "${DB_PASSWORD}"}
	}

	// Secrets sensibles → dummy + commentaire
	if envSensitiveKey.MatchString(key) {
		dummy := dummyForSecret(key, val)
		return EnvEntry{
			Key:     key,
			Value:   dummy,
			Comment: "# provide via ${SECRET_" + upper + "} for real testing",
		}
	}

	// Booleans on names qui sentent "emails/sms/notifications" → false
	if envBoolPattern.MatchString(val) && looksLikeNotification(upper) {
		return EnvEntry{Key: key, Value: "false"}
	}

	// Valeur d'exemple normale, on garde (mais on quote si bool-like pour yaml)
	if envBoolPattern.MatchString(val) {
		return EnvEntry{Key: key, Value: strings.ToLower(val)}
	}

	return EnvEntry{Key: key, Value: val}
}

func looksLikeNotification(upper string) bool {
	needles := []string{"EMAIL", "MAIL", "SMS", "NOTIF", "PUSH"}
	for _, n := range needles {
		if strings.Contains(upper, n) {
			return true
		}
	}
	return false
}

// dummyForSecret returns a realistic placeholder so services don't crash at boot
// for want of a parse-able shape.
func dummyForSecret(key, original string) string {
	upper := strings.ToUpper(key)
	switch {
	case strings.Contains(upper, "STRIPE") && strings.Contains(upper, "WEBHOOK"):
		return "whsec_preview_dummy"
	case strings.Contains(upper, "STRIPE") && strings.Contains(upper, "PUBLISHABLE"):
		return "pk_test_preview_dummy"
	case strings.Contains(upper, "STRIPE"):
		return "sk_test_preview_dummy"
	case strings.Contains(upper, "RESEND"):
		return "re_preview_dummy"
	case strings.Contains(upper, "SENDGRID"):
		return "SG.preview_dummy"
	case strings.Contains(upper, "OPENAI"):
		return "sk-preview-dummy"
	case strings.Contains(upper, "ANTHROPIC"):
		return "sk-ant-preview-dummy"
	case strings.Contains(upper, "CLIENT_ID"):
		return "preview-dummy-client-id"
	case strings.Contains(upper, "CLIENT_SECRET"):
		return "preview-dummy-client-secret"
	case strings.Contains(upper, "WEBHOOK"):
		return "preview-dummy-webhook"
	case strings.Contains(upper, "R2") || strings.Contains(upper, "S3") || strings.Contains(upper, "AWS"):
		return "preview-dummy"
	default:
		// Keep shape hints from original if non-empty & non-sensitive looking
		if original != "" && !strings.ContainsAny(original, " \t") && len(original) < 40 {
			return "preview-dummy"
		}
		return "preview-dummy"
	}
}
