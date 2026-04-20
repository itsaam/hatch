package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const dockerAPIVersion = "v1.43"

type Deployer struct {
	http    *http.Client
	pool    *pgxpool.Pool
	network string
	domain  string
}

func NewDeployer(pool *pgxpool.Pool, netName, domain string) (*Deployer, error) {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", "/var/run/docker.sock")
		},
	}
	return &Deployer{
		http:    &http.Client{Transport: tr, Timeout: 15 * time.Minute},
		pool:    pool,
		network: netName,
		domain:  domain,
	}, nil
}

func (d *Deployer) Deploy(repo string, pr int, branch, sha string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	slug := slugify(repo)
	name := fmt.Sprintf("hatch-preview-%s-%d", slug, pr)
	tag := fmt.Sprintf("%s:%s", name, shortSHA(sha))
	host := fmt.Sprintf("pr-%d.%s.%s", pr, slug, d.domain)
	publicURL := "http://" + host

	log.Printf("deploy start: %s → %s", name, publicURL)
	d.setStatus(ctx, repo, pr, "building", "")

	if err := d.build(ctx, repo, sha, tag); err != nil {
		log.Printf("build failed %s: %v", name, err)
		d.setStatus(ctx, repo, pr, "failed", "")
		return
	}

	_ = d.remove(ctx, name)

	if err := d.run(ctx, name, tag, host); err != nil {
		log.Printf("run failed %s: %v", name, err)
		d.setStatus(ctx, repo, pr, "failed", "")
		return
	}

	log.Printf("deploy ok: %s → %s", name, publicURL)
	d.setStatus(ctx, repo, pr, "running", publicURL)
}

func (d *Deployer) Destroy(repo string, pr int) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()
	name := fmt.Sprintf("hatch-preview-%s-%d", slugify(repo), pr)
	if err := d.remove(ctx, name); err != nil {
		log.Printf("destroy %s: %v", name, err)
		return
	}
	log.Printf("preview destroyed %s", name)
}

func (d *Deployer) build(ctx context.Context, repo, sha, tag string) error {
	remote := fmt.Sprintf("https://github.com/%s.git#%s", repo, sha)
	q := url.Values{}
	q.Set("remote", remote)
	q.Set("t", tag)
	q.Set("q", "1")
	q.Set("forcerm", "1")

	req, err := http.NewRequestWithContext(ctx, "POST",
		"http://docker/"+dockerAPIVersion+"/build?"+q.Encode(),
		bytes.NewReader(nil))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/tar")

	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("build http %d: %s", resp.StatusCode, truncate(string(body), 500))
	}
	if bytes.Contains(body, []byte(`"error"`)) {
		return fmt.Errorf("build stream error: %s", truncate(string(body), 500))
	}
	return nil
}

type createBody struct {
	Image            string                      `json:"Image"`
	Labels           map[string]string           `json:"Labels"`
	HostConfig       hostConfig                  `json:"HostConfig"`
	NetworkingConfig networkingConfig            `json:"NetworkingConfig"`
	Env              []string                    `json:"Env,omitempty"`
	ExposedPorts     map[string]struct{}         `json:"ExposedPorts,omitempty"`
}

type hostConfig struct {
	RestartPolicy restartPolicy `json:"RestartPolicy"`
}

type restartPolicy struct {
	Name string `json:"Name"`
}

type networkingConfig struct {
	EndpointsConfig map[string]struct{} `json:"EndpointsConfig"`
}

func (d *Deployer) run(ctx context.Context, name, tag, host string) error {
	port, err := d.detectPort(ctx, tag)
	if err != nil {
		port = "80"
	}

	r := name
	body := createBody{
		Image: tag,
		Labels: map[string]string{
			"traefik.enable": "true",
			fmt.Sprintf("traefik.http.routers.%s.rule", r):                      fmt.Sprintf("Host(`%s`)", host),
			fmt.Sprintf("traefik.http.routers.%s.entrypoints", r):               "web",
			fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port", r): port,
			"hatch.managed": "true",
		},
		HostConfig: hostConfig{
			RestartPolicy: restartPolicy{Name: "unless-stopped"},
		},
		NetworkingConfig: networkingConfig{
			EndpointsConfig: map[string]struct{}{d.network: {}},
		},
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		"http://docker/"+dockerAPIVersion+"/containers/create?name="+url.QueryEscape(name),
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
		return fmt.Errorf("create http %d: %s", resp.StatusCode, truncate(string(respBody), 300))
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

	startReq, err := http.NewRequestWithContext(ctx, "POST",
		"http://docker/"+dockerAPIVersion+"/containers/"+created.ID+"/start", nil)
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
		return fmt.Errorf("start http %d: %s", startResp.StatusCode, truncate(string(b), 300))
	}
	return nil
}

func (d *Deployer) detectPort(ctx context.Context, tag string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"http://docker/"+dockerAPIVersion+"/images/"+url.PathEscape(tag)+"/json", nil)
	if err != nil {
		return "", err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("inspect %d", resp.StatusCode)
	}
	var info struct {
		Config struct {
			ExposedPorts map[string]struct{} `json:"ExposedPorts"`
		} `json:"Config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}
	for p := range info.Config.ExposedPorts {
		if i := strings.IndexByte(p, '/'); i > 0 {
			return p[:i], nil
		}
		return p, nil
	}
	return "", errors.New("no exposed port")
}

func (d *Deployer) remove(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE",
		"http://docker/"+dockerAPIVersion+"/containers/"+url.PathEscape(name)+"?force=1&v=1", nil)
	if err != nil {
		return err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 || resp.StatusCode < 400 {
		return nil
	}
	b, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("remove http %d: %s", resp.StatusCode, truncate(string(b), 200))
}

func (d *Deployer) setStatus(ctx context.Context, repo string, pr int, status, publicURL string) {
	var err error
	if publicURL == "" {
		_, err = d.pool.Exec(ctx,
			`UPDATE previews SET status=$1, updated_at=NOW() WHERE repo_full_name=$2 AND pr_number=$3`,
			status, repo, pr)
	} else {
		_, err = d.pool.Exec(ctx,
			`UPDATE previews SET status=$1, url=$2, updated_at=NOW() WHERE repo_full_name=$3 AND pr_number=$4`,
			status, publicURL, repo, pr)
	}
	if err != nil {
		log.Printf("setStatus: %v", err)
	}
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = slugRE.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
