package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const dockerAPIVersion = "v1.43"

// PreviewRef carries enough context for status callbacks (PR comment updates).
type PreviewRef struct {
	Repo           string
	PR             int
	Branch         string
	SHA            string
	InstallationID int64
	CommentID      int64
}

// StatusNotifier is called by the deployer when a preview status transitions.
// Implementations MUST NOT block the deploy loop for long; they may run in a
// goroutine if they do remote I/O.
type StatusNotifier interface {
	OnStatusChange(ctx context.Context, ref PreviewRef, status, publicURL string)
}

type noopNotifier struct{}

func (noopNotifier) OnStatusChange(context.Context, PreviewRef, string, string) {}

type Deployer struct {
	http       *http.Client
	pool       *pgxpool.Pool
	network    string
	domain     string
	notifier   StatusNotifier
	dockerBase string // e.g. "http://docker"; tests override this.
}

func NewDeployer(pool *pgxpool.Pool, netName, domain string) (*Deployer, error) {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", "/var/run/docker.sock")
		},
	}
	return &Deployer{
		http:       &http.Client{Transport: tr, Timeout: 15 * time.Minute},
		pool:       pool,
		network:    netName,
		domain:     domain,
		notifier:   noopNotifier{},
		dockerBase: "http://docker",
	}, nil
}

// dockerURL builds a Docker Engine API URL anchored at the configured base.
func (d *Deployer) dockerURL(path string) string {
	return d.dockerBase + "/" + dockerAPIVersion + path
}

// SetNotifier wires a status notifier. Nil is coerced to a no-op.
func (d *Deployer) SetNotifier(n StatusNotifier) {
	if n == nil {
		d.notifier = noopNotifier{}
		return
	}
	d.notifier = n
}

func (d *Deployer) Deploy(ref PreviewRef) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	slug := slugify(ref.Repo)
	name := fmt.Sprintf("hatch-preview-%s-%d", slug, ref.PR)
	tag := fmt.Sprintf("%s:%s", name, shortSHA(ref.SHA))
	host := fmt.Sprintf("pr-%d-%s.%s", ref.PR, slug, d.domain)
	publicURL := "https://" + host

	log.Printf("deploy start: %s → %s", name, publicURL)
	d.setStatus(ctx, ref, "building", "")

	if err := d.build(ctx, ref.Repo, ref.SHA, tag); err != nil {
		log.Printf("build failed %s: %v", name, err)
		d.setStatus(ctx, ref, "failed", "")
		return
	}

	_ = d.remove(ctx, name)

	if err := d.run(ctx, name, tag, host); err != nil {
		log.Printf("run failed %s: %v", name, err)
		d.setStatus(ctx, ref, "failed", "")
		return
	}

	log.Printf("deploy ok: %s → %s", name, publicURL)
	d.setStatus(ctx, ref, "running", publicURL)

	if err := d.pruneOldImages(ctx, slug, ref.PR, tag); err != nil {
		log.Printf("prune images %s: %v", name, err)
	}
}

func (d *Deployer) Destroy(ref PreviewRef) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()
	name := fmt.Sprintf("hatch-preview-%s-%d", slugify(ref.Repo), ref.PR)
	if err := d.remove(ctx, name); err != nil {
		log.Printf("destroy %s: %v", name, err)
		return
	}
	log.Printf("preview destroyed %s", name)
	d.setStatus(ctx, ref, "closed", "")
}

func (d *Deployer) build(ctx context.Context, repo, sha, tag string) error {
	remote := fmt.Sprintf("https://github.com/%s.git#%s", repo, sha)
	q := url.Values{}
	q.Set("remote", remote)
	q.Set("t", tag)
	q.Set("q", "1")
	q.Set("forcerm", "1")

	req, err := http.NewRequestWithContext(ctx, "POST",
		d.dockerURL("/build?"+q.Encode()),
		bytes.NewReader(nil))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/tar")

	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("build http %d: %s", resp.StatusCode, truncate(string(body), 500))
	}
	if bytes.Contains(body, []byte(`"error"`)) {
		return fmt.Errorf("build stream error: %s", truncate(string(body), 500))
	}
	return nil
}

type createBody struct {
	Image            string              `json:"Image"`
	Labels           map[string]string   `json:"Labels"`
	HostConfig       hostConfig          `json:"HostConfig"`
	NetworkingConfig networkingConfig    `json:"NetworkingConfig"`
	Env              []string            `json:"Env,omitempty"`
	ExposedPorts     map[string]struct{} `json:"ExposedPorts,omitempty"`
}

type hostConfig struct {
	RestartPolicy restartPolicy `json:"RestartPolicy"`
}

type restartPolicy struct {
	Name string `json:"Name"`
}

type networkingConfig struct {
	EndpointsConfig map[string]struct{} `json:"EndpointsConfig"`
}

func (d *Deployer) run(ctx context.Context, name, tag, host string) error {
	port, err := d.detectPort(ctx, tag)
	if err != nil {
		port = "80"
	}

	r := name
	body := createBody{
		Image: tag,
		Labels: map[string]string{
			"traefik.enable":         "true",
			"traefik.docker.network": d.network,
			fmt.Sprintf("traefik.http.routers.%s.rule", r):                      fmt.Sprintf("Host(`%s`)", host),
			fmt.Sprintf("traefik.http.routers.%s.entrypoints", r):               "websecure",
			fmt.Sprintf("traefik.http.routers.%s.tls", r):                       "true",
			fmt.Sprintf("traefik.http.routers.%s.tls.certresolver", r):          "letsencrypt",
			fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", r): port,
			"hatch.managed": "true",
		},
		HostConfig: hostConfig{
			RestartPolicy: restartPolicy{Name: "unless-stopped"},
		},
		NetworkingConfig: networkingConfig{
			EndpointsConfig: map[string]struct{}{d.network: {}},
		},
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		d.dockerURL("/containers/create?name="+url.QueryEscape(name)),
		bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("create http %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}

	var created struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil {
		return err
	}
	if created.ID == "" {
		return errors.New("empty container id")
	}

	startReq, err := http.NewRequestWithContext(ctx, "POST",
		d.dockerURL("/containers/"+created.ID+"/start"), nil)
	if err != nil {
		return err
	}
	startResp, err := d.http.Do(startReq)
	if err != nil {
		return err
	}
	defer startResp.Body.Close()
	if startResp.StatusCode >= 400 {
		b, _ := io.ReadAll(startResp.Body)
		return fmt.Errorf("start http %d: %s", startResp.StatusCode, truncate(string(b), 300))
	}
	return nil
}

func (d *Deployer) detectPort(ctx context.Context, tag string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		d.dockerURL("/images/"+url.PathEscape(tag)+"/json"), nil)
	if err != nil {
		return "", err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("inspect %d", resp.StatusCode)
	}
	var info struct {
		Config struct {
			ExposedPorts map[string]struct{} `json:"ExposedPorts"`
		} `json:"Config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	for p := range info.Config.ExposedPorts {
		if i := strings.IndexByte(p, '/'); i > 0 {
			return p[:i], nil
		}
		return p, nil
	}
	return "", errors.New("no exposed port")
}

func (d *Deployer) remove(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE",
		d.dockerURL("/containers/"+url.PathEscape(name)+"?force=1&v=1"), nil)
	if err != nil {
		return err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 || resp.StatusCode < 400 {
		return nil
	}
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("remove http %d: %s", resp.StatusCode, truncate(string(b), 200))
}

func (d *Deployer) setStatus(ctx context.Context, ref PreviewRef, status, publicURL string) {
	var err error
	if publicURL == "" {
		_, err = d.pool.Exec(ctx,
			`UPDATE previews SET status=$1, updated_at=NOW() WHERE repo_full_name=$2 AND pr_number=$3`,
			status, ref.Repo, ref.PR)
	} else {
		_, err = d.pool.Exec(ctx,
			`UPDATE previews SET status=$1, url=$2, updated_at=NOW() WHERE repo_full_name=$3 AND pr_number=$4`,
			status, publicURL, ref.Repo, ref.PR)
	}
	if err != nil {
		log.Printf("setStatus: %v", err)
	}
	if d.notifier != nil {
		d.notifier.OnStatusChange(ctx, ref, status, publicURL)
	}
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = slugRE.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// pruneOldImages removes images tagged `hatch-preview-<slug>-<pr>:*` that are
// not the currently deployed tag. Errors from "image in use" (409) are
// swallowed — the running container legitimately holds a reference.
func (d *Deployer) pruneOldImages(ctx context.Context, slug string, pr int, currentTag string) error {
	req, err := http.NewRequestWithContext(ctx, "GET",
		d.dockerURL("/images/json?all=0"), nil)
	if err != nil {
		return fmt.Errorf("prune list req: %w", err)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return fmt.Errorf("prune list do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("prune list http %d: %s", resp.StatusCode, truncate(string(b), 200))
	}

	var images []struct {
		ID       string   `json:"Id"`
		RepoTags []string `json:"RepoTags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&images); err != nil {
		return fmt.Errorf("prune list decode: %w", err)
	}

	prefix := fmt.Sprintf("hatch-preview-%s-%d:", slug, pr)
	for _, img := range images {
		for _, tag := range img.RepoTags {
			if !strings.HasPrefix(tag, prefix) || tag == currentTag {
				continue
			}
			if err := d.removeImage(ctx, tag); err != nil {
				log.Printf("prune image %s: %v", tag, err)
			} else {
				log.Printf("pruned image %s", tag)
			}
		}
	}
	return nil
}

func (d *Deployer) removeImage(ctx context.Context, ref string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE",
		d.dockerURL("/images/"+url.PathEscape(ref)+"?force=0&noprune=0"), nil)
	if err != nil {
		return fmt.Errorf("remove image req: %w", err)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return fmt.Errorf("remove image do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		// Image in use by a running container — expected for the current tag.
		return nil
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode < 400 {
		return nil
	}
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("remove image http %d: %s", resp.StatusCode, truncate(string(b), 200))
}

// previewStore is the narrow DB contract used by reconciliation and the TTL
// reaper. It lets tests swap in an in-memory fake without spinning up
// Postgres or mocking pgx internals.
type previewStore interface {
	activePreviewKeys(ctx context.Context) (map[previewKey]bool, error)
	zombieCandidates(ctx context.Context) ([]previewLocator, error)
	markFailed(ctx context.Context, repo string, pr int) error
	expiredCandidates(ctx context.Context, cutoff time.Time) ([]PreviewRef, error)
	markExpired(ctx context.Context, repo string, pr int) error
}

type previewLocator struct {
	repo string
	pr   int
}

// pgxStore adapts a *pgxpool.Pool to previewStore.
type pgxStore struct{ pool *pgxpool.Pool }

func (s *pgxStore) activePreviewKeys(ctx context.Context) (map[previewKey]bool, error) {
	return loadActivePreviewKeys(ctx, s.pool)
}

func (s *pgxStore) zombieCandidates(ctx context.Context) ([]previewLocator, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT repo_full_name, pr_number
		FROM previews
		WHERE status IN ('running','building','pending')
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []previewLocator
	for rows.Next() {
		var l previewLocator
		if err := rows.Scan(&l.repo, &l.pr); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *pgxStore) markFailed(ctx context.Context, repo string, pr int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE previews SET status='failed', updated_at=NOW() WHERE repo_full_name=$1 AND pr_number=$2`,
		repo, pr)
	return err
}

func (s *pgxStore) expiredCandidates(ctx context.Context, cutoff time.Time) ([]PreviewRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT repo_full_name, pr_number, branch, commit_sha,
		       COALESCE(installation_id, 0), COALESCE(comment_id, 0)
		FROM previews
		WHERE status='running' AND updated_at < $1
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PreviewRef
	for rows.Next() {
		var ref PreviewRef
		if err := rows.Scan(&ref.Repo, &ref.PR, &ref.Branch, &ref.SHA, &ref.InstallationID, &ref.CommentID); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

func (s *pgxStore) markExpired(ctx context.Context, repo string, pr int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE previews SET status='expired', updated_at=NOW() WHERE repo_full_name=$1 AND pr_number=$2`,
		repo, pr)
	return err
}

// Reconcile aligns Docker containers labelled hatch.managed=true with the
// previews table. Orphan containers (no DB row with an active status) are
// destroyed. Zombie DB rows (status=running/building/pending but the
// container vanished) are flipped to failed. Best-effort: each error is
// logged and does not abort the reconciliation.
func (d *Deployer) Reconcile(ctx context.Context, pool *pgxpool.Pool) error {
	return d.reconcileWithStore(ctx, &pgxStore{pool: pool})
}

func (d *Deployer) reconcileWithStore(ctx context.Context, store previewStore) error {
	removedContainers, err := d.reconcileOrphanContainers(ctx, store)
	if err != nil {
		log.Printf("reconcile orphan containers: %v", err)
	}
	markedZombies, err := d.reconcileZombiePreviews(ctx, store)
	if err != nil {
		log.Printf("reconcile zombie previews: %v", err)
	}
	log.Printf("reconciled: removed %d orphan containers, marked %d zombie previews as failed",
		removedContainers, markedZombies)
	return nil
}

func (d *Deployer) reconcileOrphanContainers(ctx context.Context, store previewStore) (int, error) {
	filters := `{"label":["hatch.managed=true"]}`
	q := url.Values{}
	q.Set("all", "true")
	q.Set("filters", filters)

	req, err := http.NewRequestWithContext(ctx, "GET",
		d.dockerURL("/containers/json?"+q.Encode()), nil)
	if err != nil {
		return 0, fmt.Errorf("list containers req: %w", err)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("list containers do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("list containers http %d: %s", resp.StatusCode, truncate(string(b), 200))
	}

	var containers []struct {
		ID     string            `json:"Id"`
		Names  []string          `json:"Names"`
		Labels map[string]string `json:"Labels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return 0, fmt.Errorf("list containers decode: %w", err)
	}

	// Build the set of (slug, pr) keys that are currently considered active
	// in the DB. Slug is derived from repo_full_name via slugify.
	activeKeys, err := store.activePreviewKeys(ctx)
	if err != nil {
		return 0, fmt.Errorf("load active previews: %w", err)
	}

	removed := 0
	for _, c := range containers {
		name := primaryContainerName(c.Names)
		if name == "" {
			continue
		}
		slug, pr, ok := parsePreviewName(name)
		if !ok {
			continue
		}
		if activeKeys[previewKey{slug: slug, pr: pr}] {
			continue
		}
		if err := d.remove(ctx, name); err != nil {
			log.Printf("reconcile remove %s: %v", name, err)
			continue
		}
		removed++
		log.Printf("reconciled: removed orphan container %s", name)
	}
	return removed, nil
}

type previewKey struct {
	slug string
	pr   int
}

func loadActivePreviewKeys(ctx context.Context, pool *pgxpool.Pool) (map[previewKey]bool, error) {
	rows, err := pool.Query(ctx,
		`SELECT repo_full_name, pr_number FROM previews WHERE status IN ('running','building','pending')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[previewKey]bool)
	for rows.Next() {
		var repo string
		var pr int
		if err := rows.Scan(&repo, &pr); err != nil {
			return nil, err
		}
		out[previewKey{slug: slugify(repo), pr: pr}] = true
	}
	return out, rows.Err()
}

func (d *Deployer) reconcileZombiePreviews(ctx context.Context, store previewStore) (int, error) {
	list, err := store.zombieCandidates(ctx)
	if err != nil {
		return 0, fmt.Errorf("query zombies: %w", err)
	}

	marked := 0
	for _, p := range list {
		name := fmt.Sprintf("hatch-preview-%s-%d", slugify(p.repo), p.pr)
		exists, err := d.containerExists(ctx, name)
		if err != nil {
			log.Printf("reconcile inspect %s: %v", name, err)
			continue
		}
		if exists {
			continue
		}
		if err := store.markFailed(ctx, p.repo, p.pr); err != nil {
			log.Printf("reconcile mark failed %s#%d: %v", p.repo, p.pr, err)
			continue
		}
		marked++
		log.Printf("reconciled: marked zombie preview %s#%d as failed", p.repo, p.pr)
	}
	return marked, nil
}

func (d *Deployer) containerExists(ctx context.Context, name string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		d.dockerURL("/containers/"+url.PathEscape(name)+"/json"), nil)
	if err != nil {
		return false, fmt.Errorf("inspect req: %w", err)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("inspect do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return false, fmt.Errorf("inspect http %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	return true, nil
}

// StartTTLReaper launches a goroutine that periodically expires inactive
// running previews. It returns immediately; the goroutine shuts down when
// ctx is cancelled.
func (d *Deployer) StartTTLReaper(ctx context.Context, pool *pgxpool.Pool, ttl, interval time.Duration) {
	d.startTTLReaperWithStore(ctx, &pgxStore{pool: pool}, ttl, interval)
}

func (d *Deployer) startTTLReaperWithStore(ctx context.Context, store previewStore, ttl, interval time.Duration) {
	if ttl <= 0 || interval <= 0 {
		log.Printf("ttl reaper disabled (ttl=%s interval=%s)", ttl, interval)
		return
	}
	go func() {
		log.Printf("ttl reaper started (ttl=%s interval=%s)", ttl, interval)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		// Run one pass immediately so a freshly-booted server honors TTL
		// without waiting a full interval.
		d.reapOnce(ctx, store, ttl)
		for {
			select {
			case <-ctx.Done():
				log.Printf("ttl reaper stopped")
				return
			case <-ticker.C:
				d.reapOnce(ctx, store, ttl)
			}
		}
	}()
}

func (d *Deployer) reapOnce(ctx context.Context, store previewStore, ttl time.Duration) {
	passCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cutoff := time.Now().Add(-ttl)
	batch, err := store.expiredCandidates(passCtx, cutoff)
	if err != nil {
		log.Printf("reaper query: %v", err)
		return
	}

	days := int(ttl.Hours() / 24)
	for _, ref := range batch {
		d.expirePreview(passCtx, store, ref, days)
	}
}

func (d *Deployer) expirePreview(ctx context.Context, store previewStore, ref PreviewRef, days int) {
	name := fmt.Sprintf("hatch-preview-%s-%d", slugify(ref.Repo), ref.PR)
	if err := d.remove(ctx, name); err != nil {
		log.Printf("reaper remove %s: %v", name, err)
	}
	if err := store.markExpired(ctx, ref.Repo, ref.PR); err != nil {
		log.Printf("reaper mark expired %s#%d: %v", ref.Repo, ref.PR, err)
		return
	}
	log.Printf("reaper: expired preview %s#%d after %dd inactivity", ref.Repo, ref.PR, days)

	if d.notifier == nil {
		return
	}
	if _, _, ok := splitRepo(ref.Repo); !ok {
		return
	}
	if ref.InstallationID == 0 || ref.CommentID == 0 {
		return
	}
	if setter, ok := d.notifier.(hibernateNotifier); ok {
		setter.OnHibernated(ctx, ref, days)
	}
}

// hibernateNotifier is an optional extension to StatusNotifier so the reaper
// can post a dedicated "hibernated" comment without polluting the main
// status state machine.
type hibernateNotifier interface {
	OnHibernated(ctx context.Context, ref PreviewRef, days int)
}

func primaryContainerName(names []string) string {
	for _, n := range names {
		n = strings.TrimPrefix(n, "/")
		if n != "" {
			return n
		}
	}
	return ""
}

// parsePreviewName reverses the hatch-preview-<slug>-<pr> naming.
// Since the slug itself may contain dashes, we take the *last* dash as the
// pr boundary. Returns the slug and PR number, but since slug→repo isn't
// injective we return the slug as "repo" alias — callers query by it
// carefully. In practice we match DB rows by iterating all previews in the
// caller; here we return empty repo and rely on the caller using slug match.
func parsePreviewName(name string) (slug string, pr int, ok bool) {
	const prefix = "hatch-preview-"
	if !strings.HasPrefix(name, prefix) {
		return "", 0, false
	}
	rest := name[len(prefix):]
	idx := strings.LastIndexByte(rest, '-')
	if idx <= 0 || idx == len(rest)-1 {
		return "", 0, false
	}
	var n int
	for _, c := range rest[idx+1:] {
		if c < '0' || c > '9' {
			return "", 0, false
		}
		n = n*10 + int(c-'0')
	}
	return rest[:idx], n, true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
