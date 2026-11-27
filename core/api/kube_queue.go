package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type kubeOperation struct {
	id         int64
	kind       string
	expiryDays int
	ref        PreviewRef
}

func (d *Deployer) enqueueKubeOperation(ref PreviewRef, operation string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var sha string
		if err := tx.QueryRow(ctx, `SELECT commit_sha FROM previews WHERE repo_full_name=$1 AND pr_number=$2 FOR UPDATE`, ref.Repo, ref.PR).Scan(&sha); err != nil {
			return err
		}
		if operation != "close" && sha != ref.SHA {
			return errors.New("preview changed before the operation was queued")
		}
		return enqueueKubeTx(ctx, tx, ref, operation)
	})
	if err != nil {
		log.Printf("queue Kubernetes %s %s#%d: %v", operation, ref.Repo, ref.PR, err)
	}
	return err == nil
}

func enqueueKubeTx(ctx context.Context, tx pgx.Tx, ref PreviewRef, operation string) error {
	_, err := tx.Exec(ctx, `INSERT INTO kube_operations (repo_full_name, pr_number, operation)
		VALUES ($1, $2, $3) ON CONFLICT (repo_full_name, pr_number) DO UPDATE
		SET id=EXCLUDED.id, operation=EXCLUDED.operation, available_at=NOW(), expiry_days=0`, ref.Repo, ref.PR, operation)
	return err
}

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

func (d *Deployer) queueKubePullRequest(ctx context.Context, ref PreviewRef, intent kubePullIntent, read pullStateReader) (bool, error) {
	var queued bool
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		// GitHub webhook timestamps cannot order changes within one second.
		// Serialize the fresh read and its queue write for this PR. No Kubernetes
		// request is made under this lock, and independent PRs keep their own locks.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(
			'hatch-kube-pr:' || lower($1::text) || '#' || $2::integer::text, 0))`, ref.Repo, ref.PR); err != nil {
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
			if intent == kubePullClosed || !strings.EqualFold(pr.Head.Repo.FullName, ref.Repo) {
				return nil
			}
			if pr.Head.SHA == "" || pr.Head.Ref == "" {
				return errors.New("GitHub pull request head is incomplete")
			}
			if intent == kubePullChanged && pr.Head.SHA != ref.SHA {
				return nil
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

func (d *Deployer) enqueueKubeExpiry(ctx context.Context, ref PreviewRef, days int) error {
	_, err := d.pool.Exec(ctx, `INSERT INTO kube_operations (repo_full_name, pr_number, operation, expiry_days)
		SELECT repo_full_name, pr_number, 'expire', $4 FROM previews
		WHERE repo_full_name=$1 AND pr_number=$2 AND commit_sha=$3 AND status='running'
		AND updated_at < NOW() - ($5::integer * interval '1 hour')
		ON CONFLICT (repo_full_name, pr_number) DO NOTHING`,
		ref.Repo, ref.PR, ref.SHA, days, getenvInt("PREVIEW_TTL_HOURS", 168))
	return err
}

func (d *Deployer) startKubeWorkers(ctx context.Context, count int) {
	for i := 0; i < max(1, count); i++ {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			d.runKubeWorker(ctx)
		}()
	}
}

func (d *Deployer) runKubeWorker(ctx context.Context) {
	for ctx.Err() == nil {
		job, err := d.claimKubeOperation(ctx)
		if err == nil {
			d.runKubeOperation(ctx, job)
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("claim Kubernetes operation: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (d *Deployer) claimKubeOperation(ctx context.Context) (kubeOperation, error) {
	var job kubeOperation
	err := d.pool.QueryRow(ctx, `WITH next AS (
		SELECT id FROM kube_operations WHERE available_at <= NOW()
		ORDER BY available_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
	)
	UPDATE kube_operations AS op SET available_at=NOW() + interval '30 minutes'
	FROM next, previews AS p WHERE op.id=next.id
		AND p.repo_full_name=op.repo_full_name AND p.pr_number=op.pr_number
	RETURNING op.id, op.operation, op.expiry_days, p.repo_full_name, p.pr_number, p.branch, p.commit_sha,
		COALESCE(p.installation_id, 0), COALESCE(p.comment_id, 0)`).Scan(
		&job.id, &job.kind, &job.expiryDays, &job.ref.Repo, &job.ref.PR, &job.ref.Branch, &job.ref.SHA, &job.ref.InstallationID, &job.ref.CommentID)
	return job, err
}

func (d *Deployer) runKubeOperation(ctx context.Context, job kubeOperation) {
	ref := job.ref
	lock := d.acquireLock(ref.Repo, ref.PR)
	if !lock.TryLock() {
		d.releaseLock(ref.Repo, ref.PR)
		d.releaseKubeOperation(job, 1)
		return
	}
	defer func() { lock.Unlock(); d.releaseLock(ref.Repo, ref.PR) }()

	var current bool
	if err := d.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kube_operations WHERE id=$1)`, job.id).Scan(&current); err != nil || !current {
		if err != nil {
			d.releaseKubeOperation(job, 0)
		}
		return
	}
	if job.kind == "deploy" {
		d.runKubeDeploy(ctx, job)
		return
	}

	removeCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	removeRef := ref
	removeRef.SHA = ""
	if err := d.kube.destroy(removeCtx, removeRef); err != nil {
		log.Printf("remove Kubernetes preview %s#%d: %v", ref.Repo, ref.PR, err)
		d.releaseKubeOperation(job, 30)
		return
	}
	status := "closed"
	if job.kind == "expire" {
		status = "expired"
	}
	if job.kind == "failed_cleanup" {
		status = "failed"
	}
	d.completeKubeOperation(ctx, job, status, "")
}

func (d *Deployer) runKubeDeploy(ctx context.Context, job kubeOperation) {
	ref := job.ref
	if !d.updateKubeStatus(ctx, job, "building") {
		d.releaseKubeOperation(job, 0)
		return
	}
	deployCtx, cancel := context.WithTimeout(ctx, 25*time.Minute)
	defer cancel()
	go d.cancelSupersededDeploy(deployCtx, job.id, cancel)
	if owner, repo, ok := splitRepo(ref.Repo); ok && d.app != nil && ref.InstallationID != 0 {
		commentCtx, stop := context.WithTimeout(deployCtx, 5*time.Second)
		if err := ensureMentionStickyComment(commentCtx, d.pool, d.app, ref.InstallationID, owner, repo, ref.PR, ref.SHA); err != nil {
			log.Printf("ensure Kubernetes preview comment %s#%d: %v", ref.Repo, ref.PR, err)
		}
		stop()
	}
	host := kubePreviewHost(ref, d.domain)
	spec, _, err := loadComposeForRef(deployCtx, d.httpExt, d.app, ref.InstallationID, ref.Repo, ref.SHA)
	if err == nil {
		d.preparePreviewSpec(deployCtx, ref, spec, host)
		err = d.deployKubeSpec(deployCtx, job, spec, host)
	}
	if ctx.Err() != nil {
		d.releaseKubeOperation(job, 0)
		return
	}
	if errors.Is(err, errKubeSlotsFull) {
		d.updateKubeStatus(ctx, job, "pending")
		d.releaseKubeOperation(job, 30)
		return
	}
	if err != nil {
		log.Printf("deploy Kubernetes preview %s#%d: %v", ref.Repo, ref.PR, scrubToken(err))
		if _, err := d.pool.Exec(ctx, `UPDATE kube_operations SET operation='failed_cleanup', available_at=NOW() WHERE id=$1`, job.id); err != nil {
			log.Printf("queue Kubernetes cleanup %s#%d: %v", ref.Repo, ref.PR, err)
			d.releaseKubeOperation(job, 30)
		}
		return
	}
	publicURL := ""
	if ExposedService(spec) != "" {
		publicURL = "https://" + host
	}
	d.completeKubeOperation(ctx, job, "running", publicURL)
}

func (d *Deployer) cancelSupersededDeploy(ctx context.Context, id int64, cancel context.CancelFunc) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkCtx, stopCheck := context.WithTimeout(ctx, 3*time.Second)
			var current bool
			err := d.pool.QueryRow(checkCtx, `SELECT EXISTS(SELECT 1 FROM kube_operations WHERE id=$1)`, id).Scan(&current)
			stopCheck()
			if err == nil && !current {
				cancel()
				return
			}
		}
	}
}

func (d *Deployer) deployKubeSpec(ctx context.Context, job kubeOperation, spec *ComposeSpec, host string) error {
	ref := job.ref
	var source string
	defer func() {
		if source != "" {
			_ = os.RemoveAll(source)
		}
	}()
	return d.kube.deploy(ctx, ref, spec, host, func(ctx context.Context, name string, svc *ComposeService) (string, int, error) {
		if source == "" {
			var err error
			source, err = os.MkdirTemp("", "hatch-source-")
			if err != nil {
				return "", 0, err
			}
			if err := d.downloadBuildSource(ctx, ref, source); err != nil {
				return "", 0, err
			}
		}
		return d.buildKubeImage(ctx, ref, name, svc, source)
	}, func(ctx context.Context, sqlPath string) ([]byte, error) {
		return fetchRepoFile(ctx, d.httpExt, d.app, ref.InstallationID, ref.Repo, sqlPath, ref.SHA)
	}, func(ctx context.Context, publish func(context.Context) error) error {
		return d.publishCurrentKubeOperation(ctx, job, d.githubPullState, publish)
	})
}

func (d *Deployer) publishCurrentKubeOperation(ctx context.Context, job kubeOperation, read pullStateReader, publish func(context.Context) error) error {
	check := func() error {
		if err := d.checkKubePublication(ctx, job); err != nil {
			return err
		}
		current, err := read(ctx, job.ref)
		if err != nil {
			return err
		}
		if current.State != "open" || !strings.EqualFold(current.Head.Repo.FullName, job.ref.Repo) {
			return errors.New("GitHub pull request is no longer open in its original repository")
		}
		return d.checkKubePublication(ctx, job)
	}
	if err := check(); err != nil {
		return err
	}
	if err := publish(ctx); err != nil {
		return err
	}
	// GitHub may change before the webhook reaches us. Recheck both sources
	// so the worker can tear down the superseded slot under its PR lock.
	return check()
}

func (d *Deployer) checkKubePublication(ctx context.Context, job kubeOperation) error {
	var current bool
	err := d.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM kube_operations op
		JOIN previews p USING (repo_full_name, pr_number) WHERE op.id=$1 AND p.commit_sha=$2)`, job.id, job.ref.SHA).Scan(&current)
	if err == nil && !current {
		err = errors.New("preview operation was superseded during publication")
	}
	return err
}

func kubePreviewHost(ref PreviewRef, domain string) string {
	id := sha256.Sum256([]byte(strings.ToLower(ref.Repo)))
	label := fmt.Sprintf("pr-%d-%s", ref.PR, slugify(ref.Repo))
	if len(label) > 50 {
		label = strings.TrimRight(label[:50], "-")
	}
	return fmt.Sprintf("%s-%x.%s", label, id[:6], domain)
}

func (d *Deployer) updateKubeStatus(ctx context.Context, job kubeOperation, status string) bool {
	tag, err := d.pool.Exec(ctx, `UPDATE previews SET status=$1, updated_at=NOW() WHERE repo_full_name=$2 AND pr_number=$3
		AND commit_sha=$5 AND EXISTS (SELECT 1 FROM kube_operations WHERE id=$4)`, status, job.ref.Repo, job.ref.PR, job.id, job.ref.SHA)
	if err != nil {
		log.Printf("set Kubernetes preview %s status: %v", status, err)
		return false
	}
	if tag.RowsAffected() > 0 {
		d.notifier.OnStatusChange(ctx, job.ref, status, "")
		return true
	}
	return false
}

func (d *Deployer) completeKubeOperation(ctx context.Context, job kubeOperation, status, publicURL string) {
	ref := job.ref
	var applied bool
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		var sha string
		if err := tx.QueryRow(ctx, `SELECT commit_sha FROM previews WHERE repo_full_name=$1 AND pr_number=$2 FOR UPDATE`, ref.Repo, ref.PR).Scan(&sha); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM kube_operations WHERE id=$1`, job.id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		if sha != ref.SHA {
			return nil
		}
		if job.kind == "delete" {
			tag, err = tx.Exec(ctx, `DELETE FROM previews WHERE repo_full_name=$1 AND pr_number=$2`, ref.Repo, ref.PR)
			applied = tag.RowsAffected() > 0
			return err
		}
		tag, err = tx.Exec(ctx, `UPDATE previews SET status=$1, url=NULLIF($2,''), updated_at=NOW()
			WHERE repo_full_name=$3 AND pr_number=$4 AND commit_sha=$5`, status, publicURL, ref.Repo, ref.PR, ref.SHA)
		applied = tag.RowsAffected() > 0
		return err
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("complete Kubernetes operation %d: %v", job.id, err)
			d.releaseKubeOperation(job, 5)
		}
		return
	}
	if !applied {
		return
	}
	if job.kind == "expire" {
		if notifier, ok := d.notifier.(hibernateNotifier); ok && ref.InstallationID != 0 && ref.CommentID != 0 {
			notifier.OnHibernated(ctx, ref, job.expiryDays)
		}
		return
	}
	if ref.CommentID == 0 && ref.InstallationID != 0 {
		ref.CommentID = loadCommentID(ctx, d.pool, ref.Repo, ref.PR)
	}
	d.notifier.OnStatusChange(ctx, ref, status, publicURL)
}

func (d *Deployer) releaseKubeOperation(job kubeOperation, delaySeconds int) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := d.pool.Exec(ctx, `UPDATE kube_operations SET available_at=NOW() + ($2::integer * interval '1 second') WHERE id=$1`, job.id, delaySeconds); err != nil {
		log.Printf("release Kubernetes operation %d: %v", job.id, err)
	}
}

func (d *Deployer) reconcileKube(ctx context.Context) error {
	// Recreate keeps a single worker Deployment. A previous process may have
	// exited before releasing a claimed operation. Replaying is safe per slot.
	if _, err := d.pool.Exec(ctx, `UPDATE kube_operations SET available_at=NOW()`); err != nil {
		return err
	}
	rows, err := d.pool.Query(ctx, `SELECT repo_full_name, pr_number FROM previews p
		WHERE status IN ('pending','building','running') AND NOT EXISTS (
		SELECT 1 FROM kube_operations op WHERE op.repo_full_name=p.repo_full_name AND op.pr_number=p.pr_number)`)
	if err != nil {
		return err
	}
	var refs []PreviewRef
	for rows.Next() {
		var ref PreviewRef
		if err := rows.Scan(&ref.Repo, &ref.PR); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, ref := range refs {
		exists, err := d.kube.exists(ctx, ref)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := d.pool.Exec(ctx, `UPDATE previews SET status='failed', url=NULL, updated_at=NOW()
				WHERE repo_full_name=$1 AND pr_number=$2`, ref.Repo, ref.PR); err != nil {
				return err
			}
		}
	}
	return nil
}
