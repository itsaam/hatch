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
	updatedAt  time.Time
}

func (d *Deployer) enqueueKubeOperation(ref PreviewRef, operation string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := d.pool.Exec(ctx, `INSERT INTO kube_operations (repo_full_name, pr_number, operation)
		SELECT repo_full_name, pr_number, $3 FROM previews WHERE repo_full_name=$1 AND pr_number=$2
		AND ($3 <> 'deploy' OR commit_sha=$4)
		ON CONFLICT (repo_full_name, pr_number) DO UPDATE
		SET id=EXCLUDED.id, operation=EXCLUDED.operation, available_at=NOW(), expiry_days=0`, ref.Repo, ref.PR, operation, ref.SHA)
	if err != nil {
		log.Printf("queue Kubernetes %s %s#%d: %v", operation, ref.Repo, ref.PR, err)
	}
	return err == nil
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
		COALESCE(p.installation_id, 0), COALESCE(p.comment_id, 0), p.updated_at`).Scan(
		&job.id, &job.kind, &job.expiryDays, &job.ref.Repo, &job.ref.PR, &job.ref.Branch, &job.ref.SHA, &job.ref.InstallationID, &job.ref.CommentID, &job.updatedAt)
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
	host := kubePreviewHost(ref, d.domain)
	spec, _, err := loadComposeForRef(deployCtx, d.httpExt, d.app, ref.InstallationID, ref.Repo, ref.SHA)
	if err == nil {
		d.preparePreviewSpec(deployCtx, ref, spec, host)
		err = d.deployKubeSpec(deployCtx, ref, spec, host)
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
		cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 90*time.Second)
		cleanupRef := ref
		cleanupRef.SHA = ""
		cleanupErr := d.kube.destroy(cleanupCtx, cleanupRef)
		cleanupCancel()
		if cleanupErr != nil {
			log.Printf("clean up failed Kubernetes preview %s#%d: %v", ref.Repo, ref.PR, cleanupErr)
			d.releaseKubeOperation(job, 30)
			return
		}
		d.completeKubeOperation(ctx, job, "failed", "")
		return
	}
	publicURL := ""
	if ExposedService(spec) != "" {
		publicURL = "https://" + host
	}
	d.completeKubeOperation(ctx, job, "running", publicURL)
}

func (d *Deployer) deployKubeSpec(ctx context.Context, ref PreviewRef, spec *ComposeSpec, host string) error {
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
	})
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
	err := pgx.BeginFunc(ctx, d.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM kube_operations WHERE id=$1`, job.id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		if job.kind == "delete" {
			_, err = tx.Exec(ctx, `DELETE FROM previews WHERE repo_full_name=$1 AND pr_number=$2 AND updated_at=$3`, ref.Repo, ref.PR, job.updatedAt)
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE previews SET status=$1, url=NULLIF($2,''), updated_at=NOW()
			WHERE repo_full_name=$3 AND pr_number=$4 AND commit_sha=$5`, status, publicURL, ref.Repo, ref.PR, ref.SHA)
		return err
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			log.Printf("complete Kubernetes operation %d: %v", job.id, err)
			d.releaseKubeOperation(job, 5)
		}
		return
	}
	if job.kind == "expire" {
		if notifier, ok := d.notifier.(hibernateNotifier); ok && ref.InstallationID != 0 && ref.CommentID != 0 {
			notifier.OnHibernated(ctx, ref, job.expiryDays)
		}
		return
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
