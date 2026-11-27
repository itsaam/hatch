package main

import (
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPullRequestRequiresExplicitAutoConfiguration(t *testing.T) {
	const service = "version: 1\nservices:\n  web:\n    build: .\n"
	for _, test := range []struct {
		name, action, compose string
		status                int
		want                  triggerDecision
	}{
		{"open defaults to manual", "opened", service, http.StatusOK, decisionSkipMention},
		{"push remains manual", "synchronize", service, http.StatusOK, decisionSkipMention},
		{"explicit auto", "synchronize", service + "previews:\n  trigger: auto\n", http.StatusOK, decisionBuild},
		{"missing config", "opened", "", http.StatusNotFound, decisionSkipMention},
		{"unavailable config", "opened", "", http.StatusServiceUnavailable, decisionSkipMention},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := prEvent{Action: test.action, Number: 1}
			event.Repository.FullName = "acme/app"
			event.PullRequest.Head.SHA = strings.Repeat("a", 40)
			client := &http.Client{Transport: kubeTestTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.compose))}, nil
			})}
			if got := decidePullRequestTrigger(t.Context(), client, nil, event); got != test.want {
				t.Fatalf("trigger = %v, want %v", got, test.want)
			}
		})
	}
}

func TestEffectivePreviews_Defaults(t *testing.T) {
	cfg := EffectivePreviews(nil)
	if cfg.Trigger != TriggerMention {
		t.Errorf("default trigger = %q, want mention", cfg.Trigger)
	}
	if cfg.SkipWhenOnly == nil || !*cfg.SkipWhenOnly {
		t.Errorf("default skip_when_only must be true")
	}
	wantSubset := []string{"**/*.md", "**/*.pdf", "docs/**", ".github/**"}
	for _, p := range wantSubset {
		if !slices.Contains(cfg.SkipPaths, p) {
			t.Errorf("default skip paths missing %q", p)
		}
	}
}

func TestEffectivePreviews_Override(t *testing.T) {
	f := false
	spec := &ComposeSpec{
		Previews: &PreviewsConfig{
			Trigger:      "auto",
			SkipWhenOnly: &f,
			SkipPaths:    []string{"**/*.txt"},
		},
	}
	cfg := EffectivePreviews(spec)
	if cfg.Trigger != TriggerAuto {
		t.Errorf("trigger = %q, want auto", cfg.Trigger)
	}
	if cfg.SkipWhenOnly == nil || *cfg.SkipWhenOnly {
		t.Errorf("skip_when_only must be false")
	}
	if !reflect.DeepEqual(cfg.SkipPaths, []string{"**/*.txt"}) {
		t.Errorf("skip_paths = %v, want [**/*.txt]", cfg.SkipPaths)
	}
}

func TestValidateCompose_PreviewsTrigger(t *testing.T) {
	yamlOK := `version: 1
services:
  web:
    build: .
    expose: true
previews:
  trigger: mention`

	if _, err := ParseCompose([]byte(yamlOK)); err != nil {
		t.Fatalf("valid yaml rejected: %v", err)
	}

	yamlBad := `version: 1
services:
  web:
    build: .
    expose: true
previews:
  trigger: bogus`

	_, err := ParseCompose([]byte(yamlBad))
	if err == nil || !strings.Contains(err.Error(), "previews.trigger") {
		t.Fatalf("bad trigger should fail with previews.trigger error, got %v", err)
	}
}
