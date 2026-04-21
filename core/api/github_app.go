package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	githubAPIBaseDefault = "https://api.github.com"
	githubUserAgent      = "hatch-preview/0.1"
	githubAcceptHeader   = "application/vnd.github+json"
	githubAPIVersion     = "2022-11-28"

	appJWTTTL           = 9 * time.Minute
	tokenSafetyMargin   = 60 * time.Second
	defaultHTTPTimeout  = 15 * time.Second
	maxGitHubResponseSz = 1 << 20
)

// AppClient is a minimal GitHub App client with installation token caching.
type AppClient struct {
	appID      int64
	privateKey *rsa.PrivateKey
	httpClient *http.Client
	baseURL    string

	mu     sync.Mutex
	tokens map[int64]cachedToken
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

// NewAppClient loads the RSA PEM private key and returns a ready client.
func NewAppClient(appID int64, pemPath string) (*AppClient, error) {
	if appID <= 0 {
		return nil, errors.New("github app: invalid app id")
	}
	raw, err := os.ReadFile(pemPath)
	if err != nil {
		return nil, fmt.Errorf("github app: read pem: %w", err)
	}
	key, err := parseRSAPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("github app: parse pem: %w", err)
	}
	return &AppClient{
		appID:      appID,
		privateKey: key,
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
		baseURL:    githubAPIBaseDefault,
		tokens:     make(map[int64]cachedToken),
	}, nil
}

func parseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}
	rsaKey, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("key is not RSA")
	}
	return rsaKey, nil
}

// appJWT mints a short-lived RS256 JWT signed with the App private key.
func (c *AppClient) appJWT() (string, error) {
	now := time.Now()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(appJWTTTL).Unix(),
		"iss": c.appID,
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("jwt header: %w", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("jwt claims: %w", err)
	}
	signingInput := b64url(headerJSON) + "." + b64url(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("jwt sign: %w", err)
	}
	return signingInput + "." + b64url(sig), nil
}

func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// installationToken returns a cached or freshly issued installation access token.
func (c *AppClient) installationToken(ctx context.Context, installationID int64) (string, error) {
	c.mu.Lock()
	if t, ok := c.tokens[installationID]; ok && time.Now().Before(t.expiresAt) {
		c.mu.Unlock()
		return t.token, nil
	}
	c.mu.Unlock()

	jwtTok, err := c.appJWT()
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.baseURL, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", fmt.Errorf("installation token req: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtTok)
	c.setCommonHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("installation token do: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxGitHubResponseSz))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("installation token http %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("installation token decode: %w", err)
	}
	if out.Token == "" {
		return "", errors.New("installation token: empty token")
	}
	exp := out.ExpiresAt.Add(-tokenSafetyMargin)
	if out.ExpiresAt.IsZero() {
		exp = time.Now().Add(50 * time.Minute)
	}

	c.mu.Lock()
	c.tokens[installationID] = cachedToken{token: out.Token, expiresAt: exp}
	c.mu.Unlock()

	return out.Token, nil
}

func (c *AppClient) setCommonHeaders(req *http.Request) {
	req.Header.Set("Accept", githubAcceptHeader)
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	req.Header.Set("User-Agent", githubUserAgent)
}

type commentBody struct {
	Body string `json:"body"`
}

type commentResp struct {
	ID int64 `json:"id"`
}

// CommentPR posts a new issue comment on the given PR.
func (c *AppClient) CommentPR(ctx context.Context, installationID int64, owner, repo string, prNumber int, body string) (int64, error) {
	tok, err := c.installationToken(ctx, installationID)
	if err != nil {
		return 0, err
	}
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", c.baseURL, owner, repo, prNumber)
	payload, err := json.Marshal(commentBody{Body: body})
	if err != nil {
		return 0, fmt.Errorf("comment encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, fmt.Errorf("comment req: %w", err)
	}
	req.Header.Set("Authorization", "token "+tok)
	req.Header.Set("Content-Type", "application/json")
	c.setCommonHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("comment do: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxGitHubResponseSz))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("comment http %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}
	var out commentResp
	if err := json.Unmarshal(respBody, &out); err != nil {
		return 0, fmt.Errorf("comment decode: %w", err)
	}
	if out.ID == 0 {
		return 0, errors.New("comment: empty id")
	}
	return out.ID, nil
}

// UpdateComment patches an existing issue comment body.
func (c *AppClient) UpdateComment(ctx context.Context, installationID int64, owner, repo string, commentID int64, body string) error {
	tok, err := c.installationToken(ctx, installationID)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/repos/%s/%s/issues/comments/%d", c.baseURL, owner, repo, commentID)
	payload, err := json.Marshal(commentBody{Body: body})
	if err != nil {
		return fmt.Errorf("update comment encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("update comment req: %w", err)
	}
	req.Header.Set("Authorization", "token "+tok)
	req.Header.Set("Content-Type", "application/json")
	c.setCommonHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("update comment do: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxGitHubResponseSz))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("update comment http %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}
	return nil
}

// splitRepo splits "owner/name" into its two parts.
func splitRepo(full string) (owner, repo string, ok bool) {
	idx := strings.IndexByte(full, '/')
	if idx <= 0 || idx == len(full)-1 {
		return "", "", false
	}
	return full[:idx], full[idx+1:], true
}
