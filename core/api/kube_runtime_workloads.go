package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type kubeFields = map[string]any

const (
	kubeMaxSecretData = 512 << 10
	kubeReadyTimeout  = 2 * time.Minute
)

var (
	kubeEnvName   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	kubeImagePath = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*$`)
	kubeImageTag  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
)

func newKubeRuntime() (*kubeRuntime, error) {
	var namespaces []string
	seen := make(map[string]bool)
	for _, entry := range strings.Split(os.Getenv("HATCH_KUBE_PREVIEW_NAMESPACES"), ",") {
		ns := strings.TrimSpace(entry)
		if !kubeDNSLabel.MatchString(ns) {
			return nil, errors.New("HATCH_KUBE_PREVIEW_NAMESPACES must contain comma-separated namespace names")
		}
		if seen[ns] {
			return nil, fmt.Errorf("duplicate kubernetes preview namespace %q", ns)
		}
		seen[ns] = true
		namespaces = append(namespaces, ns)
	}
	k := &kubeRuntime{
		namespaces:      namespaces,
		ingressClass:    strings.TrimSpace(os.Getenv("HATCH_KUBE_INGRESS_CLASS")),
		tlsSecret:       strings.TrimSpace(os.Getenv("HATCH_KUBE_TLS_SECRET")),
		imagePullSecret: strings.TrimSpace(os.Getenv("HATCH_KUBE_IMAGE_PULL_SECRET")),
	}
	if k.ingressClass == "" {
		k.ingressClass = "traefik"
	}
	for _, setting := range []struct{ name, value string }{
		{"HATCH_KUBE_INGRESS_CLASS", k.ingressClass},
		{"HATCH_KUBE_TLS_SECRET", k.tlsSecret},
		{"HATCH_KUBE_IMAGE_PULL_SECRET", k.imagePullSecret},
	} {
		if setting.value != "" && !validKubeDNSName(setting.value) {
			return nil, fmt.Errorf("%s must be a valid Kubernetes resource name", setting.name)
		}
	}

	client, err := newKubeClient()
	if err != nil {
		return nil, err
	}
	k.client = client
	return k, nil
}

type kubeServicePlan struct {
	name    string
	service *ComposeService
	image   string
	port    int
	probe   kubeFields
	startup kubeFields
}

func planKubeServices(ctx context.Context, spec *ComposeSpec, host string, build func(context.Context, string, *ComposeService) (string, int, error)) ([]kubeServicePlan, error) {
	if err := validateCompose(spec); err != nil {
		return nil, err
	}
	if ExposedService(spec) != "" && (!validKubeDNSName(host) || net.ParseIP(host) != nil) {
		return nil, errors.New("invalid kubernetes preview hostname")
	}
	order, err := TopoSortServices(spec)
	if err != nil {
		return nil, err
	}
	for _, name := range order {
		if err := validateKubeService(name, spec.Services[name]); err != nil {
			return nil, err
		}
	}

	plans := make([]kubeServicePlan, 0, len(order))
	for _, name := range order {
		svc := spec.Services[name]
		image := svc.Image
		var detectedPort int
		if strings.TrimSpace(svc.Build) != "" {
			if build == nil {
				return nil, fmt.Errorf("kubernetes service %s requires an image builder", name)
			}
			image, detectedPort, err = build(ctx, name, svc)
			if err != nil {
				return nil, fmt.Errorf("build kubernetes service %s: %w", name, scrubToken(err))
			}
			if !validKubeImage(image) || detectedPort < 0 || detectedPort > 65535 {
				return nil, fmt.Errorf("kubernetes builder returned an invalid image or port for service %s", name)
			}
		}
		port := svc.Port
		if port == 0 {
			port = detectedPort
		}
		if port == 0 {
			port = kubeDependencyPort(name, image)
		}
		probe := kubeReadinessProbe(svc, port)
		needsStartupProbe := svc.Healthcheck != nil && (port > 0 || svc.Expose)
		if port == 0 {
			port = 80
		}
		var startup kubeFields
		if needsStartupProbe {
			// A local healthcheck can pass before a network listener is ready.
			// The PostgreSQL image, for example, initially listens on a Unix socket.
			startup = kubeTCPProbe(port)
			startup["failureThreshold"] = 60
		}
		plans = append(plans, kubeServicePlan{name: name, service: svc, image: image, port: port, probe: probe, startup: startup})
	}
	return plans, nil
}

func validateKubeService(name string, svc *ComposeService) error {
	if svc.Port < 0 || svc.Port > 65535 {
		return fmt.Errorf("kubernetes service %s port must be between 1 and 65535 when set", name)
	}
	if strings.TrimSpace(svc.Build) == "" && !validKubeImage(svc.Image) {
		return fmt.Errorf("kubernetes service %s has an invalid image reference", name)
	}
	size := 0
	for key, value := range svc.Env {
		if len(key) > 253 || !kubeEnvName.MatchString(key) || strings.ContainsRune(value, 0) {
			return fmt.Errorf("kubernetes service %s has an invalid environment entry", name)
		}
		size += len(key) + len(value)
		if size > kubeMaxSecretData {
			return fmt.Errorf("kubernetes service %s environment exceeds size limit", name)
		}
	}
	if hc := svc.Healthcheck; hc != nil {
		if strings.TrimSpace(hc.Cmd) == "" || len(hc.Cmd) > 4096 || strings.ContainsRune(hc.Cmd, 0) ||
			hc.IntervalSeconds < 0 || hc.IntervalSeconds > 60 || hc.Retries < 0 || hc.Retries > 60 {
			return fmt.Errorf("kubernetes service %s has an invalid healthcheck", name)
		}
	}
	if svc.Limits != nil {
		if math.IsNaN(svc.Limits.CPU) {
			return fmt.Errorf("kubernetes service %s has an invalid CPU limit", name)
		}
		if memory := strings.TrimSpace(svc.Limits.Memory); memory != "" {
			memory = strings.ToLower(memory)
			factor := int64(1)
			switch memory[len(memory)-1] {
			case 'k':
				factor = 1 << 10
			case 'm':
				factor = 1 << 20
			case 'g':
				factor = 1 << 30
			}
			if factor > 1 {
				memory = strings.TrimSpace(memory[:len(memory)-1])
			}
			amount, err := strconv.ParseInt(memory, 10, 64)
			if err != nil || amount <= 0 || amount > math.MaxInt64/factor {
				return fmt.Errorf("kubernetes service %s has an invalid memory limit", name)
			}
		}
	}
	return nil
}

func validKubeImage(image string) bool {
	if image == "" || len(image) > 512 {
		return false
	}
	name, digest, hasDigest := strings.Cut(image, "@")
	if hasDigest {
		algo, hash, ok := strings.Cut(digest, ":")
		length := map[string]int{"sha256": 64, "sha384": 96, "sha512": 128}[algo]
		if !ok || length == 0 || len(hash) != length || strings.ToLower(hash) != hash {
			return false
		}
		if _, err := hex.DecodeString(hash); err != nil {
			return false
		}
	}
	// A colon after the last slash is a tag. Earlier colons belong to
	// the registry, which may include a port or a bracketed IPv6 address.
	if idx := strings.LastIndex(name, ":"); idx > strings.LastIndex(name, "/") {
		if !kubeImageTag.MatchString(name[idx+1:]) {
			return false
		}
		name = name[:idx]
	}
	if len(name) > 255 {
		return false
	}
	parts := strings.Split(name, "/")
	first := parts[0]
	if len(parts) > 1 && (first == "localhost" || strings.ContainsAny(first, ".:") || first != strings.ToLower(first)) {
		if !validKubeRegistry(first) {
			return false
		}
		parts = parts[1:]
	}
	for _, part := range parts {
		if !kubeImagePath.MatchString(part) {
			return false
		}
	}
	return true
}

func validKubeRegistry(registry string) bool {
	if strings.HasPrefix(registry, "[") {
		address, rest, ok := strings.Cut(registry[1:], "]")
		return ok && strings.Contains(address, ":") && net.ParseIP(address) != nil &&
			(rest == "" || (strings.HasPrefix(rest, ":") && validKubePortString(rest[1:])))
	}
	host, port, hasPort := strings.Cut(registry, ":")
	return validKubeDNSName(strings.ToLower(host)) && (!hasPort || validKubePortString(port))
}

func kubeDependencyPort(name, image string) int {
	withoutDigest, _, _ := strings.Cut(image, "@")
	imageBase := withoutDigest[strings.LastIndex(withoutDigest, "/")+1:]
	imageBase, _, _ = strings.Cut(imageBase, ":")
	for _, candidate := range []string{imageBase, name} {
		switch candidate {
		case "postgres", "postgresql", "postgis":
			return 5432
		case "redis", "valkey":
			return 6379
		}
	}
	return 0
}

func kubeReadinessProbe(svc *ComposeService, port int) kubeFields {
	if hc := svc.Healthcheck; hc != nil {
		period, retries := hc.IntervalSeconds, hc.Retries
		if period == 0 {
			period = 5
		}
		if retries == 0 {
			retries = 3
		}
		// This shell command comes from validated project configuration and
		// runs entirely inside the workload container.
		return kubeFields{
			"exec":           kubeFields{"command": []string{"/bin/sh", "-c", hc.Cmd}},
			"timeoutSeconds": 5, "periodSeconds": period, "failureThreshold": retries,
		}
	}
	if port == 0 && !svc.Expose {
		// A worker with no declared or inferred port only needs to stay running.
		return nil
	}
	if port == 0 {
		port = 80
	}
	return kubeTCPProbe(port)
}

func kubeTCPProbe(port int) kubeFields {
	return kubeFields{
		"tcpSocket": kubeFields{"port": port}, "periodSeconds": 2, "timeoutSeconds": 1,
	}
}

func kubeObject(version, kind string, metadata kubeFields, bodyKey string, body any) kubeFields {
	return kubeFields{"apiVersion": version, "kind": kind, "metadata": metadata, bodyKey: body}
}

func (k *kubeRuntime) podSpec(container kubeFields) kubeFields {
	pod := kubeFields{
		"automountServiceAccountToken": false,
		"enableServiceLinks":           false,
		"securityContext":              kubeFields{"seccompProfile": kubeFields{"type": "RuntimeDefault"}},
		"containers":                   []kubeFields{container},
	}
	if k.imagePullSecret != "" {
		pod["imagePullSecrets"] = []kubeFields{{"name": k.imagePullSecret}}
	}
	return pod
}

func kubeContainerSecurity(drop string) kubeFields {
	return kubeFields{
		"privileged": false, "allowPrivilegeEscalation": false,
		"capabilities": kubeFields{"drop": []string{drop}},
	}
}

func kubeResources(cpuMilli, memBytes int64) kubeFields {
	return kubeFields{
		"limits":   kubeFields{"cpu": fmt.Sprintf("%dm", cpuMilli), "memory": strconv.FormatInt(memBytes, 10)},
		"requests": kubeFields{"cpu": fmt.Sprintf("%dm", min(cpuMilli, 50)), "memory": strconv.FormatInt(min(memBytes, 64<<20), 10)},
	}
}

func (k *kubeRuntime) startService(ctx context.Context, slot *kubeSlot, plan kubeServicePlan) error {
	selector := slot.labels()
	selector[kubeAppLabel] = plan.name
	labels := slot.labels()
	labels[kubeAppLabel] = plan.name
	if plan.service.Expose {
		labels[kubePublicLabel] = "true"
	}

	// Kubernetes Secret data is base64 encoded by encoding/json.
	env := make(map[string][]byte, len(plan.service.Env))
	for key, val := range plan.service.Env {
		env[key] = []byte(val)
	}
	secretName := slot.envSecretName(plan.name)
	err := k.createSecret(ctx, slot, secretName, env, kubePendingName)
	if err != nil {
		return err
	}
	service := kubeObject("v1", "Service", slot.metadata(plan.name, labels), "spec", kubeFields{
		"type": "ClusterIP", "selector": selector,
		"ports": []kubeFields{{"name": "tcp", "port": plan.port, "targetPort": plan.port}},
	})
	if err := k.createOwned(ctx, slot, kubeServices, plan.name, service, kubePendingName); err != nil {
		return err
	}
	memBytes, nanoCPU := resolveLimits(plan.service.Limits)
	container := kubeFields{
		"name": plan.name, "image": plan.image, "imagePullPolicy": "Always",
		"ports":           []kubeFields{{"containerPort": plan.port}},
		"envFrom":         []kubeFields{{"secretRef": kubeFields{"name": secretName}}},
		"resources":       kubeResources(max(1, (nanoCPU+999999)/1000000), memBytes),
		"securityContext": kubeContainerSecurity("NET_RAW"),
	}
	if plan.probe != nil {
		container["readinessProbe"] = plan.probe
	}
	if plan.startup != nil {
		container["startupProbe"] = plan.startup
	}
	deployment := kubeObject("apps/v1", "Deployment", slot.metadata(plan.name, labels), "spec", kubeFields{
		"replicas": 1, "minReadySeconds": 3, "progressDeadlineSeconds": int(kubeReadyTimeout.Seconds()) - 5,
		"selector": kubeFields{"matchLabels": selector},
		"template": kubeFields{
			"metadata": kubeFields{"labels": labels},
			"spec":     k.podSpec(container),
		},
	})
	if err := k.createOwned(ctx, slot, kubeDeployments, plan.name, deployment, kubePendingName); err != nil {
		return err
	}
	return k.waitDeployment(ctx, slot, plan.name)
}

func (k *kubeRuntime) waitDeployment(ctx context.Context, slot *kubeSlot, name string) error {
	ctx, cancel := context.WithTimeout(ctx, kubeReadyTimeout)
	defer cancel()
	path := kubeDeployments.path(slot.namespace, name)
	return waitKube(ctx, func() (bool, error) {
		var deployment struct {
			Metadata kubeMeta `json:"metadata"`
			Status   struct {
				AvailableReplicas int `json:"availableReplicas"`
				Conditions        []struct {
					Type   string `json:"type"`
					Reason string `json:"reason"`
				} `json:"conditions"`
			} `json:"status"`
		}
		if err := k.client.json(ctx, http.MethodGet, path, nil, &deployment); err != nil {
			return false, err
		}
		if !slot.ownsDirectly(deployment.Metadata) || deployment.Metadata.DeletionTimestamp != nil {
			return false, errors.New("kubernetes deployment changed while waiting for readiness")
		}
		for _, condition := range deployment.Status.Conditions {
			if condition.Type == "Progressing" && condition.Reason == "ProgressDeadlineExceeded" {
				return false, errors.New("kubernetes deployment exceeded its progress deadline")
			}
		}
		return deployment.Status.AvailableReplicas > 0, nil
	})
}

func (s *kubeSlot) envSecretName(service string) string {
	return service + ".env." + s.claim.Metadata.UID
}

func (k *kubeRuntime) createSecret(ctx context.Context, slot *kubeSlot, name string, data map[string][]byte, pendingName string) error {
	secret := kubeObject("v1", "Secret", slot.metadata(name, slot.labels()), "data", data)
	secret["immutable"] = true
	return k.createOwned(ctx, slot, kubeSecrets, name, secret, pendingName)
}

func (k *kubeRuntime) createIngress(ctx context.Context, slot *kubeSlot, host, name string) error {
	spec := kubeFields{
		"ingressClassName": k.ingressClass,
		"rules": []kubeFields{{
			"host": host,
			"http": kubeFields{"paths": []kubeFields{{
				"path": "/", "pathType": "Prefix",
				"backend": kubeFields{"service": kubeFields{"name": name, "port": kubeFields{"name": "tcp"}}},
			}}},
		}},
	}
	if k.tlsSecret != "" {
		spec["tls"] = []kubeFields{{"hosts": []string{host}, "secretName": k.tlsSecret}}
	}
	ingress := kubeObject("networking.k8s.io/v1", "Ingress", slot.metadata("hatch-preview", slot.labels()), "spec", spec)
	return k.createOwned(ctx, slot, kubeIngresses, "hatch-preview", ingress, kubePendingName)
}
