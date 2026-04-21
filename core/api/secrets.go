package main

// Secret store: per-repo env-var secrets, AES-256-GCM encrypted at rest.
//
// Key derivation:
//   - Primary: HATCH_SECRET_KEY (any string, SHA-256 derives a 32-byte key).
//   - Fallback: GITHUB_WEBHOOK_SECRET (dev convenience; logs a warning once).
// In production, HATCH_SECRET_KEY MUST be set; otherwise rotating the webhook
// secret silently invalidates every stored secret.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	secretKeyOnce sync.Once
	secretKeyVal  []byte

	errSecretCiphertextTooShort = errors.New("secrets: ciphertext too short")
)

// secretKey returns the 32-byte key derived from HATCH_SECRET_KEY, with a
// fallback to GITHUB_WEBHOOK_SECRET for local dev. Memoized.
func secretKey() []byte {
	secretKeyOnce.Do(func() {
		raw := os.Getenv("HATCH_SECRET_KEY")
		if raw == "" {
			fallback := os.Getenv("GITHUB_WEBHOOK_SECRET")
			if fallback == "" {
				log.Printf("WARNING: neither HATCH_SECRET_KEY nor GITHUB_WEBHOOK_SECRET set — secret store will use a zero key, DO NOT USE IN PROD")
				raw = "hatch-dev-fallback-do-not-use-in-prod"
			} else {
				log.Printf("WARNING: HATCH_SECRET_KEY missing, deriving secret key from GITHUB_WEBHOOK_SECRET (dev fallback)")
				raw = "hatch-fallback:" + fallback
			}
		}
		sum := sha256.Sum256([]byte(raw))
		secretKeyVal = sum[:]
	})
	return secretKeyVal
}

// EncryptSecret encrypts plaintext with AES-256-GCM. The 12-byte nonce is
// prepended to the returned ciphertext.
func EncryptSecret(key []byte, plaintext string) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secrets: nonce: %w", err)
	}
	out := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return append(nonce, out...), nil
}

// DecryptSecret reverses EncryptSecret.
func DecryptSecret(key []byte, ciphertext []byte) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("secrets: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("secrets: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("secrets: gcm: %w", err)
	}
	if len(ciphertext) < gcm.NonceSize() {
		return "", errSecretCiphertextTooShort
	}
	nonce := ciphertext[:gcm.NonceSize()]
	enc := ciphertext[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, enc, nil)
	if err != nil {
		return "", fmt.Errorf("secrets: open: %w", err)
	}
	return string(plain), nil
}

// loadRepoSecrets fetches every secret for a repo and decrypts them. Returns
// an empty map if the repo has no secrets.
func loadRepoSecrets(ctx context.Context, pool *pgxpool.Pool, repo string) (map[string]string, error) {
	if pool == nil {
		return map[string]string{}, nil
	}
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := pool.Query(qctx,
		`SELECT name, value_encrypted FROM repo_secrets WHERE repo_full_name = $1`,
		repo)
	if err != nil {
		return nil, fmt.Errorf("secrets: query: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	key := secretKey()
	for rows.Next() {
		var name string
		var enc []byte
		if err := rows.Scan(&name, &enc); err != nil {
			return nil, fmt.Errorf("secrets: scan: %w", err)
		}
		plain, err := DecryptSecret(key, enc)
		if err != nil {
			// One bad row shouldn't kill the whole deploy — log & skip.
			log.Printf("secrets: decrypt %s/%s failed: %v", repo, name, err)
			continue
		}
		out[name] = plain
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("secrets: rows: %w", err)
	}
	return out, nil
}
