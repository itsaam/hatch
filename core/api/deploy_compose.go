package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Container / network naming helpers for the multi-service layout.

func networkName(slug string, pr int) string {
	return fmt.Sprintf("hatch-net-%s-%d", slug, pr)
}

func composeContainerName(slug string, pr int, service string) string {
	return fmt.Sprintf("hatch-pr-%s-%d-%s", slug, pr, service)
}

func stackHost(slug string, pr int, domain string) string {
	return fmt.Sprintf("pr-%d-%s.%s", pr, slug, domain)
}

func buildTag(slug string, pr int, service, sha string) string {
	return fmt.Sprintf("hatch-pr-%s-%d-%s:%s", slug, pr, service, shortSHA(sha))
}

// composeCreateBody is the create payload for multi-service containers. It
// adds fields not used by the legacy single-container path (image command,
// env, named endpoints with aliases, healthcheck).
type composeCreateBody struct {
	Image        string              `json:"Image"`
	Env          []string            `json:"Env,omitempty"`
	Labels       map[string]string   `json:"Labels"`
	HostConfig   composeHostConfig   `json:"HostConfig"`
	Networking   composeNetworking   `json:"NetworkingConfig"`
	Healthcheck  *dockerHealthcheck  `json:"Healthcheck,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
}

type composeHostConfig struct {
	RestartPolicy restartPolicy `json:"RestartPolicy"`
}

type composeNetworking struct {
	EndpointsConfig map[string]composeEndpoint `json:"EndpointsConfig"`
}

type composeEndpoint struct {
	Aliases []string `json:"Aliases,omitempty"`
}

type dockerHealthcheck struct {
	Test     []string `json:"Test,omitempty"`
	Interval int64    `json:"Interval,omitempty"` // nanoseconds
	Retries  int      `json:"Retries,omitempty"`
	Timeout  int64    `json:"Timeout,omitempty"`
}

// deployCompose orchestrates a full stack deploy for a preview.
func (d *Deployer) deployCompose(ctx context.Context, ref PreviewRef, spec *ComposeSpec, app *AppClient) error {
	slug := slugify(ref.Repo)
	sctx := SubstitutionContext{
		PR:         ref.PR,
		SHA:        ref.SHA,
		Repo:       ref.Repo,
		Slug:       slug,
		DBPassword: DeriveDBPassword(webhookSecretFromEnv(), ref.Repo, ref.PR),
	}
	Substitute(spec, sctx)

	order, err := TopoSortServices(spec)
	if err != nil {
		return err
	}

	// Destroy any previous stack first so a redeploy is clean.
	if err := d.destroyStack(ctx, slug, ref.PR); err != nil {
		log.Printf("precleanup %s/%d: %v", slug, ref.PR, err)
	}

	if err := d.ensureNetwork(ctx, networkName(slug, ref.PR)); err != nil {
		return fmt.Errorf("create network: %w", err)
	}

	// Build every service that has a build context.
	for _, name := range order {
		svc := spec.Services[name]
		if svc.Build == "" {
			continue
		}
		tag := buildTag(slug, ref.PR, name, ref.SHA)
		if err := d.build(ctx, ref.Repo, ref.SHA, tag); err != nil {
			return fmt.Errorf("build %s: %w", name, err)
		}
	}

	exposed := ExposedService(spec)
	host := stackHost(slug, ref.PR, d.domain)

	for _, name := range order {
		svc := spec.Services[name]
		image := svc.Image
		if svc.Build != "" {
			image = buildTag(slug, ref.PR, name, ref.SHA)
			// Pull-through is handled by the build; nothing to do here.
		} else if image != "" {
			if err := d.pullImage(ctx, image); err != nil {
				log.Printf("pull image %s: %v (continuing — daemon may have it cached)", image, err)
			}
		}

		port := "80"
		if svc.Port > 0 {
			port = fmt.Sprintf("%d", svc.Port)
		} else if svc.Build != "" {
			if p, err := d.detectPort(ctx, image); err == nil {
				port = p
			}
		}

		if err := d.runComposeService(ctx, ref, spec, slug, name, image, port, host, exposed); err != nil {
			return fmt.Errorf("run %s: %w", name, err)
		}

		if err := d.waitHealthy(ctx, composeContainerName(slug, ref.PR, name), svc.Healthcheck != nil); err != nil {
			return fmt.Errorf("wait healthy %s: %w", name, err)
		}

		// Seed execution happens immediately after the target service is healthy.
		if spec.Seed != nil && spec.Seed.After == name {
			if err := d.runSeed(ctx, app, ref, spec, slug, name); err != nil {
				log.Printf("seed %s (non-fatal): %v", name, err)
			}
		}
	}
	return nil
}

// runComposeService creates + starts one container in the stack.
func (d *Deployer) runComposeService(ctx context.Context, ref PreviewRef, spec *ComposeSpec, slug, service, image, port, host, exposed string) error {
	svc := spec.Services[service]
	cname := composeContainerName(slug, ref.PR, service)

	labels := map[string]string{
		"hatch.managed": "true",
		"hatch.pr":      fmt.Sprintf("%d", ref.PR),
		"hatch.slug":    slug,
		"hatch.repo":    ref.Repo,
		"hatch.service": service,
	}

	endpoints := map[string]composeEndpoint{
		networkName(slug, ref.PR): {Aliases: []string{service}},
	}

	if service == exposed {
		routerID := fmt.Sprintf("hatch-pr-%s-%d", slug, ref.PR)
		labels["traefik.enable"] = "true"
		labels["traefik.docker.network"] = d.network
		labels[fmt.Sprintf("traefik.http.routers.%s.rule", routerID)] = fmt.Sprintf("Host(`%s`)", host)
		labels[fmt.Sprintf("traefik.http.routers.%s.entrypoints", routerID)] = "websecure"
		labels[fmt.Sprintf("traefik.http.routers.%s.tls", routerID)] = "true"
		labels[fmt.Sprintf("traefik.http.routers.%s.tls.certresolver", routerID)] = "letsencrypt"
		labels[fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", routerID)] = port
		// Attach to the public traefik network so routing works.
		endpoints[d.network] = composeEndpoint{}
	}

	env := make([]string, 0, len(svc.Env))
	keys := make([]string, 0, len(svc.Env))
	for k := range svc.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+svc.Env[k])
	}

	body := composeCreateBody{
		Image:  image,
		Env:    env,
		Labels: labels,
		HostConfig: composeHostConfig{
			RestartPolicy: restartPolicy{Name: "unless-stopped"},
		},
		Networking: composeNetworking{
			EndpointsConfig: endpoints,
		},
	}

	if svc.Healthcheck != nil && strings.TrimSpace(svc.Healthcheck.Cmd) != "" {
		interval := svc.Healthcheck.IntervalSeconds
		if interval <= 0 {
			interval = 5
		}
		retries := svc.Healthcheck.Retries
		if retries <= 0 {
			retries = 10
		}
		body.Healthcheck = &dockerHealthcheck{
			Test:     []string{"CMD-SHELL", svc.Healthcheck.Cmd},
			Interval: int64(interval) * int64(time.Second),
			Retries:  retries,
			Timeout:  int64(5) * int64(time.Second),
		}
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.dockerURL("/containers/create?name="+url.QueryEscape(cname)),
		bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("create %s http %d: %s", cname, resp.StatusCode, truncate(string(respBody), 300))
	}

	var created struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil {
		return err
	}
	if created.ID == "" {
		return errors.New("empty container id")
	}

	startReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.dockerURL("/containers/"+created.ID+"/start"), nil)
	if err != nil {
		return err
	}
	startResp, err := d.http.Do(startReq)
	if err != nil {
		return err
	}
	defer startResp.Body.Close()
	if startResp.StatusCode >= 400 {
		b, _ := io.ReadAll(startResp.Body)
		return fmt.Errorf("start %s http %d: %s", cname, startResp.StatusCode, truncate(string(b), 300))
	}
	return nil
}

// ensureNetwork creates a bridge network if it doesn't already exist. 409 =
// already exists = success.
func (d *Deployer) ensureNetwork(ctx context.Context, name string) error {
	payload := map[string]any{
		"Name":           name,
		"Driver":         "bridge",
		"CheckDuplicate": true,
		"Labels":         map[string]string{"hatch.managed": "true"},
	}
	buf, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.dockerURL("/networks/create"), bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict || resp.StatusCode < 400 {
		return nil
	}
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("network create http %d: %s", resp.StatusCode, truncate(string(b), 200))
}

func (d *Deployer) removeNetwork(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		d.dockerURL("/networks/"+url.PathEscape(name)), nil)
	if err != nil {
		return err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode < 400 {
		return nil
	}
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("network remove http %d: %s", resp.StatusCode, truncate(string(b), 200))
}

func (d *Deployer) pullImage(ctx context.Context, image string) error {
	q := url.Values{}
	q.Set("fromImage", image)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.dockerURL("/images/create?"+q.Encode()), nil)
	if err != nil {
		return err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("pull http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	if bytes.Contains(body, []byte(`"error"`)) {
		return fmt.Errorf("pull error: %s", truncate(string(body), 200))
	}
	return nil
}

// waitHealthy polls the container until its healthcheck reports healthy, or
// until it has been running for a brief grace period when no healthcheck is
// defined. Timeout: 90 seconds.
func (d *Deployer) waitHealthy(ctx context.Context, cname string, hasHealthcheck bool) error {
	deadline := time.Now().Add(90 * time.Second)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %s to be healthy", cname)
		}
		status, health, err := d.inspectStatus(ctx, cname)
		if err != nil {
			return err
		}
		if hasHealthcheck {
			switch health {
			case "healthy":
				return nil
			case "unhealthy":
				return fmt.Errorf("%s reported unhealthy", cname)
			}
		} else {
			if status == "running" {
				// small grace period
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(2 * time.Second):
				}
				return nil
			}
			if status == "exited" || status == "dead" {
				return fmt.Errorf("%s exited before becoming ready", cname)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (d *Deployer) inspectStatus(ctx context.Context, cname string) (status, health string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		d.dockerURL("/containers/"+url.PathEscape(cname)+"/json"), nil)
	if err != nil {
		return "", "", err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("inspect %s http %d: %s", cname, resp.StatusCode, truncate(string(b), 200))
	}
	var info struct {
		State struct {
			Status string `json:"Status"`
			Health *struct {
				Status string `json:"Status"`
			} `json:"Health"`
		} `json:"State"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", "", err
	}
	h := ""
	if info.State.Health != nil {
		h = info.State.Health.Status
	}
	return info.State.Status, h, nil
}

// runSeed fetches the SQL file, copies it into the target container, and
// execs psql. A seed failure is logged but does not fail the deploy.
func (d *Deployer) runSeed(ctx context.Context, app *AppClient, ref PreviewRef, spec *ComposeSpec, slug, serviceName string) error {
	seed := spec.Seed
	cname := composeContainerName(slug, ref.PR, serviceName)
	svc := spec.Services[serviceName]

	data, err := fetchRepoFile(ctx, d.httpExt, app, ref.InstallationID, ref.Repo, seed.SQL, ref.SHA)
	if err != nil {
		return fmt.Errorf("fetch seed: %w", err)
	}
	if data == nil {
		return fmt.Errorf("seed file %s not found at %s", seed.SQL, shortSHA(ref.SHA))
	}

	if err := d.uploadToContainer(ctx, cname, "/tmp", "hatch-seed.sql", data); err != nil {
		return fmt.Errorf("upload seed: %w", err)
	}

	user := svc.Env["POSTGRES_USER"]
	db := svc.Env["POSTGRES_DB"]
	if user == "" {
		user = "postgres"
	}
	if db == "" {
		db = user
	}

	if err := d.execInContainer(ctx, cname, []string{"psql", "-v", "ON_ERROR_STOP=1", "-U", user, "-d", db, "-f", "/tmp/hatch-seed.sql"}); err != nil {
		return fmt.Errorf("psql exec: %w", err)
	}
	log.Printf("seed applied on %s", cname)
	return nil
}

// uploadToContainer tars a single file into the given directory inside the
// container using the Docker archive API.
func (d *Deployer) uploadToContainer(ctx context.Context, cname, destDir, filename string, content []byte) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: filename,
		Mode: 0o644,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := tw.Write(content); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}

	q := url.Values{}
	q.Set("path", destDir)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		d.dockerURL("/containers/"+url.PathEscape(cname)+"/archive?"+q.Encode()),
		bytes.NewReader(buf.Bytes()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("archive put http %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	return nil
}

// execInContainer runs a command inside a container and waits for it to exit.
func (d *Deployer) execInContainer(ctx context.Context, cname string, cmd []string) error {
	createBody := map[string]any{
		"AttachStdout": true,
		"AttachStderr": true,
		"Cmd":          cmd,
	}
	buf, _ := json.Marshal(createBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.dockerURL("/containers/"+url.PathEscape(cname)+"/exec"),
		bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("exec create http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return err
	}
	if created.ID == "" {
		return errors.New("empty exec id")
	}

	startPayload := map[string]any{"Detach": false, "Tty": false}
	buf, _ = json.Marshal(startPayload)
	startReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.dockerURL("/exec/"+created.ID+"/start"),
		bytes.NewReader(buf))
	if err != nil {
		return err
	}
	startReq.Header.Set("Content-Type", "application/json")
	startResp, err := d.http.Do(startReq)
	if err != nil {
		return err
	}
	defer startResp.Body.Close()
	_, _ = io.Copy(io.Discard, startResp.Body)

	// Poll exec state for a terminal result with a short timeout.
	deadline := time.Now().Add(60 * time.Second)
	for {
		if time.Now().After(deadline) {
			return errors.New("exec timeout")
		}
		inspectReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
			d.dockerURL("/exec/"+created.ID+"/json"), nil)
		if err != nil {
			return err
		}
		ir, err := d.http.Do(inspectReq)
		if err != nil {
			return err
		}
		var info struct {
			Running  bool `json:"Running"`
			ExitCode int  `json:"ExitCode"`
		}
		_ = json.NewDecoder(ir.Body).Decode(&info)
		ir.Body.Close()
		if !info.Running {
			if info.ExitCode != 0 {
				return fmt.Errorf("exec exit code %d", info.ExitCode)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// --- Stack-level destroy ----------------------------------------------------

// destroyStack removes all containers and the network belonging to a given
// (slug, pr) pair. Returns the number of containers removed.
func (d *Deployer) destroyStack(ctx context.Context, slug string, pr int) error {
	containers, err := d.listStackContainers(ctx, slug, pr)
	if err != nil {
		return err
	}
	for _, c := range containers {
		name := primaryContainerName(c.Names)
		if name == "" {
			name = c.ID
		}
		if err := d.remove(ctx, name); err != nil {
			log.Printf("destroy %s: %v", name, err)
		}
	}
	if err := d.removeNetwork(ctx, networkName(slug, pr)); err != nil {
		log.Printf("destroy network %s: %v", networkName(slug, pr), err)
	}
	return nil
}

type dockerContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Labels map[string]string `json:"Labels"`
}

func (d *Deployer) listStackContainers(ctx context.Context, slug string, pr int) ([]dockerContainer, error) {
	filters := fmt.Sprintf(`{"label":["hatch.managed=true","hatch.slug=%s","hatch.pr=%d"]}`, slug, pr)
	q := url.Values{}
	q.Set("all", "true")
	q.Set("filters", filters)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		d.dockerURL("/containers/json?"+q.Encode()), nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list stack http %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	var out []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// listAllHatchContainers returns every container with hatch.managed=true and
// a parseable (slug, pr) label pair.
func (d *Deployer) listAllHatchContainers(ctx context.Context) ([]dockerContainer, error) {
	filters := `{"label":["hatch.managed=true"]}`
	q := url.Values{}
	q.Set("all", "true")
	q.Set("filters", filters)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		d.dockerURL("/containers/json?"+q.Encode()), nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list hatch containers http %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	var out []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
