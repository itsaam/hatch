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
		ID        int64     `json:"id"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
		User      struct {
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
func handleIssueCommentEvent(w http.ResponseWriter, r *http.Request, body []byte, pool *pgxpool.Pool, deployer *Deployer, app *AppClient, allowedOwners map[string]bool, botHandle string, ownerCommandOnly bool) {
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

	verb, requestedSHA := parseMention(ev.Comment.Body, botHandle)
	if verb == verbNone {
		writeOK(w)
		return
	}
	if ownerCommandOnly && !strings.EqualFold(ev.Comment.User.Login, owner) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true,"skipped":"owner command required"}`))
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

	if !ownerCommandOnly {
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
	}

	if verb == verbDeploy && pr.State == "open" {
		if requestedSHA != "" {
			if !strings.EqualFold(requestedSHA, pr.Head.SHA) {
				_, _ = app.CommentPR(ctx, ev.Installation.ID, owner, repo, ev.Issue.Number,
					"That commit is no longer the head of this PR. Request a new preview for the current commit.")
				writeOK(w)
				return
			}
		} else if ownerCommandOnly {
			if !buildCommitPattern.MatchString(pr.Head.SHA) {
				http.Error(w, "invalid pull request head", http.StatusBadGateway)
				return
			}
			reply := fmt.Sprintf("Review the commit and confirm with `@%s preview %s`.", strings.TrimPrefix(botHandle, "@"), pr.Head.SHA)
			if _, err := app.CommentPR(ctx, ev.Installation.ID, owner, repo, ev.Issue.Number, reply); err != nil {
				http.Error(w, "internal", http.StatusInternalServerError)
				return
			}
			writeOK(w)
			return
		}
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
		var err error
		if deployer.kube != nil {
			var approvedAt *time.Time
			read := deployer.githubPullState
			if ownerCommandOnly {
				if ev.Comment.CreatedAt.IsZero() || ev.Comment.ID <= 0 {
					http.Error(w, "missing comment identity or creation time", http.StatusBadRequest)
					return
				}
				approvedAt = &ev.Comment.CreatedAt
				read = func(ctx context.Context, ref PreviewRef) (*pullState, error) {
					return app.ownerPreviewPullState(ctx, ref, *approvedAt)
				}
			}
			var queued bool
			queued, err = deployer.queueKubeObservedPullRequest(ctx, ref, kubePullRedeploy, ev.Comment.ID,
				requestedSHA != "", approvedAt, read)
			if errors.Is(err, errStaleOwnerApproval) {
				_, _ = app.CommentPR(ctx, ev.Installation.ID, owner, repo, ev.Issue.Number,
					"The PR changed after this command was created. Confirm the current commit again after reopening.")
				writeOK(w)
				return
			}
			if err == nil && !queued {
				writeOK(w)
				return
			}
		} else {
			_, err = pool.Exec(ctx, upsertPendingPreviewSQL, ref.Repo, ref.PR, ref.Branch, ref.SHA, nullableInt64(ref.InstallationID))
		}
		if err != nil {
			log.Printf("issue_comment: upsert %s#%d: %v", ev.Repository.FullName, ev.Issue.Number, err)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		// Acknowledge so the user sees feedback immediately, then ensure the
		// sticky comment exists so OnStatusChange has something to update.
		_, _ = app.CommentPR(ctx, ev.Installation.ID, owner, repo, ev.Issue.Number, commentMentionAck)
		if deployer.kube != nil {
			writeOK(w)
			return
		}
		if err := ensureMentionStickyComment(ctx, pool, app, ev.Installation.ID, owner, repo, ev.Issue.Number, pr.Head.SHA); err != nil {
			log.Printf("issue_comment: sticky comment %s#%d: %v", ev.Repository.FullName, ev.Issue.Number, err)
		}
		ref.CommentID = loadCommentID(ctx, pool, ref.Repo, ref.PR)
		if !deployer.DeployAsync(ref) {
			http.Error(w, "deploy queue full", http.StatusServiceUnavailable)
			return
		}

	case verbDestroy:
		if deployer.kube != nil {
			queued, err := deployer.queueKubeStopCommand(ctx, ref, ev.Comment.ID)
			if err != nil {
				log.Printf("issue_comment: queue stop %s#%d: %v", ref.Repo, ref.PR, err)
				http.Error(w, "preview queue unavailable", http.StatusServiceUnavailable)
				return
			}
			if queued {
				_, _ = app.CommentPR(ctx, ev.Installation.ID, owner, repo, ev.Issue.Number, commentMentionAck)
			}
			writeOK(w)
			return
		}
		if !previewExists(ctx, pool, ref.Repo, ref.PR) {
			// Nothing to destroy — silent OK.
			writeOK(w)
			return
		}
		ref.CommentID = loadCommentID(ctx, pool, ref.Repo, ref.PR)
		if !deployer.DestroyAsync(ref) {
			http.Error(w, "destroy queue full", http.StatusServiceUnavailable)
			return
		}
		_, _ = app.CommentPR(ctx, ev.Installation.ID, owner, repo, ev.Issue.Number, commentMentionAck)
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

const upsertPendingPreviewSQL = `
		INSERT INTO previews (repo_full_name, pr_number, branch, commit_sha, status, installation_id)
		VALUES ($1, $2, $3, $4, 'pending', $5)
		ON CONFLICT (repo_full_name, pr_number) DO UPDATE
		SET branch = EXCLUDED.branch,
		    commit_sha = EXCLUDED.commit_sha,
		    status = 'pending',
		    installation_id = EXCLUDED.installation_id,
		    updated_at = NOW()
	`

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

// parseMention recognizes an explicit command after the bot's exact handle.
// Matching is case-insensitive.
func parseMention(body, botHandle string) (commentVerb, string) {
	if botHandle == "" {
		return verbNone, ""
	}
	body = strings.ToLower(body)
	handle := "@" + strings.ToLower(strings.TrimPrefix(botHandle, "@"))
	command := strings.Fields(body)
	if len(command) >= 2 && command[0] == handle && command[1] == "preview" {
		if len(command) == 2 {
			return verbDeploy, ""
		}
		if len(command) == 3 && buildCommitPattern.MatchString(command[2]) {
			return verbDeploy, command[2]
		}
		return verbNone, ""
	}
	idx := strings.Index(body, handle)
	if idx < 0 {
		return verbNone, ""
	}
	// Boundary check before the @ — must be at start, or preceded by a
	// non-word character. Avoids matching e.g. `mail@hatchpr.dev`.
	if idx > 0 && isWordChar(body[idx-1]) {
		return verbNone, ""
	}
	rest := body[idx+len(handle):]
	if len(rest) == 0 || !strings.ContainsRune(" \t\r\n", rune(rest[0])) {
		return verbNone, ""
	}
	words := strings.Fields(rest)
	if len(words) == 0 {
		return verbNone, ""
	}
	switch strings.Trim(words[0], ".,!?:;`*_-") {
	case "delete", "destroy", "down", "kill", "stop":
		return verbDestroy, ""
	default:
		return verbNone, ""
	}
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '_' || b == '-' || b == '.'
}
