package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// issueCommentEvent is the GitHub `issue_comment` payload.
// We only ever care about comments on PRs (issue.pull_request != null).
type issueCommentEvent struct {
	Action string `json:"action"`
	Issue  struct {
		Number      int              `json:"number"`
		PullRequest *json.RawMessage `json:"pull_request,omitempty"`
		User        struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"issue"`
	Comment struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
			Type  string `json:"type"` // "Bot" / "User"
		} `json:"user"`
	} `json:"comment"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// commentVerb is the action requested by the @mention.
type commentVerb int

const (
	verbNone commentVerb = iota
	verbDeploy
	verbDestroy
)

// commentACK is the short reply we post when a verb was understood but we
// rely on the existing sticky comment to reflect progress.
const (
	commentMentionAck     = "👀 OK, je m'en occupe."
	commentMentionUnauthz = "🚫 Cette action est réservée à l'auteur de la PR ou aux collaborateurs du repo."
	commentMentionClosed  = "🚫 Cette PR est fermée — rouvre-la d'abord pour relancer une preview."
)

// handleIssueCommentEvent handles the `issue_comment` webhook event. It
// reuses the same authorization gates as the `pull_request` handler (owner
// allowlist + bot/Bot guard) and routes the parsed verb to the deployer.
func handleIssueCommentEvent(w http.ResponseWriter, r *http.Request, body []byte, pool *pgxpool.Pool, deployer *Deployer, app *AppClient, allowedOwners map[string]bool, botHandle string) {
	var ev issueCommentEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if ev.Action != "created" {
		writeOK(w)
		return
	}
	if ev.Issue.PullRequest == nil {
		// Plain issue, not a PR. Ignore.
		writeOK(w)
		return
	}
	// Don't react to the bot's own comments — closes the obvious loop where
	// the sticky comment edit could re-trigger us if GitHub ever sends one.
	if strings.EqualFold(ev.Comment.User.Type, "Bot") {
		writeOK(w)
		return
	}

	owner, repo, ok := splitRepo(ev.Repository.FullName)
	if !ok || !allowedOwners[strings.ToLower(owner)] {
		log.Printf("issue_comment: owner %q not in allowlist, skipping %s#%d",
			owner, ev.Repository.FullName, ev.Issue.Number)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true,"skipped":"unauthorized owner"}`))
		return
	}

	verb, mentioned := parseMention(ev.Comment.Body, botHandle)
	if !mentioned {
		writeOK(w)
		return
	}

	if app == nil || ev.Installation.ID == 0 {
		// We can't act without an installation token — ignore quietly.
		log.Printf("issue_comment: no app/install token for %s#%d, skipping",
			ev.Repository.FullName, ev.Issue.Number)
		writeOK(w)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	pr, err := app.GetPullRequest(ctx, ev.Installation.ID, owner, repo, ev.Issue.Number)
	if err != nil {
		log.Printf("issue_comment: get pr %s#%d: %v", ev.Repository.FullName, ev.Issue.Number, err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	// Refuse fork PRs the same way the pull_request handler does — the head
	// SHA lives on a fork we don't trust.
	headFull := strings.ToLower(strings.TrimSpace(pr.Head.Repo.FullName))
	baseFull := strings.ToLower(strings.TrimSpace(ev.Repository.FullName))
	if headFull == "" || headFull != baseFull {
		log.Printf("issue_comment: fork PR %s -> %s#%d, skipping",
			pr.Head.Repo.FullName, ev.Repository.FullName, ev.Issue.Number)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true,"skipped":"fork PR"}`))
		return
	}

	authorized, err := commenterAuthorized(ctx, app, ev.Installation.ID, owner, repo, ev.Comment.User.Login, pr.User.Login)
	if err != nil {
		log.Printf("issue_comment: authz %s#%d: %v", ev.Repository.FullName, ev.Issue.Number, err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	if !authorized {
		_, _ = app.CommentPR(ctx, ev.Installation.ID, owner, repo, ev.Issue.Number, commentMentionUnauthz)
		writeOK(w)
		return
	}

	ref := PreviewRef{
		Repo:           ev.Repository.FullName,
		PR:             ev.Issue.Number,
		Branch:         pr.Head.Ref,
		SHA:            pr.Head.SHA,
		InstallationID: ev.Installation.ID,
	}

	switch verb {
	case verbDeploy:
		if pr.State != "open" {
			_, _ = app.CommentPR(ctx, ev.Installation.ID, owner, repo, ev.Issue.Number, commentMentionClosed)
			writeOK(w)
			return
		}
		// Track the preview as pending and (re)post the sticky comment so the
		// deployer's notifier has a comment id to update.
		if err := upsertPendingPreview(ctx, pool, ref); err != nil {
			log.Printf("issue_comment: upsert %s#%d: %v", ev.Repository.FullName, ev.Issue.Number, err)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		// Acknowledge so the user sees feedback immediately, then ensure the
		// sticky comment exists so OnStatusChange has something to update.
		_, _ = app.CommentPR(ctx, ev.Installation.ID, owner, repo, ev.Issue.Number, commentMentionAck)
		if err := ensureMentionStickyComment(ctx, pool, app, ev.Installation.ID, owner, repo, ev.Issue.Number, pr.Head.SHA); err != nil {
			log.Printf("issue_comment: sticky comment %s#%d: %v", ev.Repository.FullName, ev.Issue.Number, err)
		}
		ref.CommentID = loadCommentID(ctx, pool, ref.Repo, ref.PR)
		if !deployer.DeployAsync(ref) {
			http.Error(w, "deploy queue full", http.StatusServiceUnavailable)
			return
		}

	case verbDestroy:
		if !previewExists(ctx, pool, ref.Repo, ref.PR) {
			// Nothing to destroy — silent OK.
			writeOK(w)
			return
		}
		ref.CommentID = loadCommentID(ctx, pool, ref.Repo, ref.PR)
		_, _ = app.CommentPR(ctx, ev.Installation.ID, owner, repo, ev.Issue.Number, commentMentionAck)
		if !deployer.DestroyAsync(ref) {
			http.Error(w, "destroy queue full", http.StatusServiceUnavailable)
			return
		}
	}
	writeOK(w)
}

// commenterAuthorized returns true when the comment author is the PR author
// or has push access to the repo.
func commenterAuthorized(ctx context.Context, app *AppClient, installID int64, owner, repo, commenter, prAuthor string) (bool, error) {
	if commenter == "" {
		return false, errors.New("empty commenter login")
	}
	if strings.EqualFold(commenter, prAuthor) {
		return true, nil
	}
	return app.IsCollaborator(ctx, installID, owner, repo, commenter)
}

// upsertPendingPreview is the equivalent of handlePullRequest's INSERT used
// by the issue_comment path, where we don't have a prEvent payload.
func upsertPendingPreview(ctx context.Context, pool *pgxpool.Pool, ref PreviewRef) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO previews (repo_full_name, pr_number, branch, commit_sha, status, installation_id)
		VALUES ($1, $2, $3, $4, 'pending', $5)
		ON CONFLICT (repo_full_name, pr_number) DO UPDATE
		SET branch = EXCLUDED.branch,
		    commit_sha = EXCLUDED.commit_sha,
		    status = 'pending',
		    installation_id = EXCLUDED.installation_id,
		    updated_at = NOW()
	`, ref.Repo, ref.PR, ref.Branch, ref.SHA, nullableInt64(ref.InstallationID))
	return err
}

// ensureMentionStickyComment creates the "hatching" sticky comment when one
// does not yet exist for this PR. Mirrors ensurePRComment but does not need
// a prEvent.
func ensureMentionStickyComment(ctx context.Context, pool *pgxpool.Pool, app *AppClient, installID int64, owner, repo string, pr int, sha string) error {
	full := owner + "/" + repo
	if existing := loadCommentID(ctx, pool, full, pr); existing != 0 {
		// Reuse the existing sticky — flip the body to "rebuilding".
		body := fmt.Sprintf(commentRebuild, shortSHA(sha))
		return app.UpdateComment(ctx, installID, owner, repo, existing, body)
	}
	id, err := app.CommentPR(ctx, installID, owner, repo, pr, commentInitial)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx,
		`UPDATE previews SET comment_id=$1, updated_at=NOW() WHERE repo_full_name=$2 AND pr_number=$3`,
		id, full, pr)
	return err
}

// parseMention reports whether `body` mentions the bot, and returns the
// requested verb (default: deploy). Matching is case-insensitive.
func parseMention(body, botHandle string) (commentVerb, bool) {
	if botHandle == "" {
		return verbNone, false
	}
	body = strings.ToLower(body)
	handle := "@" + strings.ToLower(strings.TrimPrefix(botHandle, "@"))
	idx := strings.Index(body, handle)
	if idx < 0 {
		return verbNone, false
	}
	// Boundary check before the @ — must be at start, or preceded by a
	// non-word character. Avoids matching e.g. `mail@hatchpr.dev`.
	if idx > 0 && isWordChar(body[idx-1]) {
		return verbNone, false
	}
	// First whitespace-delimited token after the handle is the verb (or
	// empty if the mention is bare or trailed only by punctuation/EOL).
	rest := strings.TrimLeft(body[idx+len(handle):], " \t\r\n")
	verb := rest
	if end := strings.IndexAny(rest, " \t\r\n"); end >= 0 {
		verb = rest[:end]
	}
	switch strings.Trim(verb, ".,!?:;`*_-") {
	case "", "preview", "deploy", "redeploy", "rebuild":
		return verbDeploy, true
	case "delete", "destroy", "down", "kill", "stop":
		return verbDestroy, true
	default:
		// Mentioned but verb unknown — default to deploy. This makes
		// "@hatchpr please" or "@hatchpr 🙏" still trigger a build.
		return verbDeploy, true
	}
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '_' || b == '-' || b == '.'
}
