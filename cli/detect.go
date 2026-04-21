package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// FS is a minimal filesystem abstraction. Real runs use osFS,
// tests use memFS.
type FS interface {
	ReadFile(path string) ([]byte, error)
	Exists(path string) bool
	// ListDir returns names (not paths) of direct entries.
	ListDir(path string) ([]string, error)
}

// Service is the internal representation before YAML emission.
type Service struct {
	Name        string
	Build       string            // "." or subfolder
	Image       string            // OR image
	Port        int               // 0 = none
	Expose      bool
	Env         map[string]string // values as-is (may contain ${SECRET_X})
	DependsOn   []string
	Healthcheck *Healthcheck
}

type Healthcheck struct {
	Cmd             string
	IntervalSeconds int
	Retries         int
}

type DetectResult struct {
	StackName string // human-readable, e.g. "Next.js + Prisma + Postgres"
	Services  []Service
	Seed      *Seed
	// Warnings surfaced to the user (e.g. "no Dockerfile, one should be added").
	Warnings []string
}

type Seed struct {
	After string
	SQL   string
}

// Detect inspects fs and returns the best-effort stack description.
func Detect(fs FS) (*DetectResult, error) {
	// Priority 0: repo is hatch itself (edge case).
	if fs.Exists("landing/package.json") && fs.Exists("core/api/main.go") {
		return detectHatchRepo(fs)
	}

	// Priority 1: existing docker-compose.
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if fs.Exists(name) {
			return detectFromCompose(fs, name)
		}
	}

	// Priority 2: Node / package.json.
	if fs.Exists("package.json") {
		return detectFromPackageJSON(fs)
	}

	// Priority 3: Gemfile.
	if fs.Exists("Gemfile") {
		return detectFromGemfile(fs)
	}

	// Priority 4: Python.
	if fs.Exists("pyproject.toml") || fs.Exists("requirements.txt") {
		return detectFromPython(fs)
	}

	// Priority 5: Dockerfile only.
	if fs.Exists("Dockerfile") {
		return &DetectResult{
			StackName: "Dockerfile (stateless)",
			Services: []Service{
				{
					Name:   "web",
					Build:  ".",
					Port:   3000,
					Expose: true,
					Env:    map[string]string{},
				},
			},
		}, nil
	}

	return nil, fmt.Errorf("no recognizable stack found (no package.json, Gemfile, pyproject.toml, requirements.txt, Dockerfile, or docker-compose.yml)")
}

// ---------- Hatch self-hosting edge case ----------

func detectHatchRepo(fs FS) (*DetectResult, error) {
	return &DetectResult{
		StackName: "Hatch (landing-only preview)",
		Services: []Service{
			{
				Name:   "landing",
				Build:  "./landing",
				Port:   80,
				Expose: true,
				Env: map[string]string{
					"VITE_API_URL": "https://api.hatchpr.dev",
				},
			},
		},
	}, nil
}

// ---------- docker-compose parser ----------

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Build       interface{}   `yaml:"build"`
	Image       string        `yaml:"image"`
	Ports       []string      `yaml:"ports"`
	Environment interface{}   `yaml:"environment"` // map or list
	DependsOn   interface{}   `yaml:"depends_on"`  // list or map
	Healthcheck *composeHC    `yaml:"healthcheck"`
	Expose      []interface{} `yaml:"expose"`
}

type composeHC struct {
	Test     interface{} `yaml:"test"` // string or list
	Interval string      `yaml:"interval"`
	Retries  int         `yaml:"retries"`
}

func detectFromCompose(fs FS, path string) (*DetectResult, error) {
	raw, err := fs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cf composeFile
	if err := yaml.Unmarshal(raw, &cf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	names := make([]string, 0, len(cf.Services))
	for n := range cf.Services {
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
	for i, n := range names {
		cs := cf.Services[n]
		svc := Service{Name: n, Env: map[string]string{}}

		switch b := cs.Build.(type) {
		case string:
			svc.Build = b
		case map[string]interface{}:
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
		if svc.Build == "" && svc.Image == "" && cs.Build != nil {
			svc.Build = "."
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
		Services:  services,
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
	if sensitiveKeyRe.MatchString(key) {
		// Leave ${...} interpolations alone, otherwise replace.
		if !strings.HasPrefix(strings.TrimSpace(value), "${") {
			return "${SECRET_" + strings.ToUpper(key) + "}"
		}
	}
	return rewritePostgresURL(value)
}

var pgURLRe = regexp.MustCompile(`^(postgres(?:ql)?://[^:]+:)([^@]+)(@.+)$`)

func rewritePostgresURL(v string) string {
	if m := pgURLRe.FindStringSubmatch(v); len(m) == 4 {
		return m[1] + "${DB_PASSWORD}" + m[3]
	}
	return v
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

// ---------- package.json / Node ----------

type packageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Scripts         map[string]string `json:"scripts"`
}

func detectFromPackageJSON(fs FS) (*DetectResult, error) {
	raw, err := fs.ReadFile("package.json")
	if err != nil {
		return nil, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return nil, fmt.Errorf("parse package.json: %w", err)
	}

	hasDep := func(name string) bool {
		if _, ok := pkg.Dependencies[name]; ok {
			return true
		}
		_, ok := pkg.DevDependencies[name]
		return ok
	}

	warnings := []string{}
	if !fs.Exists("Dockerfile") {
		warnings = append(warnings, "No Dockerfile found. Hatch will need one to build the `web` service — add a minimal Dockerfile at the repo root.")
	}

	switch {
	case hasDep("next"):
		return nextStack(hasDep, warnings), nil
	case hasDep("express") || hasDep("fastify"):
		return nodeAPIStack(hasDep, warnings), nil
	default:
		return staticStack(warnings), nil
	}
}

func nextStack(has func(string) bool, warnings []string) *DetectResult {
	withDB := has("prisma") || has("@prisma/client") || has("pg") || has("drizzle-orm")
	web := Service{
		Name:   "web",
		Build:  ".",
		Port:   3000,
		Expose: true,
		Env: map[string]string{
			"NODE_ENV": "production",
		},
	}
	name := "Next.js"
	services := []Service{web}

	if withDB {
		name = "Next.js + Prisma + Postgres"
		web.Env["DATABASE_URL"] = "postgres://app:${DB_PASSWORD}@db:5432/app"
		web.DependsOn = []string{"db"}
		db := postgresService()
		services = []Service{web, db}
	}

	return &DetectResult{StackName: name, Services: services, Warnings: warnings}
}

func nodeAPIStack(has func(string) bool, warnings []string) *DetectResult {
	withDB := has("pg") || has("mongoose") || has("prisma") || has("@prisma/client")
	api := Service{
		Name:   "api",
		Build:  ".",
		Port:   3000,
		Expose: true,
		Env:    map[string]string{"NODE_ENV": "production"},
	}
	services := []Service{api}
	name := "Node API"
	if withDB {
		name = "Node API + Postgres"
		api.Env["DATABASE_URL"] = "postgres://app:${DB_PASSWORD}@db:5432/app"
		api.DependsOn = []string{"db"}
		services = []Service{api, postgresService()}
	}
	return &DetectResult{StackName: name, Services: services, Warnings: warnings}
}

func staticStack(warnings []string) *DetectResult {
	return &DetectResult{
		StackName: "Static site",
		Services: []Service{
			{
				Name:   "web",
				Build:  ".",
				Port:   80,
				Expose: true,
				Env:    map[string]string{},
			},
		},
		Warnings: warnings,
	}
}

func postgresService() Service {
	return Service{
		Name:  "db",
		Image: "postgres:16-alpine",
		Port:  5432,
		Env: map[string]string{
			"POSTGRES_USER":     "app",
			"POSTGRES_PASSWORD": "${DB_PASSWORD}",
			"POSTGRES_DB":       "app",
		},
		Healthcheck: &Healthcheck{
			Cmd:             "pg_isready -U app",
			IntervalSeconds: 3,
			Retries:         20,
		},
	}
}

func redisService() Service {
	return Service{
		Name:  "redis",
		Image: "redis:7-alpine",
		Port:  6379,
		Healthcheck: &Healthcheck{
			Cmd:             "redis-cli ping",
			IntervalSeconds: 3,
			Retries:         20,
		},
	}
}

// ---------- Gemfile / Rails ----------

func detectFromGemfile(fs FS) (*DetectResult, error) {
	raw, err := fs.ReadFile("Gemfile")
	if err != nil {
		return nil, err
	}
	body := string(raw)
	has := func(gem string) bool {
		// naive but good enough: match `gem "X"` or `gem 'X'`.
		re := regexp.MustCompile(`(?m)^\s*gem\s+['"]` + regexp.QuoteMeta(gem) + `['"]`)
		return re.MatchString(body)
	}

	warnings := []string{}
	if !fs.Exists("Dockerfile") {
		warnings = append(warnings, "No Dockerfile found. Rails apps need one for Hatch to build the `web` service.")
	}

	services := []Service{}
	name := "Rails"

	web := Service{
		Name:   "web",
		Build:  ".",
		Port:   3000,
		Expose: true,
		Env: map[string]string{
			"RAILS_ENV":      "production",
			"RAILS_LOG_TO_STDOUT": "1",
		},
	}
	deps := []string{}

	if has("pg") {
		deps = append(deps, "db")
		web.Env["DATABASE_URL"] = "postgres://app:${DB_PASSWORD}@db:5432/app"
		services = append(services, postgresService())
		name += " + Postgres"
	}
	if has("redis") || has("sidekiq") {
		deps = append(deps, "redis")
		web.Env["REDIS_URL"] = "redis://redis:6379/0"
		services = append(services, redisService())
		name += " + Redis"
	}

	sort.Strings(deps)
	web.DependsOn = deps
	services = append([]Service{web}, services...)

	if has("sidekiq") {
		worker := Service{
			Name:      "worker",
			Build:     ".",
			Env:       copyEnv(web.Env),
			DependsOn: deps,
			// no port/expose for workers
		}
		worker.Env["PROCESS_TYPE"] = "worker"
		services = append(services, worker)
		name += " + Sidekiq"
	}

	return &DetectResult{StackName: name, Services: services, Warnings: warnings}, nil
}

// ---------- Python ----------

func detectFromPython(fs FS) (*DetectResult, error) {
	body := ""
	for _, p := range []string{"pyproject.toml", "requirements.txt"} {
		if fs.Exists(p) {
			if raw, err := fs.ReadFile(p); err == nil {
				body += "\n" + strings.ToLower(string(raw))
			}
		}
	}
	has := func(lib string) bool {
		return strings.Contains(body, lib)
	}

	warnings := []string{}
	if !fs.Exists("Dockerfile") {
		warnings = append(warnings, "No Dockerfile found. Add one to build the `api` service.")
	}

	api := Service{
		Name:   "api",
		Build:  ".",
		Port:   8000,
		Expose: true,
		Env:    map[string]string{},
	}

	name := "Python"
	switch {
	case has("django"):
		name = "Django"
	case has("fastapi"):
		name = "FastAPI"
	case has("flask"):
		name = "Flask"
	}

	services := []Service{api}
	if has("psycopg") || has("asyncpg") {
		api.Env["DATABASE_URL"] = "postgres://app:${DB_PASSWORD}@db:5432/app"
		api.DependsOn = []string{"db"}
		services = []Service{api, postgresService()}
		name += " + Postgres"
	}
	if has("redis") {
		api.Env["REDIS_URL"] = "redis://redis:6379/0"
		api.DependsOn = appendUnique(api.DependsOn, "redis")
		services = append(services, redisService())
		name += " + Redis"
	}

	return &DetectResult{StackName: name, Services: services, Warnings: warnings}, nil
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// ---------- helpers ----------

func copyEnv(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// WalkJoin is a small helper for tests to build absolute-ish paths.
func WalkJoin(parts ...string) string { return filepath.Join(parts...) }
