package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	signatureHeader = "X-Hub-Signature-256"
	eventHeader     = "X-GitHub-Event"
	maxWebhookBody  = 5 << 20
)

type prEvent struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		Head struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func githubWebhookHandler(pool *pgxpool.Pool, secret []byte) http.HandlerFunc {
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

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		if err := handlePullRequest(ctx, pool, ev); err != nil {
			log.Printf("handle pull_request: %v", err)
			http.Error(w, "internal", http.StatusInternalServerError)
			return
		}
		writeOK(w)
	}
}

func handlePullRequest(ctx context.Context, pool *pgxpool.Pool, ev prEvent) error {
	repo := ev.Repository.FullName
	pr := ev.Number
	branch := ev.PullRequest.Head.Ref
	sha := ev.PullRequest.Head.SHA

	switch ev.Action {
	case "opened", "reopened", "synchronize":
		_, err := pool.Exec(ctx, `
			INSERT INTO previews (repo_full_name, pr_number, branch, commit_sha, status)
			VALUES ($1, $2, $3, $4, 'pending')
			ON CONFLICT (repo_full_name, pr_number) DO UPDATE
			SET branch = EXCLUDED.branch,
			    commit_sha = EXCLUDED.commit_sha,
			    status = 'pending',
			    updated_at = NOW()
		`, repo, pr, branch, sha)
		if err != nil {
			return err
		}
		log.Printf("preview upserted: %s#%d @ %s", repo, pr, shortSHA(sha))
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
