package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestEffectivePreviews_Defaults(t *testing.T) {
	cfg := EffectivePreviews(nil)
	if cfg.Trigger != TriggerAuto {
		t.Errorf("default trigger = %q, want auto", cfg.Trigger)
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
			Trigger:      "mention",
			SkipWhenOnly: &f,
			SkipPaths:    []string{"**/*.txt"},
		},
	}
	cfg := EffectivePreviews(spec)
	if cfg.Trigger != TriggerMention {
		t.Errorf("trigger = %q, want mention", cfg.Trigger)
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
