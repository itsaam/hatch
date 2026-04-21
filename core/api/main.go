package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/mail"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/001_subscribers.sql
var migrationSubscribers string

//go:embed migrations/002_previews.sql
var migrationPreviews string

//go:embed migrations/003_previews_comment.sql
var migrationPreviewsComment string

//go:embed migrations/004_preview_expired_status.sql
var migrationPreviewExpired string

//go:embed migrations/005_repo_secrets.sql
var migrationRepoSecrets string

//go:embed migrations/006_build_logs.sql
var migrationBuildLogs string

const maxEmailLen = 254

type subscribeReq struct {
	Email string `json:"email"`
}

func main() {
	dsn := mustEnv("DATABASE_URL")
	allowedOrigin := mustEnv("ALLOWED_ORIGIN")
	githubSecret := []byte(mustEnv("GITHUB_WEBHOOK_SECRET"))
	hatchNetwork := getenv("HATCH_NETWORK", "hatch_public")
	hatchDomain := getenv("HATCH_DOMAIN", "localhost")
	port := getenv("PORT", "8080")
	ttlHours := getenvInt("PREVIEW_TTL_HOURS", 168)
	reaperMinutes := getenvInt("PREVIEW_REAPER_INTERVAL_MINUTES", 60)

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	if err := waitDB(ctx, pool); err != nil {
		log.Fatalf("db ping: %v", err)
	}
	for _, m := range []string{migrationSubscribers, migrationPreviews, migrationPreviewsComment, migrationPreviewExpired, migrationRepoSecrets, migrationBuildLogs} {
		if _, err := pool.Exec(ctx, m); err != nil {
			log.Fatalf("migration: %v", err)
		}
	}

	deployer, err := NewDeployer(pool, hatchNetwork, hatchDomain)
	if err != nil {
		log.Fatalf("deployer init: %v", err)
	}

	var appClient *AppClient
	if strings.EqualFold(getenv("GITHUB_APP_ENABLED", "false"), "true") {
		appIDStr := mustEnv("GITHUB_APP_ID")
		appID, err := strconv.ParseInt(appIDStr, 10, 64)
		if err != nil {
			log.Fatalf("invalid GITHUB_APP_ID: %v", err)
		}
		pemPath := getenv("GITHUB_APP_PRIVATE_KEY_PATH", "/app/secrets/github-app.pem")
		appClient, err = NewAppClient(appID, pemPath)
		if err != nil {
			log.Fatalf("github app init: %v", err)
		}
		log.Printf("github app integration enabled (app_id=%d)", appID)
	} else {
		log.Printf("github app integration disabled")
	}

	deployer.SetNotifier(&prNotifier{pool: pool, app: appClient})
	deployer.SetAppClient(appClient)

	reconcileCtx, reconcileCancel := context.WithTimeout(rootCtx, 30*time.Second)
	if err := deployer.Reconcile(reconcileCtx, pool); err != nil {
		log.Printf("reconcile failed (non-fatal): %v", err)
	}
	reconcileCancel()

	deployer.StartTTLReaper(rootCtx, pool,
		time.Duration(ttlHours)*time.Hour,
		time.Duration(reaperMinutes)*time.Minute)

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(10 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{allowedOrigin},
		AllowedMethods: []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type", "Authorization"},
		MaxAge:         300,
	}))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r.Group(func(r chi.Router) {
		r.Use(httprate.LimitByIP(5, time.Minute))
		r.Post("/api/subscribe", subscribeHandler(pool))
	})

	r.Group(func(r chi.Router) {
		r.Use(httprate.LimitByIP(60, time.Minute))
		r.Get("/api/subscribers/count", countHandler(pool))
	})

	r.Post("/api/github/webhook", githubWebhookHandler(pool, githubSecret, deployer, appClient))

	// Secret store — gated by HATCH_ADMIN_TOKEN (bearer). See secrets_handlers.go.
	r.Route("/api/secrets", func(r chi.Router) {
		r.Use(requireAdminToken)
		r.Use(httprate.LimitByIP(30, time.Minute))
		r.Post("/", upsertSecretHandler(pool))
		r.Get("/", listSecretsHandler(pool))
		r.Delete("/", deleteSecretHandler(pool))
	})

	// Preview admin endpoints — gated by HATCH_ADMIN_TOKEN. See previews_handlers.go.
	r.Route("/api/previews", func(r chi.Router) {
		r.Use(requireAdminToken)
		r.Use(httprate.LimitByIP(30, time.Minute))
		r.Get("/", listPreviewsHandler(pool))
		r.Get("/{owner}/{repo}/{pr}/logs", previewLogsHandler(pool, deployer))
		r.Post("/{owner}/{repo}/{pr}/redeploy", previewRedeployHandler(pool, deployer))
		r.Delete("/{owner}/{repo}/{pr}", previewDestroyHandler(pool, deployer))
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Printf("hatch api listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case <-rootCtx.Done():
		log.Printf("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			log.Fatalf("server error: %v", err)
		}
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown: %v", err)
	}
	log.Printf("hatch api stopped")
}

func getenvInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Printf("invalid %s=%q, using default %d", k, v, def)
		return def
	}
	return n
}

func subscribeHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body subscribeReq
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			writeOK(w)
			return
		}

		email := strings.ToLower(strings.TrimSpace(body.Email))
		if !validEmail(email) {
			writeOK(w)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		_, err := pool.Exec(ctx,
			`INSERT INTO subscribers (email) VALUES ($1) ON CONFLICT (email) DO NOTHING`,
			email,
		)
		if err != nil {
			log.Printf("insert failed: %v", err)
		}
		writeOK(w)
	}
}

func countHandler(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		var n int64
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM subscribers`).Scan(&n); err != nil {
			log.Printf("count failed: %v", err)
			n = 0
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=30")
		_ = json.NewEncoder(w).Encode(map[string]int64{"count": n})
	}
}

func validEmail(s string) bool {
	if s == "" || len(s) > maxEmailLen {
		return false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil {
		return false
	}
	return addr.Address == s && strings.Contains(s, ".")
}

func writeOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func waitDB(ctx context.Context, pool *pgxpool.Pool) error {
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := pool.Ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(time.Second)
	}
	if lastErr == nil {
		lastErr = errors.New("timeout")
	}
	return lastErr
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf("missing env: %s", k)
	}
	return v
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
