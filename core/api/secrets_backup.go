package main

// Secret store backup / restore.
//
// Both handlers are registered under /api/secrets behind requireAdminToken +
// httprate 30/min — same gate as the rest of the secrets CRUD, no extra
// surface. The export dumps every row's raw AES-GCM ciphertext (base64) so
// the plaintext never leaves the server. That means:
//
//   * Losing the DB but keeping HATCH_SECRET_KEY → restore from dump, done.
//   * Losing HATCH_SECRET_KEY → dump is unrecoverable. Store the key in a
//     password manager alongside the dump.
//
// Every call is logged with client IP and row count for forensic traceability.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const secretsBackupVersion = 1

type secretBackupItem struct {
	Repo             string `json:"repo"`
	Name             string `json:"name"`
	ValueEncryptedB64 string `json:"value_encrypted_b64"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type secretBackupDump struct {
	Version    int                `json:"version"`
	ExportedAt string             `json:"exported_at"`
	Count      int                `json:"count"`
	Secrets    []secretBackupItem `json:"secrets"`
}

func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		if i := strings.Index(f, ","); i > 0 {
			return strings.TrimSpace(f[:i])
		}
		return strings.TrimSpace(f)
	}
	return r.RemoteAddr
}

// exportSecretsHandler dumps every secret as base64-encoded ciphertext.
// Response is application/json with Content-Disposition so browsers save
// straight to a file.
func exportSecretsHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		rows, err := pool.Query(ctx, `
			SELECT repo_full_name, name, value_encrypted, created_at, updated_at
			FROM repo_secrets
			ORDER BY repo_full_name, name
		`)
		if err != nil {
			log.Printf("secrets export query: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		defer rows.Close()

		out := secretBackupDump{
			Version:    secretsBackupVersion,
			ExportedAt: time.Now().UTC().Format(time.RFC3339),
			Secrets:    []secretBackupItem{},
		}
		for rows.Next() {
			var item secretBackupItem
			var enc []byte
			var created, updated time.Time
			if err := rows.Scan(&item.Repo, &item.Name, &enc, &created, &updated); err != nil {
				log.Printf("secrets export scan: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
				return
			}
			item.ValueEncryptedB64 = base64.StdEncoding.EncodeToString(enc)
			item.CreatedAt = created.UTC().Format(time.RFC3339)
			item.UpdatedAt = updated.UTC().Format(time.RFC3339)
			out.Secrets = append(out.Secrets, item)
		}
		if err := rows.Err(); err != nil {
			log.Printf("secrets export rows: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		out.Count = len(out.Secrets)

		log.Printf("secrets export: %d rows requested by %s", out.Count, clientIP(r))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="hatch-secrets-backup.json"`)
		_ = json.NewEncoder(w).Encode(out)
	}
}

// importSecretsHandler restores a dump produced by exportSecretsHandler.
// Default mode skips rows whose (repo, name) already exists. Pass
// ?overwrite=true to replace existing ciphertext.
func importSecretsHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		overwrite := strings.EqualFold(r.URL.Query().Get("overwrite"), "true")

		const maxDumpBytes = 16 << 20 // 16 MiB
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDumpBytes))
		dec.DisallowUnknownFields()

		var dump secretBackupDump
		if err := dec.Decode(&dump); err != nil {
			http.Error(w, "invalid dump: "+err.Error(), http.StatusBadRequest)
			return
		}
		if dump.Version != secretsBackupVersion {
			http.Error(w, "unsupported dump version", http.StatusBadRequest)
			return
		}
		if len(dump.Secrets) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "imported": 0, "skipped": 0})
			return
		}

		// Pre-flight: validate every row's shape and that ciphertext decodes
		// cleanly before we touch the DB. Fail fast, no partial imports.
		decoded := make([][]byte, len(dump.Secrets))
		for i, s := range dump.Secrets {
			if !repoNameRE.MatchString(s.Repo) {
				http.Error(w, "invalid repo in dump: "+s.Repo, http.StatusBadRequest)
				return
			}
			if !secretNameRE.MatchString(s.Name) {
				http.Error(w, "invalid name in dump: "+s.Name, http.StatusBadRequest)
				return
			}
			b, err := base64.StdEncoding.DecodeString(s.ValueEncryptedB64)
			if err != nil || len(b) < 13 {
				http.Error(w, "invalid ciphertext for "+s.Repo+"/"+s.Name, http.StatusBadRequest)
				return
			}
			decoded[i] = b
		}

		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		tx, err := pool.Begin(ctx)
		if err != nil {
			log.Printf("secrets import begin: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}
		defer tx.Rollback(ctx)

		imported, skipped := 0, 0
		for i, s := range dump.Secrets {
			var q string
			if overwrite {
				q = `INSERT INTO repo_secrets (repo_full_name, name, value_encrypted)
				     VALUES ($1, $2, $3)
				     ON CONFLICT (repo_full_name, name)
				     DO UPDATE SET value_encrypted = EXCLUDED.value_encrypted, updated_at = NOW()`
			} else {
				q = `INSERT INTO repo_secrets (repo_full_name, name, value_encrypted)
				     VALUES ($1, $2, $3)
				     ON CONFLICT (repo_full_name, name) DO NOTHING`
			}
			tag, err := tx.Exec(ctx, q, s.Repo, s.Name, decoded[i])
			if err != nil {
				log.Printf("secrets import insert %s/%s: %v", s.Repo, s.Name, err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
				return
			}
			if tag.RowsAffected() > 0 {
				imported++
			} else {
				skipped++
			}
		}
		if err := tx.Commit(ctx); err != nil {
			log.Printf("secrets import commit: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
			return
		}

		log.Printf("secrets import: %d imported, %d skipped (overwrite=%v) from %s",
			imported, skipped, overwrite, clientIP(r))
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"imported":  imported,
			"skipped":   skipped,
			"overwrite": overwrite,
		})
	}
}

