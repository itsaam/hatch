package main

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Build       interface{} `yaml:"build"`
	Image       string      `yaml:"image"`
	Ports       []string    `yaml:"ports"`
	Environment interface{} `yaml:"environment"` // map or list
	DependsOn   interface{} `yaml:"depends_on"`  // list or map
	Healthcheck *composeHC  `yaml:"healthcheck"`
	Command     interface{} `yaml:"command"`
	Entrypoint  interface{} `yaml:"entrypoint"`
}

type composeHC struct {
	Test     interface{} `yaml:"test"` // string or list
	Interval string      `yaml:"interval"`
	Retries  int         `yaml:"retries"`
}

var previewServiceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$|^[a-z]$`)

func detectFromCompose(fs FS, path string) (*DetectResult, error) {
	raw, err := fs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cf composeFile
	if err := yaml.Unmarshal(raw, &cf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if len(cf.Services) == 0 {
		return nil, fmt.Errorf("%s has no services", path)
	}
	names := make([]string, 0, len(cf.Services))
	for n := range cf.Services {
		if !previewServiceName.MatchString(n) {
			return nil, fmt.Errorf("%s service %q is not a valid Hatch service name (use a lowercase DNS label of up to 32 characters starting with a letter)", path, n)
		}
		names = append(names, n)
	}
	sort.Strings(names)

	// Pick main exposed service: first with a build + port mapping.
	mainIdx := -1
	for i, n := range names {
		svc := cf.Services[n]
		if svc.Build != nil && len(svc.Ports) > 0 {
			mainIdx = i
			break
		}
	}
	if mainIdx == -1 {
		for i, n := range names {
			if len(cf.Services[n].Ports) > 0 {
				mainIdx = i
				break
			}
		}
	}

	services := make([]Service, 0, len(names))
	var warnings []string
	for i, n := range names {
		cs := cf.Services[n]
		svc := Service{Name: n, Env: map[string]string{}}

		switch b := cs.Build.(type) {
		case string:
			svc.Build = b
		case map[string]interface{}:
			svc.Dockerfile, _ = b["dockerfile"].(string)
			if ctx, ok := b["context"].(string); ok {
				svc.Build = ctx
			} else {
				svc.Build = "."
			}
		default:
			if cs.Image != "" {
				svc.Image = cs.Image
			}
		}
		if svc.Build == "" && svc.Image == "" {
			return nil, fmt.Errorf("%s service %q must declare a build or image", path, n)
		}
		if svc.Build != "" {
			context := filepath.Clean(svc.Build)
			if !filepath.IsLocal(context) || strings.Contains(svc.Build, "://") {
				return nil, fmt.Errorf("%s service %q must use a build context inside the repository", path, n)
			}
			if svc.Dockerfile != "" && (filepath.IsAbs(svc.Dockerfile) || strings.Contains(svc.Dockerfile, "..")) {
				return nil, fmt.Errorf("%s service %q must use a Dockerfile path relative to its build context, without '..'", path, n)
			}
			if context != "." {
				warnings = append(warnings, fmt.Sprintf("Service %s uses a nested build context. Kubernetes supports it. For Docker, use the repository root as the context and adjust the Dockerfile path and COPY instructions.", n))
			}
		}
		if cs.Command != nil || cs.Entrypoint != nil {
			warnings = append(warnings, fmt.Sprintf("Service %s overrides its startup command in Compose. Set that command in its Dockerfile or image for Hatch.", n))
		}

		if len(cs.Ports) > 0 {
			if p := parsePort(cs.Ports[0]); p > 0 {
				svc.Port = p
			}
		}
		if i == mainIdx && svc.Port > 0 {
			svc.Expose = true
		}

		svc.Env = normalizeEnv(cs.Environment)

		svc.DependsOn = normalizeDependsOn(cs.DependsOn)

		if cs.Healthcheck != nil {
			svc.Healthcheck = &Healthcheck{
				Cmd:             hcTestToCmd(cs.Healthcheck.Test),
				IntervalSeconds: parseDuration(cs.Healthcheck.Interval),
				Retries:         cs.Healthcheck.Retries,
			}
			if svc.Healthcheck.IntervalSeconds == 0 {
				svc.Healthcheck.IntervalSeconds = 3
			}
			if svc.Healthcheck.Retries == 0 {
				svc.Healthcheck.Retries = 20
			}
		}

		services = append(services, svc)
	}

	return &DetectResult{
		StackName: fmt.Sprintf("docker-compose (%d services)", len(services)),
		Manifest:  path,
		Services:  services,
		Warnings:  warnings,
	}, nil
}

var portRe = regexp.MustCompile(`^(?:[\d.:]+:)?(\d+)(?::\d+)?$`)

func parsePort(spec string) int {
	// Handles "3000", "3000:3000", "127.0.0.1:3000:3000".
	parts := strings.Split(spec, ":")
	last := parts[len(parts)-1]
	if n, err := strconv.Atoi(last); err == nil {
		return n
	}
	if m := portRe.FindStringSubmatch(spec); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return 0
}

var sensitiveKeyRe = regexp.MustCompile(`(?i)(secret|token|api[_-]?key|password|passwd|private[_-]?key)`)

func normalizeEnv(raw interface{}) map[string]string {
	out := map[string]string{}
	switch v := raw.(type) {
	case map[string]interface{}:
		for k, val := range v {
			s := fmt.Sprintf("%v", val)
			out[k] = scrubValue(k, s)
		}
	case []interface{}:
		for _, item := range v {
			s := fmt.Sprintf("%v", item)
			if i := strings.Index(s, "="); i >= 0 {
				k := s[:i]
				val := s[i+1:]
				out[k] = scrubValue(k, val)
			}
		}
	}
	return out
}

// scrubValue applies two rewrites:
// 1. secret-looking keys get value replaced by ${SECRET_<NAME>}
// 2. postgres URLs get their password replaced by ${DB_PASSWORD}
func scrubValue(key, value string) string {
	databaseURL := strings.TrimSpace(value)
	if strings.EqualFold(key, "DATABASE_URL") && isLocalDatabaseURL(databaseURL) {
		return value
	}
	if envOtherDBURL.MatchString(databaseURL) ||
		(strings.EqualFold(key, "DATABASE_URL") && !envPGURL.MatchString(databaseURL) && !previewVariable.MatchString(databaseURL)) {
		return databaseSecretEntry(key).Value
	}
	if sensitiveKeyRe.MatchString(key) {
		// Leave ${...} interpolations alone, otherwise replace.
		if !strings.HasPrefix(strings.TrimSpace(value), "${") {
			return "${SECRET_" + strings.ToUpper(key) + "}"
		}
	}
	if !envPGURL.MatchString(databaseURL) {
		return value
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return databaseSecretEntry(key).Value
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return databaseSecretEntry(key).Value
	}
	for parameter := range query {
		if strings.EqualFold(parameter, "password") || strings.EqualFold(parameter, "sslpassword") {
			return databaseSecretEntry(key).Value
		}
	}
	if _, hasPassword := parsed.User.Password(); !hasPassword {
		return value
	}
	if parsed.Host == "" {
		return databaseSecretEntry(key).Value
	}
	username := url.User(parsed.User.Username()).String()
	parsed.User = nil
	prefix := parsed.Scheme + "://"
	return prefix + username + ":${DB_PASSWORD}@" + strings.TrimPrefix(parsed.String(), prefix)
}

func normalizeDependsOn(raw interface{}) []string {
	switch v := raw.(type) {
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprintf("%v", item))
		}
		sort.Strings(out)
		return out
	case map[string]interface{}:
		out := make([]string, 0, len(v))
		for k := range v {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	return nil
}

func hcTestToCmd(raw interface{}) string {
	switch v := raw.(type) {
	case string:
		return v
	case []interface{}:
		parts := []string{}
		for _, p := range v {
			s := fmt.Sprintf("%v", p)
			if s == "CMD" || s == "CMD-SHELL" {
				continue
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, " ")
	}
	return ""
}

var durRe = regexp.MustCompile(`^(\d+)(s|m|h)?$`)

func parseDuration(s string) int {
	if s == "" {
		return 0
	}
	m := durRe.FindStringSubmatch(strings.TrimSpace(s))
	if len(m) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	switch m[2] {
	case "m":
		return n * 60
	case "h":
		return n * 3600
	default:
		return n
	}
}
