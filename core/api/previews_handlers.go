package main

// Preview admin endpoints consumed by the dashboard.
//
// Auth: every handler in this file is wired under r.Route("/api/previews")
// with the requireAdminToken middleware (see main.go). Bearer token against
// HATCH_ADMIN_TOKEN, constant-time compare — fail closed if the env var is
// empty.
//
// Routes:
//   GET    /api/previews                                  — list all previews
//   GET    /api/previews/{owner}/{repo}/{pr}/logs         — docker logs (last 200 lines)
//   POST   /api/previews/{owner}/{repo}/{pr}/redeploy     — queue a redeploy
//   DELETE /api/previews/{owner}/{repo}/{pr}              — force destroy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ownerRepoSegmentRE validates a single path segment — prevents path
// traversal or weird injections when we recompose owner/repo or feed it
// into Docker filters.
var ownerRepoSegmentRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type previewListItem struct {
	RepoFullName   string `json:"repo_full_name"`
	PRNumber       int    `json:"pr_number"`
	Branch         string `json:"branch"`
	CommitSHA      string `json:"commit_sha"`
	Status         string `json:"status"`
	URL            string `json:"url"`
	UpdatedAt      string `json:"updated_at"`
	CreatedAt      string `json:"created_at"`
	InstallationID int64  `json:"installation_id"`
	CommentID      int64  `json:"comment_id"`
}

// listPreviewsHandler returns the 500 most recently updated previews.
func listPreviewsHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		rows, err := pool.Query(ctx, `
			SELECT repo_full_name, pr_number, branch, commit_sha, status, COALESCE(url, ''),
			       updated_at, created_at,
			       COALESCE(installation_id, 0), COALESCE(comment_id, 0)
			FROM previews
			ORDER BY updated_at DESC
			LIMIT 500
		`)
		if err != nil {
			log.Printf("list previews: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		defer rows.Close()

		out := make([]previewListItem, 0, 64)
		for rows.Next() {
			var item previewListItem
			var updated, created time.Time
			if err := rows.Scan(
				&item.RepoFullName, &item.PRNumber, &item.Branch, &item.CommitSHA,
				&item.Status, &item.URL, &updated, &created,
				&item.InstallationID, &item.CommentID,
			); err != nil {
				log.Printf("scan previews: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
				return
			}
			item.UpdatedAt = updated.UTC().Format(time.RFC3339)
			item.CreatedAt = created.UTC().Format(time.RFC3339)
			out = append(out, item)
		}
		if err := rows.Err(); err != nil {
			log.Printf("rows err previews: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// parsePreviewPath extracts {owner}/{repo}/{pr} from chi URL params and
// validates each segment. Returns repoFullName, pr, and a boolean ok.
// On !ok, it has already written a 400 response.
func parsePreviewPath(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	owner := chi.URLParam(r, "owner")
	repo := chi.URLParam(r, "repo")
	prStr := chi.URLParam(r, "pr")

	if !ownerRepoSegmentRE.MatchString(owner) || !ownerRepoSegmentRE.MatchString(repo) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid owner/repo"})
		return "", 0, false
	}
	pr, err := strconv.Atoi(prStr)
	if err != nil || pr <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid pr"})
		return "", 0, false
	}
	return owner + "/" + repo, pr, true
}

// loadPreviewRef reads the minimum fields needed to rebuild a PreviewRef
// from the previews table. Returns (ref, found, err).
func loadPreviewRef(ctx context.Context, pool *pgxpool.Pool, repo string, pr int) (PreviewRef, bool, error) {
	var ref PreviewRef
	ref.Repo = repo
	ref.PR = pr
	err := pool.QueryRow(ctx, `
		SELECT branch, commit_sha, COALESCE(installation_id, 0), COALESCE(comment_id, 0)
		FROM previews
		WHERE repo_full_name = $1 AND pr_number = $2
	`, repo, pr).Scan(&ref.Branch, &ref.SHA, &ref.InstallationID, &ref.CommentID)
	if err != nil {
		// pgx returns ErrNoRows — but we keep the check cheap: distinguish
		// by error string is brittle, so we re-query with COUNT instead.
		var n int
		if qerr := pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM previews WHERE repo_full_name=$1 AND pr_number=$2`,
			repo, pr).Scan(&n); qerr == nil && n == 0 {
			return ref, false, nil
		}
		return ref, false, err
	}
	return ref, true, nil
}

// previewLogsHandler returns logs for a preview. When ?kind=build it pulls
// the most recent build_logs rows (one per service). Otherwise (kind=runtime
// or absent) it streams the exposed container's stdout+stderr via Docker.
// If the preview never produced a running container, it falls back to the
// latest build logs automatically so a failed build can still be inspected.
func previewLogsHandler(pool *pgxpool.Pool, deployer *Deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, pr, ok := parsePreviewPath(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		_, found, err := loadPreviewRef(ctx, pool, repo, pr)
		if err != nil {
			log.Printf("logs load %s#%d: %v", repo, pr, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "preview not found"})
			return
		}

		kind := r.URL.Query().Get("kind")

		if kind != "build" {
			container, err := deployer.exposedContainerName(ctx, repo, pr)
			if err != nil {
				log.Printf("locate exposed container %s#%d: %v", repo, pr, err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "docker error"})
				return
			}
			if container != "" {
				logs, err := deployer.containerLogs(ctx, container, 500)
				if err == nil {
					writeJSON(w, http.StatusOK, map[string]any{"kind": "runtime", "logs": logs})
					return
				}
				if err != errContainerNotFound {
					log.Printf("logs %s: %v", container, err)
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "docker error"})
					return
				}
				// container vanished — fall through to build logs
			}
			// No runtime container — caller implicitly wanted runtime but we
			// only have build output. Return build logs (still useful) with
			// kind set so the UI knows to label them as such.
		}

		// Build logs path: latest row per service for this PR.
		rows, err := pool.Query(ctx, `
			SELECT DISTINCT ON (service) service, status, COALESCE(raw_output,''), COALESCE(error,''), started_at, completed_at
			FROM build_logs
			WHERE repo_full_name=$1 AND pr_number=$2
			ORDER BY service, started_at DESC
		`, repo, pr)
		if err != nil {
			log.Printf("build logs query %s#%d: %v", repo, pr, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		defer rows.Close()

		type buildRow struct {
			Service     string  `json:"service"`
			Status      string  `json:"status"`
			Output      string  `json:"output"`
			Error       string  `json:"error,omitempty"`
			StartedAt   string  `json:"started_at"`
			CompletedAt *string `json:"completed_at,omitempty"`
		}
		var out []buildRow
		for rows.Next() {
			var b buildRow
			var started time.Time
			var completed *time.Time
			if err := rows.Scan(&b.Service, &b.Status, &b.Output, &b.Error, &started, &completed); err != nil {
				log.Printf("build logs scan %s#%d: %v", repo, pr, err)
				continue
			}
			b.StartedAt = started.UTC().Format(time.RFC3339)
			if completed != nil {
				s := completed.UTC().Format(time.RFC3339)
				b.CompletedAt = &s
			}
			out = append(out, b)
		}
		if len(out) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no logs yet"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"kind": "build", "services": out})
	}
}

// previewRedeployHandler queues a redeploy for an existing preview.
func previewRedeployHandler(pool *pgxpool.Pool, deployer *Deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, pr, ok := parsePreviewPath(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		ref, found, err := loadPreviewRef(ctx, pool, repo, pr)
		if err != nil {
			log.Printf("redeploy load %s#%d: %v", repo, pr, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "preview not found"})
			return
		}

		// Fire and forget — Deploy owns its own 25-minute context. Same pattern
		// as the GitHub webhook path.
		go deployer.Deploy(ref)

		writeJSON(w, http.StatusAccepted, map[string]any{
			"ok":      true,
			"message": "redeploy queued",
		})
	}
}

// previewDestroyHandler force-destroys a preview stack synchronously.
func previewDestroyHandler(pool *pgxpool.Pool, deployer *Deployer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, pr, ok := parsePreviewPath(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		ref, found, err := loadPreviewRef(ctx, pool, repo, pr)
		if err != nil {
			log.Printf("destroy load %s#%d: %v", repo, pr, err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "preview not found"})
			return
		}

		// Destroy owns its own 1-minute context internally.
		deployer.Destroy(ref)

		// Hard-delete the DB row so the preview disappears from the dashboard
		// list. Webhook-driven closes keep the row (status='closed') for
		// history; manual admin deletes want a clean removal.
		delCtx, delCancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer delCancel()
		if _, err := pool.Exec(delCtx,
			`DELETE FROM previews WHERE repo_full_name=$1 AND pr_number=$2`,
			repo, pr); err != nil {
			log.Printf("destroy delete row %s#%d: %v", repo, pr, err)
		}

		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// --- Docker helpers ---------------------------------------------------------

// errContainerNotFound is returned by containerLogs when the target container
// doesn't exist anymore.
var errContainerNotFound = fmt.Errorf("container not found")

// exposedContainerName returns the name of the container that exposes the
// preview (i.e. has a traefik.enable=true label), falling back to the default
// "web" service naming used by the Dockerfile fallback path.
// Empty string + nil error means "no stack container exists".
func (d *Deployer) exposedContainerName(ctx context.Context, repo string, pr int) (string, error) {
	slug := slugify(repo)
	containers, err := d.listStackContainers(ctx, slug, pr)
	if err != nil {
		return "", err
	}
	if len(containers) == 0 {
		// Fallback: legacy single-container preview.
		legacy := fmt.Sprintf("hatch-preview-%s-%d", slug, pr)
		exists, err := d.containerExists(ctx, legacy)
		if err != nil {
			return "", err
		}
		if exists {
			return legacy, nil
		}
		return "", nil
	}

	// Prefer the container carrying traefik.enable=true (the exposed one).
	for _, c := range containers {
		if c.Labels["traefik.enable"] == "true" {
			if name := primaryContainerName(c.Names); name != "" {
				return name, nil
			}
		}
	}

	// Fallback 1: FallbackCompose default service name "web".
	webName := composeContainerName(slug, pr, "web")
	for _, c := range containers {
		if primaryContainerName(c.Names) == webName {
			return webName, nil
		}
	}

	// Fallback 2: first container in the stack — at least surface *something*.
	if name := primaryContainerName(containers[0].Names); name != "" {
		return name, nil
	}
	return "", nil
}

// containerLogs fetches the last `tail` lines of stdout+stderr from a
// container and demultiplexes the Docker stream framing.
//
// Docker stream framing (TTY=false), one frame is:
//
//	[0]    STREAM_TYPE (0=stdin, 1=stdout, 2=stderr)
//	[1:4]  reserved (zero)
//	[4:8]  big-endian uint32 payload size
//	[8:]   payload
//
// We concatenate stdout and stderr in arrival order — that matches what
// `docker logs` prints.
func (d *Deployer) containerLogs(ctx context.Context, name string, tail int) (string, error) {
	if tail <= 0 {
		tail = 200
	}
	q := url.Values{}
	q.Set("stdout", "1")
	q.Set("stderr", "1")
	q.Set("tail", strconv.Itoa(tail))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		d.dockerURL("/containers/"+url.PathEscape(name)+"/logs?"+q.Encode()), nil)
	if err != nil {
		return "", err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("logs do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", errContainerNotFound
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("logs http %d: %s", resp.StatusCode, truncate(string(b), 200))
	}

	return demuxDockerStream(resp.Body, 4*1024*1024) // cap: 4 MiB
}

// demuxDockerStream decodes Docker's multiplexed stdout/stderr framing and
// returns the concatenated payload. maxBytes is a defensive upper bound on
// total bytes read — larger streams are truncated with a trailing marker.
func demuxDockerStream(r io.Reader, maxBytes int) (string, error) {
	var buf []byte
	header := make([]byte, 8)
	read := 0
	for {
		if read >= maxBytes {
			buf = append(buf, []byte("\n…[truncated]\n")...)
			break
		}
		_, err := io.ReadFull(r, header)
		if err == io.EOF {
			break
		}
		if err == io.ErrUnexpectedEOF {
			// Some daemons terminate cleanly in the middle of a frame when
			// the container exits — treat it as end of stream.
			break
		}
		if err != nil {
			return "", fmt.Errorf("read frame header: %w", err)
		}
		// header[0] = stream type, header[1:4] reserved, header[4:8] size.
		size := binary.BigEndian.Uint32(header[4:8])
		if size == 0 {
			continue
		}
		// Bound the frame to what we're still allowed to read.
		remaining := maxBytes - read
		toRead := int(size)
		if toRead > remaining {
			toRead = remaining
		}
		payload := make([]byte, toRead)
		if _, err := io.ReadFull(r, payload); err != nil {
			return "", fmt.Errorf("read frame payload: %w", err)
		}
		// Discard any bytes we chose to skip to stay under maxBytes.
		if int(size) > toRead {
			if _, err := io.CopyN(io.Discard, r, int64(int(size)-toRead)); err != nil {
				return "", fmt.Errorf("discard overflow: %w", err)
			}
		}
		buf = append(buf, payload...)
		read += toRead
	}
	return string(buf), nil
}
