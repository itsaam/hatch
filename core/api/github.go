package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	signatureHeader = "X-Hub-Signature-256"
	eventHeader     = "X-GitHub-Event"
	maxWebhookBody  = 5 << 20

	commentInitial   = "🥚 Hatch is hatching your preview…"
	commentRebuild   = "🔄 Rebuilding preview for commit `%s`…"
	commentReady     = "✅ Preview ready: %s"
	commentFailed    = "❌ Preview failed. Check the API logs for details."
	commentTakenDown = "🧹 Preview taken down."
	commentHibernate = "💤 Preview hibernated after %d days of inactivity."
)

type prEvent struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		HTMLURL string `json:"html_url"`
		Head    struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// prNotifier bridges deployer status transitions to PR comment updates.
type prNotifier struct {
	pool *pgxpool.Pool
	app  *AppClient // may be nil when App integration disabled
}

func (n *prNotifier) OnStatusChange(ctx context.Context, ref PreviewRef, status, publicURL string) {
	if n == nil || n.app == nil {
		return
	}
	if ref.InstallationID == 0 || ref.CommentID == 0 {
		return
	}
	owner, repo, ok := splitRepo(ref.Repo)
	if !ok {
		return
	}

	var body string
	switch status {
	case "running":
		body = fmtSafe(commentReady, publicURL)
	case "failed":
		body = commentFailed
	case "building":
		// Intermediate state; leave the existing comment alone.
		return
	case "closed":
		body = commentTakenDown
	default:
		return
	}

	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := n.app.UpdateComment(cctx, ref.InstallationID, owner, repo, ref.CommentID, body); err != nil {
		log.Printf("update pr comment %s#%d: %v", ref.Repo, ref.PR, err)
	}
}

// OnHibernated satisfies the hibernateNotifier contract for the reaper.
func (n *prNotifier) OnHibernated(ctx context.Context, ref PreviewRef, days int) {
	if n == nil || n.app == nil {
		return
	}
	if ref.InstallationID == 0 || ref.CommentID == 0 {
		return
	}
	owner, repo, ok := splitRepo(ref.Repo)
	if !ok {
		return
	}
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	body := fmtSafe(commentHibernate, days)
	if err := n.app.UpdateComment(cctx, ref.InstallationID, owner, repo, ref.CommentID, body); err != nil {
		log.Printf("hibernate comment %s#%d: %v", ref.Repo, ref.PR, err)
	}
	_ = ctx
}

func githubWebhookHandler(pool *pgxpool.Pool, secret []byte, deployer *Deployer, app *AppClient, allowedOwners map[string]bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
		if err != nil {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}

		if !verifySignature(secret, r.Header.Get(signatureHeader), body) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}

		event := r.Header.Get(eventHeader)
		if event == "ping" {
			writeOK(w)
			return
		}
		if event != "pull_request" {
			writeOK(w)
			return
		}

		var ev prEvent
		if err := json.Unmarshal(body, &ev); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		// --- Authorization gates -------------------------------------
		// 1) Owner allowlist: HMAC authenticates the channel (GitHub
		//    really sent this), not the intent. The `hatchpr` App is
		//    public on GitHub's marketplace, so any stranger can install
		//    it on their repo and trigger our build farm. We refuse to
		//    build for owners not explicitly allowed.
		// 2) Fork PRs: the PR head SHA lives on the fork, under control
		//    of whoever opened it. GitHub Actions flags this as the
		//    `pull_request_target` class of risk. For now we never build
		//    fork PRs — contributors must either be repo collaborators
		//    or push directly to a branch of the base repo.
		// Both paths return 202 Accepted with a silent log: we avoid
		//    leaking which owners are allowed via response shape.
		owner, _, splitOK := splitRepo(ev.Repository.FullName)
		if !splitOK || !allowedOwners[strings.ToLower(owner)] {
			log.Printf("webhook: owner %q not in allowlist, skipping %s#%d",
				owner, ev.Repository.FullName, ev.Number)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"ok":true,"skipped":"unauthorized owner"}`))
			return
		}
		if isForkPR(ev) {
			log.Printf("webhook: fork PR %s → %s#%d, skipping",
				ev.PullRequest.Head.Repo.FullName, ev.Repository.FullName, ev.Number)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"ok":true,"skipped":"fork PR"}`))
			return
		}
		// -------------------------------------------------------------

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		if err := handlePullRequest(ctx, pool, app, ev); err != nil {
			log.Printf("handle pull_request: %v", err)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}

		ref := PreviewRef{
			Repo:           ev.Repository.FullName,
			PR:             ev.Number,
			Branch:         ev.PullRequest.Head.Ref,
			SHA:            ev.PullRequest.Head.SHA,
			InstallationID: ev.Installation.ID,
		}
		// Load the comment id persisted (if any) so notifier can update it.
		ref.CommentID = loadCommentID(ctx, pool, ref.Repo, ref.PR)

		switch ev.Action {
		case "opened", "reopened", "synchronize":
			if !deployer.DeployAsync(ref) {
				// Queue full or shutting down. 503 lets GitHub retry the
				// webhook on its own backoff schedule.
				http.Error(w, "deploy queue full", http.StatusServiceUnavailable)
				return
			}
		case "closed":
			if !deployer.DestroyAsync(ref) {
				http.Error(w, "destroy queue full", http.StatusServiceUnavailable)
				return
			}
		}

		writeOK(w)
	}
}

func handlePullRequest(ctx context.Context, pool *pgxpool.Pool, app *AppClient, ev prEvent) error {
	repo := ev.Repository.FullName
	pr := ev.Number
	branch := ev.PullRequest.Head.Ref
	sha := ev.PullRequest.Head.SHA
	installationID := ev.Installation.ID

	switch ev.Action {
	case "opened", "reopened", "synchronize":
		_, err := pool.Exec(ctx, `
			INSERT INTO previews (repo_full_name, pr_number, branch, commit_sha, status, installation_id)
			VALUES ($1, $2, $3, $4, 'pending', $5)
			ON CONFLICT (repo_full_name, pr_number) DO UPDATE
			SET branch = EXCLUDED.branch,
			    commit_sha = EXCLUDED.commit_sha,
			    status = 'pending',
			    installation_id = EXCLUDED.installation_id,
			    updated_at = NOW()
		`, repo, pr, branch, sha, nullableInt64(installationID))
		if err != nil {
			return err
		}
		log.Printf("preview upserted: %s#%d @ %s", repo, pr, shortSHA(sha))

		if app != nil && installationID != 0 {
			if err := ensurePRComment(ctx, pool, app, ev); err != nil {
				log.Printf("ensure pr comment %s#%d: %v", repo, pr, err)
			}
		}

	case "closed":
		_, err := pool.Exec(ctx, `
			UPDATE previews
			SET status = 'closed', updated_at = NOW()
			WHERE repo_full_name = $1 AND pr_number = $2
		`, repo, pr)
		if err != nil {
			return err
		}
		log.Printf("preview closed: %s#%d", repo, pr)
	}
	return nil
}

// ensurePRComment posts the initial comment on first event or updates it for
// subsequent `synchronize` actions.
func ensurePRComment(ctx context.Context, pool *pgxpool.Pool, app *AppClient, ev prEvent) error {
	owner, repo, ok := splitRepo(ev.Repository.FullName)
	if !ok {
		return errors.New("invalid repo full name")
	}

	existing := loadCommentID(ctx, pool, ev.Repository.FullName, ev.Number)

	if existing == 0 {
		body := commentInitial
		if ev.Action == "synchronize" {
			body = fmtSafe(commentRebuild, shortSHA(ev.PullRequest.Head.SHA))
		}
		id, err := app.CommentPR(ctx, ev.Installation.ID, owner, repo, ev.Number, body)
		if err != nil {
			return err
		}
		_, err = pool.Exec(ctx,
			`UPDATE previews SET comment_id=$1, updated_at=NOW() WHERE repo_full_name=$2 AND pr_number=$3`,
			id, ev.Repository.FullName, ev.Number)
		return err
	}

	// Existing comment: update the body to reflect the new action.
	body := commentInitial
	if ev.Action == "synchronize" {
		body = fmtSafe(commentRebuild, shortSHA(ev.PullRequest.Head.SHA))
	}
	return app.UpdateComment(ctx, ev.Installation.ID, owner, repo, existing, body)
}

func loadCommentID(ctx context.Context, pool *pgxpool.Pool, repo string, pr int) int64 {
	var id *int64
	err := pool.QueryRow(ctx,
		`SELECT comment_id FROM previews WHERE repo_full_name=$1 AND pr_number=$2`,
		repo, pr).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		log.Printf("load comment id %s#%d: %v", repo, pr, err)
		return 0
	}
	if id == nil {
		return 0
	}
	return *id
}

// isForkPR reports whether the PR's head (code to be built) lives in a
// different repository than the base. A fork PR means the attacker controls
// the build context; we refuse to deploy those automatically.
// A missing head repo (deleted fork) is treated as a fork for safety.
func isForkPR(ev prEvent) bool {
	head := strings.ToLower(strings.TrimSpace(ev.PullRequest.Head.Repo.FullName))
	base := strings.ToLower(strings.TrimSpace(ev.Repository.FullName))
	if head == "" {
		return true
	}
	return head != base
}

// parseAllowedOwners turns a CSV env var ("alice,Bob,charlie") into a
// lowercase set. Empty string / whitespace entries are ignored.
func parseAllowedOwners(csv string) map[string]bool {
	out := map[string]bool{}
	for _, raw := range strings.Split(csv, ",") {
		o := strings.ToLower(strings.TrimSpace(raw))
		if o != "" {
			out[o] = true
		}
	}
	return out
}

func verifySignature(secret []byte, header string, body []byte) bool {
	if len(secret) == 0 {
		return false
	}
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	got, err := hex.DecodeString(header[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	want := mac.Sum(nil)
	return hmac.Equal(got, want)
}

func shortSHA(s string) string {
	if len(s) < 7 {
		return s
	}
	return s[:7]
}

func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// fmtSafe wraps fmt.Sprintf to keep comment formatting in one place.
func fmtSafe(tmpl string, args ...any) string {
	return fmt.Sprintf(tmpl, args...)
}
