package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxBuildArchive = 256 << 20

var (
	buildCommitPattern    = regexp.MustCompile(`^[a-f0-9]{40}$`)
	buildDigestPattern    = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	registryPrefixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*(:[0-9]+)?(/[a-z0-9]+([._-][a-z0-9]+)*)+$`)
)

type kubeBuilder struct {
	endpoint, serverName, ca, cert, key string
	registry, registryConfig            string
}

func newKubeBuilder() (*kubeBuilder, error) {
	b := &kubeBuilder{
		endpoint: os.Getenv("HATCH_BUILDKIT_ADDR"), serverName: os.Getenv("HATCH_BUILDKIT_SERVER_NAME"),
		ca: os.Getenv("HATCH_BUILDKIT_CA"), cert: os.Getenv("HATCH_BUILDKIT_CERT"), key: os.Getenv("HATCH_BUILDKIT_KEY"),
		registry: os.Getenv("HATCH_REGISTRY_PREFIX"), registryConfig: os.Getenv("HATCH_REGISTRY_CONFIG"),
	}
	endpoint, err := url.Parse(b.endpoint)
	if err != nil || endpoint.Scheme != "tcp" || endpoint.Hostname() == "" || endpoint.Port() == "" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" || b.serverName == "" {
		return nil, fmt.Errorf("Kubernetes builds require HATCH_BUILDKIT_ADDR (tcp://host:port) and HATCH_BUILDKIT_SERVER_NAME")
	}
	if !registryPrefixPattern.MatchString(b.registry) || filepath.Base(b.registryConfig) != "config.json" {
		return nil, fmt.Errorf("set HATCH_REGISTRY_PREFIX to a registry repository and HATCH_REGISTRY_CONFIG to its config.json path")
	}
	for _, file := range []string{b.ca, b.cert, b.key, b.registryConfig} {
		if !filepath.IsAbs(file) {
			return nil, fmt.Errorf("BuildKit mTLS and registry credentials require absolute file paths")
		}
		if _, err := os.Stat(file); err != nil {
			return nil, fmt.Errorf("read build configuration: %w", err)
		}
	}
	if _, err := exec.LookPath("buildctl"); err != nil {
		return nil, fmt.Errorf("Kubernetes builds require buildctl: %w", err)
	}
	return b, nil
}

func (d *Deployer) downloadBuildSource(ctx context.Context, ref PreviewRef, dir string) error {
	owner, repo, ok := splitRepo(ref.Repo)
	if !ok || !buildCommitPattern.MatchString(ref.SHA) {
		return fmt.Errorf("invalid GitHub repository or build commit")
	}
	address := "https://api.github.com/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/tarball/" + ref.SHA
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", githubUserAgent)
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if d.app != nil && ref.InstallationID != 0 {
		tok, err := d.app.installationToken(ctx, ref.InstallationID)
		if err != nil {
			return fmt.Errorf("get installation token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	client := *d.httpExt
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) > 1 || next.URL.Scheme != "https" || next.URL.Host != "codeload.github.com" {
			return fmt.Errorf("unexpected GitHub archive redirect")
		}
		next.Header.Del("Authorization")
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		// Private archives can have a short-lived token in the redirect URL.
		return fmt.Errorf("GitHub archive request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub archive returned HTTP %d", resp.StatusCode)
	}
	return unpackBuildArchive(resp.Body, dir)
}

func unpackBuildArchive(src io.Reader, dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	gzipReader, err := gzip.NewReader(io.LimitReader(src, maxBuildArchive+1))
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	archive := &io.LimitedReader{R: gzipReader, N: maxBuildArchive + 1}
	t := tar.NewReader(archive)
	var top string
	for entries := 0; ; entries++ {
		h, err := t.Next()
		if archive.N <= 0 || entries > 50_000 {
			return fmt.Errorf("GitHub build context exceeds archive limits")
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read GitHub archive: %w", err)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			// GitHub includes the commit ID in a global PAX record.
			continue
		}
		first, name, _ := strings.Cut(strings.TrimSuffix(h.Name, "/"), "/")
		if top == "" {
			top = first
		}
		if first != top || first == "" || first == "." || first == ".." {
			return fmt.Errorf("invalid GitHub archive root")
		}
		if name == "" && h.Typeflag == tar.TypeDir {
			continue
		}
		if name == "" || !filepath.IsLocal(name) || path.Clean(name) != name || h.Size >= archive.N {
			return fmt.Errorf("invalid GitHub archive entry")
		}
		if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = root.MkdirAll(name, 0o755)
		case tar.TypeReg:
			err = writeBuildFile(root, name, h.Mode, t)
		case tar.TypeSymlink:
			if h.Linkname == "" || path.IsAbs(h.Linkname) || !filepath.IsLocal(path.Join(path.Dir(name), h.Linkname)) {
				return fmt.Errorf("invalid GitHub archive symlink")
			}
			err = root.Symlink(h.Linkname, name)
		default:
			return fmt.Errorf("unsupported GitHub archive entry type %d", h.Typeflag)
		}
		if err != nil {
			return err
		}
	}
}

func writeBuildFile(root *os.Root, name string, mode int64, content io.Reader) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644|os.FileMode(mode)&0o111)
	if err != nil {
		return err
	}
	_, writeErr := io.Copy(file, content)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (d *Deployer) buildKubeImage(ctx context.Context, ref PreviewRef, name string, svc *ComposeService, source string) (image string, port int, err error) {
	var logID int64
	_ = d.pool.QueryRow(ctx, `INSERT INTO build_logs (repo_full_name, pr_number, commit_sha, service, status)
		VALUES ($1,$2,$3,$4,'running') RETURNING id`, ref.Repo, ref.PR, ref.SHA, name).Scan(&logID)
	var output strings.Builder
	defer func() {
		if err != nil {
			d.finishBuildLog(ctx, logID, cleanKubeBuildLog(output.String()), "failed", err.Error())
		} else {
			d.finishBuildLog(ctx, logID, cleanKubeBuildLog(output.String()), "success", "")
		}
	}()

	buildPath := path.Clean(svc.Build)
	if !filepath.IsLocal(buildPath) {
		return "", 0, fmt.Errorf("build context must stay inside the repository")
	}
	sourceRoot, err := os.OpenRoot(source)
	if err != nil {
		return "", 0, err
	}
	defer sourceRoot.Close()
	contextDir, err := sourceRoot.OpenRoot(buildPath)
	if err != nil {
		return "", 0, fmt.Errorf("open build context: %w", err)
	}
	defer contextDir.Close()
	contextPath, err := filepath.EvalSymlinks(filepath.Join(source, buildPath))
	if err != nil {
		return "", 0, err
	}
	dockerfile := svc.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	file, err := contextDir.Open(dockerfile)
	if err != nil {
		return "", 0, fmt.Errorf("open Dockerfile relative to its build context: %w", err)
	}
	port = dockerfileExpose(file)
	file.Close()

	// Keep build metadata outside the untrusted source tree.
	metaDir, err := os.MkdirTemp("", "hatch-build-meta-")
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(metaDir)
	metadata := filepath.Join(metaDir, "image.json")
	id := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%s", ref.Repo, ref.PR, name)))
	b := d.kubeBuilder
	tag := fmt.Sprintf("%s:pr-%x-%s", b.registry, id[:12], ref.SHA)
	cmd := exec.CommandContext(ctx, "buildctl", "--addr", b.endpoint,
		"--tlsservername", b.serverName, "--tlscacert", b.ca, "--tlscert", b.cert, "--tlskey", b.key,
		"build", "--frontend", "dockerfile.v0", "--local", "context="+contextPath, "--local", "dockerfile="+contextPath,
		"--opt", "filename="+dockerfile, "--output", "type=image,name="+tag+",push=true", "--metadata-file", metadata, "--progress=plain")
	cmd.Env = []string{"DOCKER_CONFIG=" + filepath.Dir(b.registryConfig), "HOME=" + metaDir}
	cmd.WaitDelay = 5 * time.Second
	if err := d.runKubeBuild(ctx, cmd, logID, &output); err != nil {
		return "", 0, err
	}
	encoded, err := os.ReadFile(metadata)
	if err != nil {
		return "", 0, fmt.Errorf("read BuildKit image metadata: %w", err)
	}
	var result struct {
		Digest string `json:"containerimage.digest"`
	}
	if err := json.Unmarshal(encoded, &result); err != nil || !buildDigestPattern.MatchString(result.Digest) {
		return "", 0, fmt.Errorf("BuildKit did not return a pushed image digest")
	}
	return b.registry + "@" + result.Digest, port, nil
}

func (d *Deployer) runKubeBuild(ctx context.Context, cmd *exec.Cmd, logID int64, output *strings.Builder) error {
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	reader := bufio.NewReader(pipe)
	var lastFlush time.Time
	for {
		line, readErr := reader.ReadSlice('\n')
		if len(line) > 0 && output.Len() < 256<<10 {
			output.Write(line)
		}
		if logID != 0 && time.Since(lastFlush) > 1500*time.Millisecond {
			_, _ = d.pool.Exec(ctx, `UPDATE build_logs SET raw_output=$1 WHERE id=$2`, cleanKubeBuildLog(output.String()), logID)
			lastFlush = time.Now()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil && readErr != bufio.ErrBufferFull {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return readErr
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("BuildKit build failed: %w", err)
	}
	return nil
}

func cleanKubeBuildLog(output string) string {
	return scrubString(strings.ToValidUTF8(strings.ReplaceAll(output, "\x00", ""), "?"))
}

func dockerfileExpose(file io.Reader) int {
	var port int
	s := bufio.NewScanner(io.LimitReader(file, 1<<20))
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 2 {
			continue
		}
		if strings.EqualFold(fields[0], "FROM") {
			port = 0
		}
		if port != 0 || !strings.EqualFold(fields[0], "EXPOSE") {
			continue
		}
		for _, field := range fields[1:] {
			number, protocol, _ := strings.Cut(field, "/")
			p, err := strconv.Atoi(number)
			if err == nil && p > 0 && p <= 65535 && (protocol == "" || strings.EqualFold(protocol, "tcp")) {
				port = p
				break
			}
		}
	}
	return port
}
