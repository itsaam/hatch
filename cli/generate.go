package main

import (
	"fmt"
	"sort"
	"strings"
)

// Generate emits a .hatch.yml string from a DetectResult.
// We write YAML manually (not via yaml.Marshal) so the ordering
// is stable and human-friendly.
func Generate(r *DetectResult) string {
	var b strings.Builder
	b.WriteString("version: 1\n")
	b.WriteString("services:\n")
	for i, svc := range r.Services {
		writeService(&b, svc)
		if i < len(r.Services)-1 {
			b.WriteString("\n")
		}
	}
	if r.Seed != nil {
		b.WriteString("\nseed:\n")
		b.WriteString(fmt.Sprintf("  after: %s\n", r.Seed.After))
		b.WriteString(fmt.Sprintf("  sql: %s\n", r.Seed.SQL))
	}
	return b.String()
}

func writeService(b *strings.Builder, s Service) {
	fmt.Fprintf(b, "  %s:\n", s.Name)
	if s.Image != "" {
		fmt.Fprintf(b, "    image: %s\n", s.Image)
	} else if s.Build != "" {
		fmt.Fprintf(b, "    build: %s\n", s.Build)
	} else {
		fmt.Fprintf(b, "    build: .\n")
	}
	if s.Port > 0 {
		fmt.Fprintf(b, "    port: %d\n", s.Port)
	}
	if s.Expose {
		fmt.Fprintf(b, "    expose: true\n")
	}
	if len(s.Env) > 0 {
		fmt.Fprintf(b, "    env:\n")
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(b, "      %s: %s\n", k, quoteIfNeeded(s.Env[k]))
		}
	}
	if len(s.DependsOn) > 0 {
		fmt.Fprintf(b, "    depends_on: [%s]\n", strings.Join(s.DependsOn, ", "))
	}
	if s.Healthcheck != nil {
		fmt.Fprintf(b, "    healthcheck:\n")
		fmt.Fprintf(b, "      cmd: %s\n", quoteIfNeeded(s.Healthcheck.Cmd))
		if s.Healthcheck.IntervalSeconds > 0 {
			fmt.Fprintf(b, "      interval_seconds: %d\n", s.Healthcheck.IntervalSeconds)
		}
		if s.Healthcheck.Retries > 0 {
			fmt.Fprintf(b, "      retries: %d\n", s.Healthcheck.Retries)
		}
	}
}

// quoteIfNeeded quotes values that would otherwise confuse a YAML parser.
func quoteIfNeeded(v string) string {
	if v == "" {
		return `""`
	}
	// YAML-reserved unquoted values
	lower := strings.ToLower(v)
	switch lower {
	case "true", "false", "yes", "no", "null", "~", "on", "off":
		return fmt.Sprintf("%q", v)
	}
	// Contains characters that would need quoting
	if strings.ContainsAny(v, ":#\n\"'") || strings.HasPrefix(v, "@") || strings.HasPrefix(v, "*") || strings.HasPrefix(v, "&") {
		return fmt.Sprintf("%q", v)
	}
	return v
}
