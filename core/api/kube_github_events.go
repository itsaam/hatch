package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	errAwaitingGitHub     = errors.New("GitHub pull request state does not match the delivery")
	errSupersededDelivery = errors.New("a newer GitHub state superseded the delivery")
	errStaleOwnerApproval = errors.New("pull request changed after the owner approval")
)

type kubePullIntent uint8

const (
	kubePullChanged kubePullIntent = iota
	kubePullClosed
	kubePullRedeploy
)

type pullStateReader func(context.Context, PreviewRef) (*pullState, error)

func (d *Deployer) githubPullState(ctx context.Context, ref PreviewRef) (*pullState, error) {
	owner, repo, ok := splitRepo(ref.Repo)
	if !ok || ref.PR <= 0 {
		return nil, errors.New("invalid pull request")
	}
	if d.app != nil && ref.InstallationID != 0 {
		return d.app.GetPullRequest(ctx, ref.InstallationID, owner, repo, ref.PR)
	}
	return fetchPullRequest(ctx, d.httpExt, "https://api.github.com", "", owner, repo, ref.PR)
}

func (d *Deployer) queueKubePullRequest(ctx context.Context, ref PreviewRef, intent kubePullIntent, commentID int64, read pullStateReader) (bool, error) {
	return d.queueKubeObservedPullRequest(ctx, ref, intent, commentID, false, nil, read)
}

func (d *Deployer) queueKubeObservedPullRequest(ctx context.Context, ref PreviewRef, intent kubePullIntent, commentID int64, pinned bool, approvedAt *time.Time, read pullStateReader) (bool, error) {
	var queued bool
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		// GitHub webhook timestamps cannot order changes within one second.
		// Serialize the fresh read and its queue write for this PR. No Kubernetes
		// request is made under this lock, and independent PRs keep their own locks.
		if err := lockKubeGitHubPR(ctx, tx, &ref); err != nil {
			return err
		}
		fresh, err := rememberKubeCommand(ctx, tx, commentID)
		if err != nil || !fresh {
			return err
		}
		pr, err := read(ctx, ref)
		if err != nil {
			return err
		}
		if pr.State != "open" && pr.State != "closed" {
			return errors.New("invalid GitHub pull request state")
		}
		closed := pr.State == "closed"
		if approvedAt != nil && closed {
			return nil
		}
		if !closed {
			if intent == kubePullClosed {
				return errAwaitingGitHub
			}
			if !strings.EqualFold(pr.Head.Repo.FullName, ref.Repo) {
				return nil
			}
			if pr.Head.SHA == "" || pr.Head.Ref == "" {
				return errors.New("GitHub pull request head is incomplete")
			}
			if intent == kubePullChanged && pr.Head.SHA != ref.SHA {
				return errAwaitingGitHub
			}
			if pinned && pr.Head.SHA != ref.SHA {
				// This command observed a different head. Record it without
				// scheduling a build, so a redelivery cannot build the new head.
				return nil
			}
			ref.SHA, ref.Branch = pr.Head.SHA, pr.Head.Ref
		}

		tag, err := tx.Exec(ctx, `INSERT INTO kube_pull_request_events AS previous
			(repo_full_name, pr_number, head_sha, closed, owner_approved_at)
			SELECT $1,$2,$3,$4,$6::timestamptz
			WHERE $6::timestamptz IS NULL OR NOT EXISTS (
			    SELECT 1 FROM kube_github_deliveries
			    WHERE lower(repo_full_name)=lower($1) AND pr_number=$2 AND closed
			      AND source_updated_at >= $6
			)
			ON CONFLICT (repo_full_name, pr_number) DO UPDATE
			SET head_sha=EXCLUDED.head_sha, closed=EXCLUDED.closed,
			    owner_approved_at=COALESCE(EXCLUDED.owner_approved_at, previous.owner_approved_at)
			WHERE ($6::timestamptz IS NULL OR
			      ((previous.last_closed_at IS NULL OR previous.last_closed_at < $6)
			   AND (previous.owner_approved_at IS NULL OR previous.owner_approved_at <= $6)))
			  AND (previous.closed IS DISTINCT FROM EXCLUDED.closed
			    OR previous.head_sha IS DISTINCT FROM EXCLUDED.head_sha
			    OR $5::boolean)`, ref.Repo, ref.PR, pr.Head.SHA, closed, intent == kubePullRedeploy && !closed, approvedAt)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if err := queueKubePreviewTx(ctx, tx, ref, !closed); err != nil {
			return err
		}
		queued = !closed
		return nil
	})
	return queued && err == nil, err
}

func (d *Deployer) queueKubeOwnerClose(ctx context.Context, ref PreviewRef, closedAt time.Time) error {
	return pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if err := lockKubeGitHubPR(ctx, tx, &ref); err != nil {
			return err
		}
		// The signed close also fences old commands after a reopen. GitHub
		// timestamps have second precision, so a close wins an ambiguous tie.
		var closesPreview bool
		err := tx.QueryRow(ctx, `INSERT INTO kube_pull_request_events AS previous
			(repo_full_name, pr_number, head_sha, closed, last_closed_at)
			VALUES ($1,$2,$3,true,$4) ON CONFLICT (repo_full_name, pr_number) DO UPDATE
			SET last_closed_at=EXCLUDED.last_closed_at,
			    closed=previous.owner_approved_at IS NULL OR previous.owner_approved_at <= EXCLUDED.last_closed_at
			WHERE previous.last_closed_at IS NULL OR previous.last_closed_at < EXCLUDED.last_closed_at
			RETURNING closed`, ref.Repo, ref.PR, ref.SHA, closedAt).Scan(&closesPreview)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil || !closesPreview {
			return err
		}
		return queueKubePreviewTx(ctx, tx, ref, false)
	})
}

func lockKubeGitHubPR(ctx context.Context, tx pgx.Tx, ref *PreviewRef) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(
		'hatch-kube-pr:' || lower($1::text) || '#' || $2::integer::text, 0))`, ref.Repo, ref.PR); err != nil {
		return err
	}
	// Reuse the existing spelling so case-only renames cannot claim a second
	// slot. Preserve GitHub's spelling for new rows and existing secret keys.
	return tx.QueryRow(ctx, `SELECT COALESCE((SELECT repo_full_name FROM previews
		WHERE lower(repo_full_name)=lower($1) AND pr_number=$2 ORDER BY id LIMIT 1),
		(SELECT repo_full_name FROM kube_pull_request_events
		WHERE lower(repo_full_name)=lower($1) AND pr_number=$2 ORDER BY repo_full_name LIMIT 1), $1)`, ref.Repo, ref.PR).Scan(&ref.Repo)
}

func queueKubePreviewTx(ctx context.Context, tx pgx.Tx, ref PreviewRef, deploy bool) error {
	if deploy {
		if _, err := tx.Exec(ctx, upsertPendingPreviewSQL, ref.Repo, ref.PR, ref.Branch, ref.SHA, nullableInt64(ref.InstallationID)); err != nil {
			return err
		}
		return enqueueKubeTx(ctx, tx, ref, "deploy")
	}
	tag, err := tx.Exec(ctx, `UPDATE previews SET status='closed', updated_at=NOW()
		WHERE repo_full_name=$1 AND pr_number=$2`, ref.Repo, ref.PR)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	return enqueueKubeTx(ctx, tx, ref, "close")
}

func rememberKubeCommand(ctx context.Context, tx pgx.Tx, commentID int64) (bool, error) {
	if commentID == 0 {
		return true, nil
	}
	tag, err := tx.Exec(ctx, `INSERT INTO kube_chatops_commands (comment_id) VALUES ($1) ON CONFLICT DO NOTHING`, commentID)
	return err == nil && tag.RowsAffected() > 0, err
}

func (d *Deployer) queueKubeStopCommand(ctx context.Context, ref PreviewRef, commentID int64) (bool, error) {
	var queued bool
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		if err := lockKubeGitHubPR(ctx, tx, &ref); err != nil {
			return err
		}
		fresh, err := rememberKubeCommand(ctx, tx, commentID)
		if err != nil || !fresh {
			return err
		}
		var pr int
		if err := tx.QueryRow(ctx, `SELECT pr_number FROM previews WHERE repo_full_name=$1 AND pr_number=$2 FOR UPDATE`, ref.Repo, ref.PR).Scan(&pr); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		if err := enqueueKubeTx(ctx, tx, ref, "close"); err != nil {
			return err
		}
		queued = true
		return nil
	})
	return queued && err == nil, err
}

func (d *Deployer) storeKubeWebhook(ctx context.Context, ref PreviewRef, intent kubePullIntent, updatedAt time.Time) error {
	if intent == kubePullClosed {
		ref.SHA = ""
	}
	var sourceUpdatedAt *time.Time
	if !updatedAt.IsZero() {
		sourceUpdatedAt = &updatedAt
	}
	_, err := d.pool.Exec(ctx, `INSERT INTO kube_github_deliveries
		(repo_full_name, pr_number, installation_id, commit_sha, closed, source_updated_at)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (repo_full_name, pr_number, commit_sha, closed) DO UPDATE
		SET id=EXCLUDED.id, installation_id=EXCLUDED.installation_id, available_at=NOW(), mismatches=0,
		    source_updated_at=greatest(kube_github_deliveries.source_updated_at, EXCLUDED.source_updated_at)`,
		ref.Repo, ref.PR, ref.InstallationID, ref.SHA, intent == kubePullClosed, sourceUpdatedAt)
	return err
}

func (d *Deployer) runKubeWebhookWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		worked, err := d.processKubeWebhook(ctx, d.githubPullState, d.kubeAutoBuildAllowed)
		if err != nil && ctx.Err() == nil {
			log.Printf("process Kubernetes GitHub delivery: %v", err)
			if !errors.Is(err, errInvalidCompose) {
				d.monitor.report("github_delivery_failed", nil, nil)
			}
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Deployer) kubeAutoBuildAllowed(ctx context.Context, ref PreviewRef) (bool, error) {
	if d.previewOwnerOnly {
		return false, nil
	}
	decision, err := automaticPreviewTrigger(ctx, d.httpExt, d.app, ref)
	return decision == decisionBuild, err
}

func (d *Deployer) processKubeWebhook(ctx context.Context, read pullStateReader, autoAllowed func(context.Context, PreviewRef) (bool, error)) (bool, error) {
	var id int64
	var mismatches int16
	var sourceUpdatedAt *time.Time
	var ref PreviewRef
	var closed bool
	err := d.pool.QueryRow(ctx, `WITH next AS (
		SELECT id FROM kube_github_deliveries WHERE available_at <= NOW()
		ORDER BY available_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
	)
	UPDATE kube_github_deliveries d SET available_at=NOW() + interval '30 seconds'
	FROM next WHERE d.id=next.id
	RETURNING d.id,d.repo_full_name,d.pr_number,d.installation_id,d.commit_sha,d.closed,d.mismatches,d.source_updated_at`).Scan(
		&id, &ref.Repo, &ref.PR, &ref.InstallationID, &ref.SHA, &closed, &mismatches, &sourceUpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	intent := kubePullChanged
	if closed {
		intent = kubePullClosed
	}
	attempt, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if closed && d.previewOwnerOnly && sourceUpdatedAt != nil {
		if err := d.queueKubeOwnerClose(attempt, ref, *sourceUpdatedAt); err != nil {
			return true, err
		}
		_, err := d.pool.Exec(ctx, `DELETE FROM kube_github_deliveries WHERE id=$1`, id)
		return true, err
	}
	if !closed {
		allowed, err := autoAllowed(attempt, ref)
		if err != nil && !errors.Is(err, errInvalidCompose) {
			return true, err
		}
		if !allowed || err != nil {
			if _, deleteErr := d.pool.Exec(ctx, `DELETE FROM kube_github_deliveries WHERE id=$1`, id); deleteErr != nil {
				return true, deleteErr
			}
			return true, err
		}
	}
	observe := read
	if closed && sourceUpdatedAt != nil {
		observe = func(ctx context.Context, ref PreviewRef) (*pullState, error) {
			pr, err := read(ctx, ref)
			// Time is only evidence of a later reopening. Equal timestamps
			// remain ambiguous and must not discard the pending closure.
			if err == nil && pr.State == "open" && pr.UpdatedAt.After(*sourceUpdatedAt) {
				return nil, errSupersededDelivery
			}
			return pr, err
		}
	}
	_, err = d.queueKubePullRequest(attempt, ref, intent, 0, observe)
	if errors.Is(err, errSupersededDelivery) {
		err = nil
	}
	if errors.Is(err, errAwaitingGitHub) {
		// An old head can be superseded. A closure remains pending until the
		// canonical state confirms it, with a slower cadence after rapid retries.
		if mismatches < 4 || closed {
			delay := 5
			if mismatches >= 4 {
				delay = 120
			}
			_, err = d.pool.Exec(ctx, `UPDATE kube_github_deliveries
				SET mismatches=least(mismatches+1,5), available_at=NOW() + ($2::integer * interval '1 second') WHERE id=$1`, id, delay)
			return true, err
		}
		err = nil
	}
	if err != nil {
		// The lease already schedules the next attempt after transport/API failure.
		return true, err
	}
	_, err = d.pool.Exec(ctx, `DELETE FROM kube_github_deliveries WHERE id=$1`, id)
	return true, err
}
