package main

import (
	"context"
	"log"
	"path"
	"strings"
)

// ensureBaseImagesForService fetches the service's Dockerfile from the repo,
// extracts every external FROM image, and pre-pulls each via the classic
// Docker /images/create endpoint (reusing d.pullImage).
//
// Why: Docker 29+ with the containerd snapshotter requires BuildKit
// (version=2) to negotiate a gRPC session before it can resolve remote image
// metadata during `docker build`. We drive the Docker API over raw HTTP and
// do not implement the session protocol, so we sidestep the issue by making
// sure every base image is already present in the local content store before
// the build starts.
//
// Best-effort: any error is logged and swallowed. A failed pre-pull means
// the build will surface the original "no active sessions" error, which is
// strictly no worse than the previous behaviour.
func (d *Deployer) ensureBaseImagesForService(ctx context.Context, repo, sha, buildContext, dockerfile string, installationID int64) {
	dfPath := resolveDockerfilePath(buildContext, dockerfile)
	data, err := fetchRepoFile(ctx, d.httpExt, d.app, installationID, repo, dfPath, sha)
	if err != nil {
		log.Printf("ensure base images: fetch %s@%s %s: %v", repo, shortSHA(sha), dfPath, err)
		return
	}
	if data == nil {
		log.Printf("ensure base images: %s@%s has no %s, skipping", repo, shortSHA(sha), dfPath)
		return
	}
	for _, img := range parseBaseImages(data) {
		if err := d.pullImage(ctx, img); err != nil {
			// A 404 here is expected when FROM references a named build
			// stage that the parser couldn't exclude — it's harmless.
			log.Printf("ensure base images: pull %s (non-fatal): %v", img, err)
			continue
		}
		log.Printf("ensure base images: pulled %s for %s@%s", img, repo, shortSHA(sha))
	}
}

// resolveDockerfilePath joins the build context with the Dockerfile name,
// normalising for leading "./" and empty values. GitHub's contents API
// rejects leading slashes, so the result never starts with one.
func resolveDockerfilePath(buildContext, dockerfile string) string {
	df := strings.TrimSpace(dockerfile)
	if df == "" {
		df = "Dockerfile"
	}
	ctxPath := strings.TrimSpace(buildContext)
	ctxPath = strings.TrimPrefix(ctxPath, "./")
	if ctxPath == "" || ctxPath == "." {
		return df
	}
	// If the Dockerfile path contains a slash, treat it as repo-rooted and
	// don't re-join with the context.
	if strings.Contains(df, "/") {
		return strings.TrimPrefix(path.Clean(df), "/")
	}
	return strings.TrimPrefix(path.Clean(ctxPath+"/"+df), "/")
}

// parseBaseImages scans a Dockerfile and returns each external FROM image in
// declaration order. Duplicates, $VARIABLE references, and references to
// locally-defined build stages (`FROM ... AS name`) are excluded.
func parseBaseImages(dockerfile []byte) []string {
	stages := map[string]bool{}
	seen := map[string]bool{}
	var out []string

	for _, raw := range strings.Split(string(dockerfile), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		upper := strings.ToUpper(line)
		if !strings.HasPrefix(upper, "FROM ") && !strings.HasPrefix(upper, "FROM\t") {
			continue
		}
		rest := strings.TrimSpace(line[4:])
		// Strip BuildKit flags like --platform=linux/amd64.
		for strings.HasPrefix(rest, "--") {
			sp := strings.IndexFunc(rest, func(r rune) bool { return r == ' ' || r == '\t' })
			if sp < 0 {
				rest = ""
				break
			}
			rest = strings.TrimSpace(rest[sp+1:])
		}
		parts := strings.Fields(rest)
		if len(parts) == 0 {
			continue
		}
		image := parts[0]
		if len(parts) >= 3 && strings.EqualFold(parts[1], "AS") {
			stages[parts[2]] = true
		}
		if image == "" || stages[image] || strings.HasPrefix(image, "$") {
			continue
		}
		if seen[image] {
			continue
		}
		seen[image] = true
		out = append(out, image)
	}
	return out
}
