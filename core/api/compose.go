package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ComposeSpec is the in-memory representation of a parsed `.hatch.yml`.
type ComposeSpec struct {
	Version  int                        `yaml:"version"`
	Services map[string]*ComposeService `yaml:"services"`
	Seed     *ComposeSeed               `yaml:"seed,omitempty"`
}

// ComposeService is one service of the stack. `build` and `image` are
// mutually exclusive.
type ComposeService struct {
	Build       string              `yaml:"build,omitempty"`
	Image       string              `yaml:"image,omitempty"`
	Port        int                 `yaml:"port,omitempty"`
	Expose      bool                `yaml:"expose,omitempty"`
	Env         map[string]string   `yaml:"env,omitempty"`
	DependsOn   []string            `yaml:"depends_on,omitempty"`
	Healthcheck *ComposeHealthcheck `yaml:"healthcheck,omitempty"`
}

// ComposeHealthcheck mirrors docker's HEALTHCHECK but with Hatch-friendly
// field names.
type ComposeHealthcheck struct {
	Cmd             string `yaml:"cmd"`
	IntervalSeconds int    `yaml:"interval_seconds,omitempty"`
	Retries         int    `yaml:"retries,omitempty"`
}

// ComposeSeed declares an optional SQL seed to run after a given service is
// healthy.
type ComposeSeed struct {
	After string `yaml:"after"`
	SQL   string `yaml:"sql"`
}

// ParseCompose parses `.hatch.yml` bytes and validates the result.
func ParseCompose(data []byte) (*ComposeSpec, error) {
	var spec ComposeSpec
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("parse hatch.yml: %w", err)
	}
	if err := validateCompose(&spec); err != nil {
		return nil, err
	}
	return &spec, nil
}

// serviceNameRE restricts service names to DNS-safe shortnames since they are
// used as network aliases and Docker container suffixes.
var serviceNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}[a-z0-9]$|^[a-z]$`)

func validateCompose(spec *ComposeSpec) error {
	if spec == nil {
		return errors.New("hatch.yml: empty spec")
	}
	if spec.Version != 1 {
		return fmt.Errorf("hatch.yml: unsupported version %d (want 1)", spec.Version)
	}
	if len(spec.Services) == 0 {
		return errors.New("hatch.yml: at least one service is required")
	}
	exposed := 0
	for name, svc := range spec.Services {
		if !serviceNameRE.MatchString(name) {
			return fmt.Errorf("hatch.yml: invalid service name %q", name)
		}
		if svc == nil {
			return fmt.Errorf("hatch.yml: service %q is empty", name)
		}
		hasBuild := strings.TrimSpace(svc.Build) != ""
		hasImage := strings.TrimSpace(svc.Image) != ""
		if hasBuild == hasImage {
			return fmt.Errorf("hatch.yml: service %q must set exactly one of build/image", name)
		}
		if svc.Expose {
			exposed++
		}
		for _, dep := range svc.DependsOn {
			if _, ok := spec.Services[dep]; !ok {
				return fmt.Errorf("hatch.yml: service %q depends on unknown service %q", name, dep)
			}
		}
	}
	if exposed > 1 {
		return errors.New("hatch.yml: only one service may set expose:true")
	}
	if spec.Seed != nil {
		if strings.TrimSpace(spec.Seed.After) == "" {
			return errors.New("hatch.yml: seed.after is required")
		}
		if _, ok := spec.Services[spec.Seed.After]; !ok {
			return fmt.Errorf("hatch.yml: seed.after references unknown service %q", spec.Seed.After)
		}
		if strings.TrimSpace(spec.Seed.SQL) == "" {
			return errors.New("hatch.yml: seed.sql is required")
		}
	}
	return nil
}

// FallbackCompose synthesises a single-service spec for repos with no
// `.hatch.yml` — preserves the original Dockerfile-at-root behaviour.
func FallbackCompose() *ComposeSpec {
	return &ComposeSpec{
		Version: 1,
		Services: map[string]*ComposeService{
			"web": {
				Build:  ".",
				Expose: true,
			},
		},
	}
}

// SubstitutionContext holds the values used for ${...} substitution.
type SubstitutionContext struct {
	PR         int
	SHA        string
	Repo       string // raw "owner/repo"
	Slug       string // sanitized slug
	DBPassword string
	Domain     string // e.g. "hatchpr.dev", used to derive PREVIEW_URL / PREVIEW_HOST
}

// DeriveDBPassword builds a deterministic per-PR DB password.
// It is HMAC_SHA256(webhookSecret, "db:{repo}:{pr}")[:24 hex chars].
func DeriveDBPassword(webhookSecret []byte, repo string, pr int) string {
	h := hmac.New(sha256.New, webhookSecret)
	_, _ = fmt.Fprintf(h, "db:%s:%d", repo, pr)
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:24]
}

var varRE = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// Substitute replaces supported ${...} tokens inside env values and build
// contexts. Unknown tokens are left untouched.
func Substitute(spec *ComposeSpec, sctx SubstitutionContext) {
	if spec == nil {
		return
	}
	for _, svc := range spec.Services {
		if svc == nil {
			continue
		}
		svc.Build = substituteString(svc.Build, sctx)
		for k, v := range svc.Env {
			svc.Env[k] = substituteString(v, sctx)
		}
	}
}

func substituteString(s string, sctx SubstitutionContext) string {
	if s == "" || !strings.Contains(s, "${") {
		return s
	}
	return varRE.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1]
		switch name {
		case "PR":
			return fmt.Sprintf("%d", sctx.PR)
		case "SHA":
			return sctx.SHA
		case "REPO":
			return sctx.Slug
		case "DB_PASSWORD":
			return sctx.DBPassword
		case "PREVIEW_HOST":
			if sctx.Domain == "" {
				return match
			}
			return fmt.Sprintf("pr-%d-%s.%s", sctx.PR, sctx.Slug, sctx.Domain)
		case "PREVIEW_URL":
			if sctx.Domain == "" {
				return match
			}
			return fmt.Sprintf("https://pr-%d-%s.%s", sctx.PR, sctx.Slug, sctx.Domain)
		default:
			return match
		}
	})
}

// TopoSortServices returns services ordered so that each service appears
// after all its dependencies. Services with no depends_on stay in
// alphabetical order for determinism.
func TopoSortServices(spec *ComposeSpec) ([]string, error) {
	names := make([]string, 0, len(spec.Services))
	for n := range spec.Services {
		names = append(names, n)
	}
	sort.Strings(names)

	visited := make(map[string]int) // 0=unseen,1=on-stack,2=done
	order := make([]string, 0, len(names))
	var visit func(n string, path []string) error
	visit = func(n string, path []string) error {
		switch visited[n] {
		case 1:
			return fmt.Errorf("hatch.yml: cycle in depends_on: %s", strings.Join(append(path, n), " -> "))
		case 2:
			return nil
		}
		visited[n] = 1
		svc := spec.Services[n]
		deps := append([]string(nil), svc.DependsOn...)
		sort.Strings(deps)
		for _, dep := range deps {
			if err := visit(dep, append(path, n)); err != nil {
				return err
			}
		}
		visited[n] = 2
		order = append(order, n)
		return nil
	}
	for _, n := range names {
		if err := visit(n, nil); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// ExposedService returns the single service with expose:true, or "" if none.
func ExposedService(spec *ComposeSpec) string {
	for n, svc := range spec.Services {
		if svc != nil && svc.Expose {
			return n
		}
	}
	return ""
}

// --- GitHub raw fetch -------------------------------------------------------

// githubRawURL builds the api.github.com contents URL for a given file.
// Using /repos/.../contents/<path> returns the decoded raw bytes when
// Accept: application/vnd.github.raw is set, and works equally with or
// without an installation token.
func githubContentsURL(repo, path, ref string) string {
	cleaned := strings.TrimPrefix(path, "./")
	cleaned = strings.TrimPrefix(cleaned, "/")
	return fmt.Sprintf("https://api.github.com/repos/%s/contents/%s?ref=%s", repo, cleaned, ref)
}

// fetchRepoFile downloads a file from a GitHub repo at a specific commit.
// If `app` and `installationID` are non-nil/non-zero, it authenticates with
// an installation token (required for private repos). Returns (nil, nil) for
// 404 so callers can distinguish "not present" from transport errors.
func fetchRepoFile(ctx context.Context, httpClient *http.Client, app *AppClient, installationID int64, repo, path, ref string) ([]byte, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	url := githubContentsURL(repo, path, ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", path, err)
	}
	req.Header.Set("Accept", "application/vnd.github.raw")
	req.Header.Set("User-Agent", githubUserAgent)
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if app != nil && installationID != 0 {
		tok, err := app.installationToken(ctx, installationID)
		if err != nil {
			return nil, fmt.Errorf("install token for %s: %w", path, err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxGitHubResponseSz))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch %s http %d: %s", path, resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

// loadComposeForRef fetches `.hatch.yml` at the PR's commit. If the file is
// absent, it returns FallbackCompose(). Parse errors are surfaced.
func loadComposeForRef(ctx context.Context, httpClient *http.Client, app *AppClient, installationID int64, repo, sha string) (*ComposeSpec, bool, error) {
	data, err := fetchRepoFile(ctx, httpClient, app, installationID, repo, ".hatch.yml", sha)
	if err != nil {
		return nil, false, err
	}
	if data == nil {
		return FallbackCompose(), false, nil
	}
	spec, err := ParseCompose(data)
	if err != nil {
		return nil, false, err
	}
	return spec, true, nil
}

// webhookSecretFromEnv reads GITHUB_WEBHOOK_SECRET for password derivation.
// Falls back to an empty byte slice if missing — callers should treat that
// as "no stable password possible" (the deploy will still work but be
// different between restarts, which is acceptable only in dev).
func webhookSecretFromEnv() []byte {
	return []byte(os.Getenv("GITHUB_WEBHOOK_SECRET"))
}
