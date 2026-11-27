package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var errAwaitingGitHub = errors.New("GitHub pull request state does not match the delivery")

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
			ref.SHA, ref.Branch = pr.Head.SHA, pr.Head.Ref
		}

		tag, err := tx.Exec(ctx, `INSERT INTO kube_pull_request_events (repo_full_name, pr_number, head_sha, closed)
			VALUES ($1,$2,$3,$4) ON CONFLICT (repo_full_name, pr_number) DO UPDATE
			SET head_sha=EXCLUDED.head_sha, closed=EXCLUDED.closed
			WHERE kube_pull_request_events.closed IS DISTINCT FROM EXCLUDED.closed
			   OR kube_pull_request_events.head_sha IS DISTINCT FROM EXCLUDED.head_sha
			   OR $5::boolean`, ref.Repo, ref.PR, pr.Head.SHA, closed, intent == kubePullRedeploy && !closed)
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

func lockKubeGitHubPR(ctx context.Context, tx pgx.Tx, ref *PreviewRef) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(
		'hatch-kube-pr:' || lower($1::text) || '#' || $2::integer::text, 0))`, ref.Repo, ref.PR); err != nil {
		return err
	}
	// Reuse the existing spelling so case-only renames cannot claim a second
	// slot for the same public host. New rows use GitHub's case-insensitive key.
	return tx.QueryRow(ctx, `SELECT COALESCE((SELECT repo_full_name FROM previews
		WHERE lower(repo_full_name)=lower($1) AND pr_number=$2 ORDER BY id LIMIT 1), lower($1))`, ref.Repo, ref.PR).Scan(&ref.Repo)
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

func (d *Deployer) storeKubeWebhook(ctx context.Context, ref PreviewRef, intent kubePullIntent) error {
	if intent == kubePullClosed {
		ref.SHA = ""
	}
	_, err := d.pool.Exec(ctx, `INSERT INTO kube_github_deliveries
		(repo_full_name, pr_number, installation_id, commit_sha, closed)
		VALUES (lower($1),$2,$3,$4,$5) ON CONFLICT DO NOTHING`, ref.Repo, ref.PR, ref.InstallationID, ref.SHA, intent == kubePullClosed)
	return err
}

func (d *Deployer) runKubeWebhookWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		worked, err := d.processKubeWebhook(ctx, d.githubPullState)
		if err != nil && ctx.Err() == nil {
			log.Printf("process Kubernetes GitHub delivery: %v", err)
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

func (d *Deployer) processKubeWebhook(ctx context.Context, read pullStateReader) (bool, error) {
	var id int64
	var mismatches int16
	var ref PreviewRef
	var closed bool
	err := d.pool.QueryRow(ctx, `WITH next AS (
		SELECT id FROM kube_github_deliveries WHERE available_at <= NOW()
		ORDER BY available_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
	)
	UPDATE kube_github_deliveries d SET available_at=NOW() + interval '30 seconds'
	FROM next WHERE d.id=next.id
	RETURNING d.id,d.repo_full_name,d.pr_number,d.installation_id,d.commit_sha,d.closed,d.mismatches`).Scan(
		&id, &ref.Repo, &ref.PR, &ref.InstallationID, &ref.SHA, &closed, &mismatches)
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
	_, err = d.queueKubePullRequest(attempt, ref, intent, 0, read)
	if errors.Is(err, errAwaitingGitHub) {
		// A valid but different snapshot can be a delayed delivery or a stale
		// GitHub read. Retry briefly, then keep the canonical newer state.
		if mismatches < 4 {
			_, err = d.pool.Exec(ctx, `UPDATE kube_github_deliveries
				SET mismatches=mismatches+1, available_at=NOW() + interval '5 seconds' WHERE id=$1`, id)
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
