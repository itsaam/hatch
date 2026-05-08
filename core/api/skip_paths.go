package main

import (
	"path"
	"strings"
)

// defaultSkipPaths is the built-in glob list used when `.hatch.yml` does not
// override `previews.skip_paths`. It targets files that cannot influence the
// container build (documentation, repo metadata, IDE configs).
//
// Globs support `**` for "any depth" plus the usual `?` / `[...]` from
// path.Match. Patterns match the full path from the repo root, case-insensitive.
var defaultSkipPaths = []string{
	"**/*.md",
	"**/*.mdx",
	"**/*.txt",
	"**/*.pdf",
	"**/*.rst",
	"LICENSE*",
	"CHANGELOG*",
	"AUTHORS*",
	"CONTRIBUTORS*",
	"CODEOWNERS",
	"docs/**",
	".github/**",
	".gitignore",
	".gitattributes",
	".editorconfig",
}

// matchSkipGlob reports whether p matches pattern. Both inputs are normalized
// to forward slashes and lowercased before matching. The `**` token matches
// zero or more path segments.
func matchSkipGlob(pattern, p string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	p = strings.ToLower(strings.TrimSpace(p))
	if pattern == "" || p == "" {
		return false
	}
	pattern = strings.ReplaceAll(pattern, "\\", "/")
	p = strings.ReplaceAll(p, "\\", "/")
	return matchSegments(splitGlob(pattern), strings.Split(p, "/"))
}

// splitGlob splits a pattern on "/", but keeps "**" as its own token so the
// matcher can recognise it. Empty leading/trailing segments are dropped.
func splitGlob(pattern string) []string {
	parts := strings.Split(pattern, "/")
	out := parts[:0]
	for _, seg := range parts {
		if seg == "" {
			continue
		}
		out = append(out, seg)
	}
	return out
}

// matchSegments matches glob segments against path segments. `**` in the
// pattern matches zero or more path segments. Plain segments match via
// path.Match (so `*.md`, `*-test.go` etc. work).
func matchSegments(pat, seg []string) bool {
	pi, si := 0, 0
	starPi, starSi := -1, -1
	for si < len(seg) {
		if pi < len(pat) {
			if pat[pi] == "**" {
				starPi = pi
				starSi = si
				pi++
				continue
			}
			ok, _ := path.Match(pat[pi], seg[si])
			if ok {
				pi++
				si++
				continue
			}
		}
		if starPi >= 0 {
			pi = starPi + 1
			starSi++
			si = starSi
			continue
		}
		return false
	}
	for pi < len(pat) && pat[pi] == "**" {
		pi++
	}
	return pi == len(pat)
}

// allFilesMatchSkip reports whether *every* file in the slice matches at
// least one of the skip patterns. An empty file list returns false (we never
// skip when GitHub claims zero files changed — likely a stale or odd payload).
func allFilesMatchSkip(files, patterns []string) bool {
	if len(files) == 0 || len(patterns) == 0 {
		return false
	}
	for _, f := range files {
		if !anyGlobMatch(patterns, f) {
			return false
		}
	}
	return true
}

func anyGlobMatch(patterns []string, p string) bool {
	for _, pat := range patterns {
		if matchSkipGlob(pat, p) {
			return true
		}
	}
	return false
}
