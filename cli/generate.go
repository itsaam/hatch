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
	// Determine which service owns the parsed env entries (first exposed).
	mainIdx := 0
	for i, s := range r.Services {
		if s.Expose {
			mainIdx = i
			break
		}
	}
	for i, svc := range r.Services {
		if i == mainIdx && len(r.EnvEntries) > 0 {
			writeServiceWithEntries(&b, svc, r.EnvEntries, r.EnvSource)
		} else {
			writeService(&b, svc)
		}
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

// writeServiceWithEntries emits a service block using ordered env entries
// from a parsed .env.example. Entries not already in svc.Env are skipped
// (shouldn't happen — applyEnvExample merges them first, but we are safe).
// Additional env keys present in svc.Env that are NOT in entries (e.g.
// stack-forced defaults like NODE_ENV, NEXTAUTH_URL) are emitted first so
// they're clearly visible at the top.
func writeServiceWithEntries(b *strings.Builder, s Service, entries []EnvEntry, source string) {
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
		if source != "" {
			fmt.Fprintf(b, "      # généré depuis %s — valeurs dummy pour preview, ajuste avec hatch secrets pour les vraies clés de test\n", source)
		}
		// Which keys are in entries?
		inEntries := map[string]bool{}
		for _, e := range entries {
			inEntries[e.Key] = true
		}
		// Extra keys added by the stack (not from .env.example).
		extras := []string{}
		for k := range s.Env {
			if !inEntries[k] {
				extras = append(extras, k)
			}
		}
		sort.Strings(extras)
		for _, k := range extras {
			fmt.Fprintf(b, "      %s: %s\n", k, quoteIfNeeded(s.Env[k]))
		}
		// Then entries in their original order.
		for _, e := range entries {
			v, ok := s.Env[e.Key]
			if !ok {
				v = e.Value
			}
			fmt.Fprintf(b, "      %s: %s\n", e.Key, quoteIfNeeded(v))
			if e.Comment != "" {
				fmt.Fprintf(b, "      %s\n", e.Comment)
			}
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
	// Pure digits — would parse as int; quote to keep it a string (e.g. PORT).
	if isAllDigits(v) {
		return fmt.Sprintf("%q", v)
	}
	// Contains characters that would need quoting
	if strings.ContainsAny(v, ":#\n\"'") || strings.HasPrefix(v, "@") || strings.HasPrefix(v, "*") || strings.HasPrefix(v, "&") {
		return fmt.Sprintf("%q", v)
	}
	return v
}

func isAllDigits(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
