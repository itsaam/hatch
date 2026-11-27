package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	kubeServiceAccount = "/var/run/secrets/kubernetes.io/serviceaccount/"
	kubeMaxAPIResponse = 4 << 20
)

var kubeDNSLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type kubeClient struct {
	baseURL   string
	tokenPath string
	http      *http.Client
}

type kubeResource string

const (
	kubeConfigMaps  kubeResource = "/api/v1/namespaces/%s/configmaps"
	kubeSecrets     kubeResource = "/api/v1/namespaces/%s/secrets"
	kubeServices    kubeResource = "/api/v1/namespaces/%s/services"
	kubePods        kubeResource = "/api/v1/namespaces/%s/pods"
	kubeDeployments kubeResource = "/apis/apps/v1/namespaces/%s/deployments"
	kubeIngresses   kubeResource = "/apis/networking.k8s.io/v1/namespaces/%s/ingresses"
	kubeJobs        kubeResource = "/apis/batch/v1/namespaces/%s/jobs"
)

func (r kubeResource) path(namespace string, parts ...string) string {
	path := fmt.Sprintf(string(r), url.PathEscape(namespace))
	for _, part := range parts {
		path += "/" + url.PathEscape(part)
	}
	return path
}

func newKubeClient() (*kubeClient, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if (net.ParseIP(host) == nil && !validKubeDNSName(strings.ToLower(host))) || !validKubePortString(port) {
		return nil, errors.New("kubernetes in-cluster API address is missing or invalid")
	}
	ca, err := readKubeFile(kubeServiceAccount+"ca.crt", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("read kubernetes service account CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errors.New("invalid kubernetes service account CA")
	}

	client := &kubeClient{
		baseURL:   "https://" + net.JoinHostPort(host, port),
		tokenPath: kubeServiceAccount + "token",
		http: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				DialContext:            (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
				TLSClientConfig:        &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
				TLSHandshakeTimeout:    5 * time.Second,
				IdleConnTimeout:        90 * time.Second,
				MaxIdleConnsPerHost:    16,
				MaxResponseHeaderBytes: 64 << 10,
				ForceAttemptHTTP2:      true,
			},
		},
	}
	if _, err := client.token(); err != nil {
		client.http.CloseIdleConnections()
		return nil, err
	}
	return client, nil
}

func readKubeFile(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return readKubeBounded(file, maxBytes)
}

func readKubeBounded(r io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errors.New("kubernetes body exceeds size limit")
	}
	return body, nil
}

func (c *kubeClient) token() (string, error) {
	// The kubelet replaces the projected token atomically. Reopen it for every request.
	body, err := readKubeFile(c.tokenPath, 16<<10)
	if err != nil {
		return "", fmt.Errorf("read kubernetes service account token: %w", err)
	}
	token := strings.TrimSpace(string(body))
	if token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("invalid kubernetes service account token")
	}
	return token, nil
}

type kubeHTTPError struct {
	method string
	path   string
	status int
}

func (e *kubeHTTPError) Error() string {
	return fmt.Sprintf("kubernetes %s %s returned HTTP %d", e.method, e.path, e.status)
}

func kubeHTTPStatus(err error, status int) bool {
	var httpErr *kubeHTTPError
	return errors.As(err, &httpErr) && httpErr.status == status
}

// A create whose outcome is unknown can still materialize after the client
// stops waiting. Keep its slot reserved so another preview cannot start there.
type kubeUncertainCreate struct{ error }

func (e *kubeUncertainCreate) Unwrap() error { return e.error }

func (c *kubeClient) request(ctx context.Context, method, path string, query url.Values, accept string, value, result any, maxBytes int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var payload []byte
	if value != nil {
		var err error
		payload, err = json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode kubernetes %s %s: %w", method, path, err)
		}
	}
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("prepare kubernetes %s %s", method, path)
	}
	token, err := c.token()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", accept)
	if value != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("kubernetes %s %s: %w", method, path, ctx.Err())
		} else {
			err = fmt.Errorf("kubernetes %s %s: request failed", method, path)
		}
		if method == http.MethodPost {
			err = &kubeUncertainCreate{err}
		}
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Kubernetes error bodies can contain object data, including env values or SQL.
		err := error(&kubeHTTPError{method: method, path: path, status: resp.StatusCode})
		if method == http.MethodPost && (resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout) {
			err = &kubeUncertainCreate{err}
		}
		return err
	}

	if result == nil {
		// Drain small success responses for connection reuse, including create responses with Secret data.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 128<<10))
		return nil
	}
	body, err := readKubeBounded(resp.Body, maxBytes)
	if err != nil {
		return fmt.Errorf("read kubernetes %s %s: response unavailable or too large", method, path)
	}
	if raw, ok := result.(*string); ok {
		*raw = string(body)
		return nil
	}
	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("decode kubernetes %s %s: invalid response", method, path)
	}
	return nil
}

func (c *kubeClient) json(ctx context.Context, method, path string, value, result any) error {
	return c.request(ctx, method, path, nil, "application/json", value, result, kubeMaxAPIResponse)
}

func validKubePortString(s string) bool {
	port, err := strconv.ParseUint(s, 10, 16)
	return err == nil && port > 0
}

func validKubeDNSName(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	for _, part := range strings.Split(name, ".") {
		if !kubeDNSLabel.MatchString(part) {
			return false
		}
	}
	return true
}
