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
	for _, migration := range []string{migrationPreviews, migrationPreviewsComment, migrationKubeOperations, migrationKubeOperationOrder, migrationKubePRSnapshot} {
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
		queued, err := d.queueKubePullRequest(ctx, event, intent, reader(canonical))
		if err != nil {
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
		_, err := d.queueKubePullRequest(ctx, ref, kubePullClosed, reader(closed))
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
		_, err := d.queueKubePullRequest(ctx, ref, kubePullChanged, func(ctx context.Context, _ PreviewRef) (*pullState, error) {
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
		_, err := d.queueKubePullRequest(ctx, newHead, kubePullChanged, func(context.Context, PreviewRef) (*pullState, error) {
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

	mustExec(pool, `ALTER TABLE kube_operations ADD CHECK (operation <> 'close')`)
	if _, err := d.queueKubePullRequest(ctx, newHead, kubePullClosed, reader(fixturePull(newHead, "closed"))); err == nil {
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
