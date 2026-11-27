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
	for _, migration := range []string{migrationPreviews, migrationPreviewsComment, migrationKubeOperations, migrationKubeOperationOrder} {
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
	eventAt := time.Now().UTC().Truncate(time.Second)
	if err := d.queueKubePullRequest(ctx, ref, false, eventAt.Add(-2*time.Second), false); err != nil {
		t.Fatal(err)
	}
	closing, err := d.claimKubeOperation(ctx)
	if err != nil || closing.kind != "close" {
		t.Fatalf("close claim = %+v, %v", closing, err)
	}
	if err := d.queueKubePullRequest(ctx, ref, true, eventAt.Add(-time.Second), true); err != nil {
		t.Fatal(err)
	}
	d.completeKubeOperation(ctx, closing, "closed", "")
	reopened, err := d.claimKubeOperation(ctx)
	if err != nil || reopened.kind != "deploy" || reopened.id == closing.id {
		t.Fatalf("reopen with same SHA = %+v, %v", reopened, err)
	}
	if err := d.publishCurrentKubeOperation(ctx, closing, func(context.Context) error {
		t.Fatal("a superseded operation reached ingress publication")
		return nil
	}); err == nil {
		t.Fatal("expected stale publication to fail")
	}
	if err := d.publishCurrentKubeOperation(ctx, reopened, func(context.Context) error { return nil }); err != nil {
		t.Fatalf("current publication was rejected: %v", err)
	}
	if err := d.publishCurrentKubeOperation(ctx, reopened, func(ctx context.Context) error {
		return d.queueKubePullRequest(ctx, ref, false, eventAt, false)
	}); err == nil {
		t.Fatal("publication did not notice a concurrent close")
	}
	for _, stale := range []time.Time{eventAt.Add(-time.Second), eventAt} {
		if err := d.queueKubePullRequest(ctx, ref, true, stale, true); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := d.claimKubeOperation(ctx)
	if err != nil || latest.kind != "close" {
		t.Fatalf("stale delivery overrode closure: %+v, %v", latest, err)
	}
	staleRef := ref
	staleRef.SHA = strings.Repeat("c", 40)
	if !d.enqueueKubeOperation(staleRef, "close") {
		t.Fatal("stop command rejected a preview from an older commit")
	}
	if err := d.queueKubePullRequest(ctx, ref, true, eventAt.Add(time.Second), true); err != nil {
		t.Fatal(err)
	}
	newHead := ref
	newHead.SHA = strings.Repeat("d", 40)
	if err := d.queueKubePullRequest(ctx, newHead, true, eventAt.Add(time.Second), true); err != nil {
		t.Fatal(err)
	}
	latest, err = d.claimKubeOperation(ctx)
	if err != nil || latest.kind != "deploy" || latest.ref.SHA != newHead.SHA {
		t.Fatalf("same-second verified head was skipped: %+v, %v", latest, err)
	}
	mustExec(pool, `ALTER TABLE kube_operations ADD CHECK (operation <> 'close')`)
	if err := d.queueKubePullRequest(ctx, newHead, false, eventAt.Add(2*time.Second), false); err == nil {
		t.Fatal("expected the queue write to fail")
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM previews WHERE repo_full_name=$1 AND pr_number=$2`, ref.Repo, ref.PR).Scan(&state); err != nil || state != "pending" {
		t.Fatalf("failed enqueue changed the preview state to %q, %v", state, err)
	}
}
