package main

// Secret store CRUD endpoints.
//
// Auth: every handler in this file is gated by requireAdminToken, which
// compares the Authorization: Bearer <token> header to HATCH_ADMIN_TOKEN using
// a constant-time comparison. Absent/mismatched token → 401.
//
// If HATCH_ADMIN_TOKEN is empty the middleware denies every request (fail
// closed) so that forgetting to set the env var in prod cannot silently expose
// the secret store.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// secretNameRE validates env-var names: uppercase/digits/underscore, must
// start with a letter, 1..128 chars. Same shape as POSIX env names.
var secretNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// repoNameRE validates "owner/repo" shape.
var repoNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

const maxSecretValueBytes = 64 * 1024 // 64 KiB

type secretUpsertReq struct {
	Repo  string `json:"repo"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type secretListItem struct {
	Name      string `json:"name"`
	UpdatedAt string `json:"updated_at"`
}

// requireAdminToken is a chi-compatible middleware enforcing bearer auth via
// HATCH_ADMIN_TOKEN. Fail-closed: empty env var rejects everything.
func requireAdminToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := os.Getenv("HATCH_ADMIN_TOKEN")
		if expected == "" {
			http.Error(w, "admin token not configured", http.StatusUnauthorized)
			return
		}
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(h, prefix) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		got := strings.TrimSpace(h[len(prefix):])
		if subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// upsertSecretHandler stores (or updates) one secret for a repo.
func upsertSecretHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body secretUpsertReq
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(maxSecretValueBytes+1024)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		repo := strings.TrimSpace(body.Repo)
		name := strings.TrimSpace(body.Name)
		if !repoNameRE.MatchString(repo) {
			http.Error(w, "invalid repo", http.StatusBadRequest)
			return
		}
		if !secretNameRE.MatchString(name) {
			http.Error(w, "invalid name (uppercase A-Z0-9_, start with letter or _)", http.StatusBadRequest)
			return
		}
		if len(body.Value) == 0 || len(body.Value) > maxSecretValueBytes {
			http.Error(w, "invalid value length", http.StatusBadRequest)
			return
		}

		enc, err := EncryptSecret(secretKey(), body.Value)
		if err != nil {
			log.Printf("encrypt %s/%s: %v", repo, name, err)
			http.Error(w, "encryption failed", http.StatusInternalServerError)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		_, err = pool.Exec(ctx, `
			INSERT INTO repo_secrets (repo_full_name, name, value_encrypted)
			VALUES ($1, $2, $3)
			ON CONFLICT (repo_full_name, name)
			DO UPDATE SET value_encrypted = EXCLUDED.value_encrypted, updated_at = NOW()
		`, repo, name, enc)
		if err != nil {
			log.Printf("upsert secret %s/%s: %v", repo, name, err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "repo": repo, "name": name})
	}
}

// listSecretsHandler returns names only, never values.
func listSecretsHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo := strings.TrimSpace(r.URL.Query().Get("repo"))
		if !repoNameRE.MatchString(repo) {
			http.Error(w, "invalid repo", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		rows, err := pool.Query(ctx,
			`SELECT name, updated_at FROM repo_secrets WHERE repo_full_name = $1 ORDER BY name ASC`,
			repo)
		if err != nil {
			log.Printf("list secrets %s: %v", repo, err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		out := make([]secretListItem, 0)
		for rows.Next() {
			var item secretListItem
			var ts time.Time
			if err := rows.Scan(&item.Name, &ts); err != nil {
				log.Printf("scan secrets %s: %v", repo, err)
				http.Error(w, "db error", http.StatusInternalServerError)
				return
			}
			item.UpdatedAt = ts.UTC().Format(time.RFC3339)
			out = append(out, item)
		}
		if err := rows.Err(); err != nil {
			log.Printf("rows err secrets %s: %v", repo, err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"repo": repo, "secrets": out})
	}
}

// deleteSecretHandler removes one secret. Missing row → 404.
func deleteSecretHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo := strings.TrimSpace(r.URL.Query().Get("repo"))
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if !repoNameRE.MatchString(repo) {
			http.Error(w, "invalid repo", http.StatusBadRequest)
			return
		}
		if !secretNameRE.MatchString(name) {
			http.Error(w, "invalid name", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		tag, err := pool.Exec(ctx,
			`DELETE FROM repo_secrets WHERE repo_full_name = $1 AND name = $2`,
			repo, name)
		if err != nil {
			log.Printf("delete secret %s/%s: %v", repo, name, err)
			http.Error(w, "db error", http.StatusInternalServerError)
			return
		}
		if tag.RowsAffected() == 0 {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// sentinel kept for future use (extends errors.Is matching in handlers)
var _ = errors.New
