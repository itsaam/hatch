package main

import (
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type errorMonitor struct {
	client *sentry.Client

	mu   sync.Mutex
	last map[monitorKey]time.Time
}

type monitorKey struct {
	kind  string
	route string
}

func newErrorMonitor(dsn, environment, release string) (*errorMonitor, error) {
	if dsn == "" {
		return nil, nil
	}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:            dsn,
		Environment:    environment,
		Release:        release,
		SendDefaultPII: false,
		Integrations:   func([]sentry.Integration) []sentry.Integration { return nil },
		BeforeSend: func(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			event.Request = nil
			event.User = sentry.User{}
			event.ServerName = ""
			event.Contexts = nil
			event.Breadcrumbs = nil
			return event
		},
	})
	if err != nil {
		return nil, err
	}
	return &errorMonitor{client: client, last: make(map[monitorKey]time.Time)}, nil
}

func (m *errorMonitor) flush() {
	if m != nil {
		m.client.Flush(2 * time.Second)
	}
}

func (m *errorMonitor) report(kind string, tags map[string]string, trace *sentry.Stacktrace) {
	if m == nil {
		return
	}
	// Repeated failures from a retry loop should not overwhelm GlitchTip.
	now := time.Now()
	key := monitorKey{kind: kind, route: tags["route"]}
	m.mu.Lock()
	last := m.last[key]
	if now.Sub(last) < 30*time.Second {
		m.mu.Unlock()
		return
	}
	m.last[key] = now
	m.mu.Unlock()

	event := sentry.NewEvent()
	event.Level = sentry.LevelError
	event.Message = kind
	event.Tags = tags
	event.Fingerprint = []string{kind}
	if key.route != "" {
		event.Fingerprint = append(event.Fingerprint, key.route)
	}
	if trace != nil {
		event.Exception = []sentry.Exception{{Type: kind, Stacktrace: trace}}
	}
	m.client.CaptureEvent(event, nil, nil)
}

func (m *errorMonitor) httpErrors(next http.Handler) http.Handler {
	if m == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		route := func() string {
			if routes := chi.RouteContext(r.Context()); routes != nil {
				return routes.RoutePattern()
			}
			return ""
		}
		defer func() {
			if value := recover(); value != nil {
				if value != http.ErrAbortHandler {
					m.report("http_panic", map[string]string{"route": route()}, sentry.NewStacktrace())
				}
				panic(value)
			}
		}()
		next.ServeHTTP(wrapped, r)
		status := wrapped.Status()
		pattern := route()
		if status >= 500 && pattern != "/healthz" && pattern != "/readyz" {
			m.report("http_server_error", map[string]string{"route": pattern, "status": strconv.Itoa(status)}, nil)
		}
	})
}

// maxRequestBody preserves net/http's connection handling for oversized
// requests when the response writer is wrapped by middleware.
func maxRequestBody(w http.ResponseWriter, body io.ReadCloser, limit int64) io.ReadCloser {
	for {
		wrapped, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return http.MaxBytesReader(w, body, limit)
		}
		w = wrapped.Unwrap()
	}
}
