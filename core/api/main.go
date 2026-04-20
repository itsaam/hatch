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
	"strings"
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

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	if err := waitDB(ctx, pool); err != nil {
		log.Fatalf("db ping: %v", err)
	}
	for _, m := range []string{migrationSubscribers, migrationPreviews} {
		if _, err := pool.Exec(ctx, m); err != nil {
			log.Fatalf("migration: %v", err)
		}
	}

	deployer, err := NewDeployer(pool, hatchNetwork, hatchDomain)
	if err != nil {
		log.Fatalf("deployer init: %v", err)
	}

	r := chi.NewRouter()
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(10 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{allowedOrigin},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders: []string{"Content-Type"},
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

	r.Post("/api/github/webhook", githubWebhookHandler(pool, githubSecret, deployer))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("hatch api listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
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
