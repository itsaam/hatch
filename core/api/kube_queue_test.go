package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestKubeQueuePostgreSQL(t *testing.T) {
	dsn := os.Getenv("HATCH_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("HATCH_TEST_DATABASE_URL is required for the PostgreSQL queue integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("hatch_queue_test_%d", time.Now().UnixNano())
	mustExec := func(pool *pgxpool.Pool, query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(admin, "CREATE SCHEMA "+schema)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, migration := range []string{migrationPreviews, migrationPreviewsComment, migrationKubeOperations, migrationKubeOperationOrder, migrationKubePRSnapshot, migrationKubeGitHubDeliveries, migrationKubeGitHubDeliveryTime} {
		mustExec(pool, migration)
	}
	d, err := NewDeployer(pool, "unused", "hatch.example.org", 1)
	if err != nil {
		t.Fatal(err)
	}
	ref := PreviewRef{Repo: "acme/app", PR: 123, Branch: "fix", SHA: strings.Repeat("a", 40)}
	mustExec(pool, `INSERT INTO previews (repo_full_name, pr_number, branch, commit_sha, status) VALUES ($1,$2,$3,$4,'pending')`, ref.Repo, ref.PR, ref.Branch, ref.SHA)
	if !d.enqueueKubeOperation(ref, "deploy") {
		t.Fatal("enqueue initial deploy")
	}
	old, err := d.claimKubeOperation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.claimKubeOperation(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("leased op was claimed again: %v", err)
	}
	if err := d.reconcileKube(ctx); err != nil {
		t.Fatal(err)
	}
	replayed, err := d.claimKubeOperation(ctx)
	if err != nil || replayed.id != old.id {
		t.Fatalf("restart claim = %+v, %v", replayed, err)
	}
	ref.SHA = strings.Repeat("b", 40)
	mustExec(pool, `UPDATE previews SET commit_sha=$1 WHERE repo_full_name=$2 AND pr_number=$3`, ref.SHA, ref.Repo, ref.PR)
	if !d.enqueueKubeOperation(ref, "deploy") {
		t.Fatal("enqueue new deploy")
	}
	d.completeKubeOperation(ctx, old, "failed", "")
	current, err := d.claimKubeOperation(ctx)
	if err != nil || current.id == old.id || current.ref.SHA != ref.SHA {
		t.Fatalf("current claim = %+v, %v", current, err)
	}
	d.releaseKubeOperation(current, 0)
	current, err = d.claimKubeOperation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://pr-123-acme-app.hatch.example.org"
	d.completeKubeOperation(ctx, current, "running", url)
	var state, publicURL string
	if err := pool.QueryRow(ctx, `SELECT status,url FROM previews WHERE repo_full_name=$1 AND pr_number=$2`, ref.Repo, ref.PR).Scan(&state, &publicURL); err != nil || state != "running" || publicURL != url {
		t.Fatalf("preview state = %q, %q, %v", state, publicURL, err)
	}
	open := fixturePull(ref, "open")
	closed := fixturePull(ref, "closed")
	reader := func(pr *pullState) pullStateReader {
		return func(context.Context, PreviewRef) (*pullState, error) { return pr, nil }
	}
	submit := func(event PreviewRef, intent kubePullIntent, canonical *pullState) bool {
		t.Helper()
		queued, err := d.queueKubePullRequest(ctx, event, intent, 0, reader(canonical))
		if err != nil && !errors.Is(err, errAwaitingGitHub) {
			t.Fatal(err)
		}
		return queued
	}
	mustClaim := func(kind string) kubeOperation {
		t.Helper()
		job, err := d.claimKubeOperation(ctx)
		if err != nil || job.kind != kind {
			t.Fatalf("expected %s operation, got %+v: %v", kind, job, err)
		}
		return job
	}

	submit(ref, kubePullClosed, closed)
	closing := mustClaim("close")
	if !submit(ref, kubePullChanged, open) {
		t.Fatal("reopen at the same head was not queued")
	}
	d.completeKubeOperation(ctx, closing, "closed", "")
	reopened := mustClaim("deploy")
	if reopened.id == closing.id || reopened.ref.SHA != ref.SHA {
		t.Fatalf("reopen replaced with wrong operation: %+v", reopened)
	}
	if submit(ref, kubePullChanged, open) {
		t.Fatal("redelivery requeued an active deploy")
	}
	upperCaseRef := ref
	upperCaseRef.Repo = strings.ToUpper(ref.Repo)
	if submit(upperCaseRef, kubePullChanged, open) {
		t.Fatal("repository case variation created another preview")
	}
	submit(ref, kubePullClosed, open)
	if err := d.publishCurrentKubeOperation(ctx, reopened, reader(open), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("duplicate or delayed close superseded the active deploy: %v", err)
	}
	if err := d.publishCurrentKubeOperation(ctx, reopened, reader(closed), func(context.Context) error {
		t.Fatal("a closed pull request reached ingress publication")
		return nil
	}); err == nil {
		t.Fatal("expected closed publication to fail")
	}
	publicationState := open
	if err := d.publishCurrentKubeOperation(ctx, reopened, func(context.Context, PreviewRef) (*pullState, error) {
		return publicationState, nil
	}, func(context.Context) error {
		publicationState = closed
		return nil
	}); err == nil {
		t.Fatal("publication did not notice GitHub closing before webhook delivery")
	}
	if err := d.publishCurrentKubeOperation(ctx, reopened, reader(open), func(context.Context) error {
		_, err := d.queueKubePullRequest(ctx, ref, kubePullClosed, 0, reader(closed))
		return err
	}); err == nil {
		t.Fatal("publication did not notice a concurrent close")
	}
	if submit(ref, kubePullRedeploy, closed) {
		t.Fatal("ChatOps requeued a closed PR")
	}
	mustClaim("close")
	staleRef := ref
	staleRef.SHA = strings.Repeat("c", 40)
	if !d.enqueueKubeOperation(staleRef, "close") {
		t.Fatal("stop command rejected a preview from an older commit")
	}

	// Keep A's fresh observation open while another handler receives B.
	// B must not observe GitHub until A's transaction has committed.
	newHead := ref
	newHead.SHA = strings.Repeat("d", 40)
	firstRead := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondRead := make(chan struct{})
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() {
		_, err := d.queueKubePullRequest(ctx, ref, kubePullChanged, 0, func(ctx context.Context, _ PreviewRef) (*pullState, error) {
			close(firstRead)
			select {
			case <-releaseFirst:
				return open, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		firstDone <- err
	}()
	select {
	case <-firstRead:
	case err := <-firstDone:
		t.Fatalf("first observation failed: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		_, err := d.queueKubePullRequest(ctx, newHead, kubePullChanged, 0, func(context.Context, PreviewRef) (*pullState, error) {
			close(secondRead)
			return fixturePull(newHead, "open"), nil
		})
		secondDone <- err
	}()
	select {
	case <-secondRead:
		close(releaseFirst)
		t.Fatal("concurrent observation bypassed the PR lock")
	case <-time.After(100 * time.Millisecond):
		close(releaseFirst)
	}
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if submit(ref, kubePullChanged, fixturePull(newHead, "open")) {
		t.Fatal("late delivery of the old head replaced the latest head")
	}
	if latest := mustClaim("deploy"); latest.ref.SHA != newHead.SHA {
		t.Fatalf("concurrent observations queued the wrong head: %+v", latest)
	}
	if err := d.storeKubeWebhook(ctx, newHead, kubePullChanged, time.Time{}); err != nil {
		t.Fatal(err)
	}
	configurationFailure := errors.New("temporary config read failure")
	if worked, err := d.processKubeWebhook(ctx, commandlessReader(t), func(context.Context, PreviewRef) (bool, error) {
		return false, configurationFailure
	}); !worked || !errors.Is(err, configurationFailure) {
		t.Fatalf("configuration failure was not retried: worked=%v, err=%v", worked, err)
	}
	mustExec(pool, `UPDATE kube_github_deliveries SET available_at=NOW()`)
	if worked, err := d.processKubeWebhook(ctx, commandlessReader(t), func(context.Context, PreviewRef) (bool, error) {
		return false, nil
	}); !worked || err != nil {
		t.Fatalf("manual configuration was not ignored: worked=%v, err=%v", worked, err)
	}
	if err := d.storeKubeWebhook(ctx, newHead, kubePullChanged, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if worked, err := d.processKubeWebhook(ctx, commandlessReader(t), func(context.Context, PreviewRef) (bool, error) {
		return false, errInvalidCompose
	}); !worked || !errors.Is(err, errInvalidCompose) {
		t.Fatalf("invalid config handling: worked=%v, err=%v", worked, err)
	}
	mustExec(pool, `UPDATE kube_github_deliveries SET available_at=NOW()`)
	if worked, err := d.processKubeWebhook(ctx, commandlessReader(t), commandlessPolicy(t)); worked || err != nil {
		t.Fatalf("invalid configuration was retained: worked=%v, err=%v", worked, err)
	}
	if err := d.storeKubeWebhook(ctx, newHead, kubePullClosed, time.Time{}); err != nil {
		t.Fatal(err)
	}
	autoAllowed := func(context.Context, PreviewRef) (bool, error) { return true, nil }
	apiFailure := errors.New("temporarily unavailable")
	if worked, err := d.processKubeWebhook(ctx, func(context.Context, PreviewRef) (*pullState, error) {
		return nil, apiFailure
	}, autoAllowed); !worked || !errors.Is(err, apiFailure) {
		t.Fatalf("failed observation: worked=%v, err=%v", worked, err)
	}
	makeDeliveryReady := func() {
		t.Helper()
		mustExec(pool, `UPDATE kube_github_deliveries SET available_at=NOW()`)
	}
	makeDeliveryReady()
	mustExec(pool, `UPDATE kube_github_deliveries SET mismatches=5`)
	if worked, err := d.processKubeWebhook(ctx, reader(fixturePull(newHead, "open")), autoAllowed); !worked || err != nil {
		t.Fatalf("inconclusive observation: worked=%v, err=%v", worked, err)
	}
	makeDeliveryReady()
	if worked, err := d.processKubeWebhook(ctx, func(ctx context.Context, _ PreviewRef) (*pullState, error) {
		return fixturePull(newHead, "closed"), d.storeKubeWebhook(ctx, newHead, kubePullClosed, time.Time{})
	}, autoAllowed); !worked || err != nil {
		t.Fatalf("closure retry: worked=%v, err=%v", worked, err)
	}
	mustClaim("close")
	if worked, err := d.processKubeWebhook(ctx, reader(fixturePull(newHead, "closed")), autoAllowed); !worked || err != nil {
		t.Fatalf("replacement signal was lost during processing: worked=%v, err=%v", worked, err)
	}
	closedAt := time.Now().UTC().Truncate(time.Second)
	if err := d.storeKubeWebhook(ctx, newHead, kubePullClosed, closedAt); err != nil {
		t.Fatal(err)
	}
	laterOpen := fixturePull(newHead, "open")
	laterOpen.UpdatedAt = closedAt.Add(time.Second)
	if worked, err := d.processKubeWebhook(ctx, reader(laterOpen), autoAllowed); !worked || err != nil {
		t.Fatalf("superseded close handling: worked=%v, err=%v", worked, err)
	}
	makeDeliveryReady()
	if worked, err := d.processKubeWebhook(ctx, commandlessReader(t), commandlessPolicy(t)); worked || err != nil {
		t.Fatalf("superseded close was retained: worked=%v, err=%v", worked, err)
	}

	commandReader := reader(fixturePull(newHead, "open"))
	if queued, err := d.queueKubePullRequest(ctx, newHead, kubePullRedeploy, 125, commandReader); !queued || err != nil {
		t.Fatalf("deploy command: queued=%v, err=%v", queued, err)
	}
	mustClaim("deploy")
	if queued, err := d.queueKubeStopCommand(ctx, newHead, 126); !queued || err != nil {
		t.Fatalf("stop command: queued=%v, err=%v", queued, err)
	}
	if queued, err := d.queueKubePullRequest(ctx, newHead, kubePullRedeploy, 125, commandReader); queued || err != nil {
		t.Fatalf("redelivery replaced stop: queued=%v, err=%v", queued, err)
	}
	mustClaim("close")
	if queued, err := d.queueKubePullRequest(ctx, newHead, kubePullRedeploy, 127, commandReader); !queued || err != nil {
		t.Fatalf("new deploy command: queued=%v, err=%v", queued, err)
	}
	if queued, err := d.queueKubeStopCommand(ctx, newHead, 126); queued || err != nil {
		t.Fatalf("redelivery replaced deploy: queued=%v, err=%v", queued, err)
	}
	mustClaim("deploy")

	mustExec(pool, `ALTER TABLE kube_operations ADD CHECK (operation <> 'close')`)
	if _, err := d.queueKubePullRequest(ctx, newHead, kubePullClosed, 0, reader(fixturePull(newHead, "closed"))); err == nil {
		t.Fatal("expected the queue write to fail")
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM previews WHERE repo_full_name=$1 AND pr_number=$2`, ref.Repo, ref.PR).Scan(&state); err != nil || state != "pending" {
		t.Fatalf("failed enqueue changed the preview state to %q, %v", state, err)
	}
}

func fixturePull(ref PreviewRef, state string) *pullState {
	pr := &pullState{State: state}
	pr.Head.SHA, pr.Head.Ref, pr.Head.Repo.FullName = ref.SHA, ref.Branch, ref.Repo
	return pr
}

func commandlessReader(t *testing.T) pullStateReader {
	return func(context.Context, PreviewRef) (*pullState, error) {
		t.Fatal("the manual preview policy reached the GitHub state observer")
		return nil, nil
	}
}

func commandlessPolicy(t *testing.T) func(context.Context, PreviewRef) (bool, error) {
	return func(context.Context, PreviewRef) (bool, error) {
		t.Fatal("a finished delivery was retried")
		return false, nil
	}
}
