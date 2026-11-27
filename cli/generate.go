package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Generate emits a .hatch.yml string with stable service and environment ordering.
func Generate(r *DetectResult) string {
	var b strings.Builder
	b.WriteString("version: 1\nservices:\n")

	mainIdx := 0
	for i, s := range r.Services {
		if s.Expose {
			mainIdx = i
			break
		}
	}
	for i, svc := range r.Services {
		if i == mainIdx {
			writeService(&b, svc, r.EnvEntries, r.EnvSource)
		} else {
			writeService(&b, svc, nil, "")
		}
		if i < len(r.Services)-1 {
			b.WriteByte('\n')
		}
	}
	if r.Seed != nil {
		fmt.Fprintf(&b, "\nseed:\n  after: %s\n  sql: %s\n", r.Seed.After, quoteIfNeeded(r.Seed.SQL))
	}
	return b.String()
}

func writeService(b *strings.Builder, s Service, entries []EnvEntry, source string) {
	fmt.Fprintf(b, "  %s:\n", s.Name)
	if s.Image != "" {
		fmt.Fprintf(b, "    image: %s\n", quoteIfNeeded(s.Image))
	} else {
		build := s.Build
		if build == "" {
			build = "."
		}
		fmt.Fprintf(b, "    build: %s\n", quoteIfNeeded(build))
		if s.Dockerfile != "" {
			fmt.Fprintf(b, "    dockerfile: %s\n", quoteIfNeeded(s.Dockerfile))
		}
	}
	if s.Port > 0 {
		fmt.Fprintf(b, "    port: %d\n", s.Port)
	}
	if s.Expose {
		b.WriteString("    expose: true\n")
	}
	writeEnv(b, s.Env, entries, source)
	if len(s.DependsOn) > 0 {
		fmt.Fprintf(b, "    depends_on: [%s]\n", strings.Join(s.DependsOn, ", "))
	}
	if s.Healthcheck != nil {
		fmt.Fprintf(b, "    healthcheck:\n      cmd: %s\n", quoteIfNeeded(s.Healthcheck.Cmd))
		if s.Healthcheck.IntervalSeconds > 0 {
			fmt.Fprintf(b, "      interval_seconds: %d\n", s.Healthcheck.IntervalSeconds)
		}
		if s.Healthcheck.Retries > 0 {
			fmt.Fprintf(b, "      retries: %d\n", s.Healthcheck.Retries)
		}
	}
}

func writeEnv(b *strings.Builder, env map[string]string, entries []EnvEntry, source string) {
	if len(env) == 0 {
		return
	}
	b.WriteString("    env:\n")
	if source != "" {
		fmt.Fprintf(b, "      # Generated from %s. Review values and store any test credentials in Hatch.\n", source)
	}
	fromExample := make(map[string]bool, len(entries))
	for _, entry := range entries {
		fromExample[entry.Key] = true
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		if !fromExample[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(b, "      %s: %s\n", quoteIfNeeded(key), quoteIfNeeded(env[key]))
	}
	for _, entry := range entries {
		if value, ok := env[entry.Key]; ok && fromExample[entry.Key] {
			fmt.Fprintf(b, "      %s: %s\n", quoteIfNeeded(entry.Key), quoteIfNeeded(value))
			if entry.Comment != "" {
				fmt.Fprintf(b, "      %s\n", entry.Comment)
			}
			delete(fromExample, entry.Key)
		}
	}
}

// Keep plain scalars readable when they round-trip as the same string.
func quoteIfNeeded(value string) string {
	var doc yaml.Node
	if !strings.ContainsAny(value, "\r\n\t") && yaml.Unmarshal([]byte(value), &doc) == nil && len(doc.Content) == 1 {
		node := doc.Content[0]
		if node.Kind == yaml.ScalarNode && node.Tag == "!!str" && node.Style == 0 && node.Value == value {
			return value
		}
	}
	return strconv.Quote(value)
}
