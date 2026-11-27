package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	kubeClaimName   = "hatch-claim"
	kubeClaimLabel  = "hatchpr.dev/claim"
	kubeAppLabel    = "hatchpr.dev/service"
	kubePublicLabel = "hatchpr.dev/exposed"
	kubeSeedName    = "hatch-seed"
	kubePoll        = time.Second
	kubeStopTime    = 2 * time.Minute
)

var errKubeSlotsFull = errors.New("all kubernetes preview namespaces are claimed")

type kubeRuntime struct {
	client          *kubeClient
	namespaces      []string
	ingressClass    string
	tlsSecret       string
	imagePullSecret string

	// Serialize changes to the same preview. Different PRs claim slots independently.
	locksMu sync.Mutex
	locks   map[kubePreviewKey]*kubePreviewLock
}

type kubePreviewKey struct {
	repo string
	pr   int
}

type kubePreviewLock struct {
	token chan struct{}
	users int
}

type kubeMeta struct {
	Name              string            `json:"name"`
	UID               string            `json:"uid"`
	Labels            map[string]string `json:"labels"`
	DeletionTimestamp *time.Time        `json:"deletionTimestamp"`
	CreationTimestamp time.Time         `json:"creationTimestamp"`
	OwnerReferences   []struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Name       string `json:"name"`
		UID        string `json:"uid"`
	} `json:"ownerReferences"`
}

type kubeClaim struct {
	Metadata kubeMeta          `json:"metadata"`
	Data     map[string]string `json:"data"`
}

type kubeSlot struct {
	namespace string
	claim     kubeClaim
}

func (k *kubeRuntime) lock(ctx context.Context, ref PreviewRef) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := kubePreviewKey{repo: ref.Repo, pr: ref.PR}
	k.locksMu.Lock()
	if k.locks == nil {
		k.locks = make(map[kubePreviewKey]*kubePreviewLock)
	}
	lock := k.locks[key]
	if lock == nil {
		lock = &kubePreviewLock{token: make(chan struct{}, 1)}
		k.locks[key] = lock
	}
	lock.users++
	k.locksMu.Unlock()

	release := func() {
		k.locksMu.Lock()
		lock.users--
		if lock.users == 0 {
			delete(k.locks, key)
		}
		k.locksMu.Unlock()
	}
	select {
	case lock.token <- struct{}{}:
		return func() {
			<-lock.token
			release()
		}, nil
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
}

func validateKubeRef(ref PreviewRef) error {
	if ref.Repo == "" || len(ref.Repo) > 256 || ref.PR < 1 || len(ref.SHA) > 128 {
		return errors.New("invalid kubernetes preview identity")
	}
	return nil
}

func kubeClaimData(ref PreviewRef) map[string]string {
	return map[string]string{"repo": ref.Repo, "pr": strconv.Itoa(ref.PR), "sha": ref.SHA}
}

func (c kubeClaim) matches(ref PreviewRef, matchSHA bool) bool {
	return c.Data["repo"] == ref.Repo && c.Data["pr"] == strconv.Itoa(ref.PR) &&
		(!matchSHA || c.Data["sha"] == ref.SHA)
}

func (c kubeClaim) valid() bool {
	return c.Metadata.Name == kubeClaimName && kubeDNSLabel.MatchString(c.Metadata.UID) &&
		c.Data["repo"] != "" && c.Data["pr"] != "" && c.Data["sha"] != ""
}

func (k *kubeRuntime) getClaim(ctx context.Context, namespace string) (kubeClaim, error) {
	var claim kubeClaim
	err := k.client.json(ctx, http.MethodGet, kubeConfigMaps.path(namespace, kubeClaimName), nil, &claim)
	return claim, err
}

func (k *kubeRuntime) locate(ctx context.Context, ref PreviewRef) ([]*kubeSlot, []string, error) {
	var slots []*kubeSlot
	var empty []string
	for _, ns := range k.namespaces {
		claim, err := k.getClaim(ctx, ns)
		if kubeHTTPStatus(err, http.StatusNotFound) {
			empty = append(empty, ns)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if claim.matches(ref, false) {
			if !claim.valid() {
				return nil, nil, errors.New("kubernetes preview has an invalid claim")
			}
			slots = append(slots, &kubeSlot{namespace: ns, claim: claim})
		}
	}
	return slots, empty, nil
}

func (k *kubeRuntime) claimSlot(ctx context.Context, ref PreviewRef) (*kubeSlot, error) {
	previous, empty, err := k.locate(ctx, ref)
	if err != nil {
		return nil, err
	}
	// An indeterminate claim create can arrive after a retry claims another
	// slot. It never launches resources, so reconcile all prior claims here.
	candidates := make([]string, 0, len(previous)+len(empty))
	for _, old := range previous {
		if err := k.cleanSlot(ctx, old); err != nil {
			return nil, err
		}
		candidates = append(candidates, old.namespace)
	}
	candidates = append(candidates, empty...)

	for _, ns := range candidates {
		body := kubeObject("v1", "ConfigMap", kubeFields{"name": kubeClaimName}, "data", kubeClaimData(ref))
		body["immutable"] = true
		err := k.client.json(ctx, http.MethodPost, kubeConfigMaps.path(ns), body, nil)
		if kubeHTTPStatus(err, http.StatusConflict) {
			continue
		}
		if err != nil {
			return nil, err
		}

		claim, err := k.getClaim(ctx, ns)
		if err != nil {
			return nil, err
		}
		if !claim.valid() || !claim.matches(ref, true) || claim.Metadata.DeletionTimestamp != nil {
			return nil, errors.New("could not verify the kubernetes preview claim")
		}
		slot := &kubeSlot{namespace: ns, claim: claim}
		// Fail closed if an earlier coordinator left objects behind without a claim.
		// Retain the new claim so another tenant cannot use this slot.
		if err := k.checkSlotEmpty(ctx, slot.namespace); err != nil {
			return nil, err
		}
		return slot, nil
	}
	return nil, errKubeSlotsFull
}

func (k *kubeRuntime) deploy(ctx context.Context, ref PreviewRef, spec *ComposeSpec, host string, build func(context.Context, string, *ComposeService) (string, int, error), seed func(context.Context, string) ([]byte, error)) (result error) {
	if err := validateKubeRef(ref); err != nil {
		return err
	}
	if ref.SHA == "" {
		return errors.New("kubernetes preview deploy requires a commit SHA")
	}
	unlock, err := k.lock(ctx, ref)
	if err != nil {
		return err
	}
	defer unlock()

	previous, empty, err := k.locate(ctx, ref)
	if err != nil {
		return err
	}
	if len(previous) == 0 && len(empty) == 0 {
		return errKubeSlotsFull
	}
	plans, err := planKubeServices(ctx, spec, host, build)
	if err != nil {
		return err
	}
	slot, err := k.claimSlot(ctx, ref)
	if err != nil {
		return err
	}
	defer func() {
		var uncertain *kubeUncertainCreate
		if result == nil || errors.As(result, &uncertain) {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), kubeStopTime)
		defer cancel()
		result = errors.Join(result, k.cleanSlot(cleanupCtx, slot))
	}()

	for _, plan := range plans {
		if err := k.startService(ctx, slot, plan); err != nil {
			return fmt.Errorf("start kubernetes service %s: %w", plan.name, err)
		}
		if spec.Seed != nil && spec.Seed.After == plan.name {
			if err := k.seedPostgres(ctx, slot, plan, spec.Seed.SQL, seed); err != nil {
				log.Printf("kubernetes seed for %s failed: %v", plan.name, err)
			}
		}
	}
	if exposed := ExposedService(spec); exposed != "" {
		return k.createIngress(ctx, slot, host, exposed)
	}
	return nil
}

func (k *kubeRuntime) destroy(ctx context.Context, ref PreviewRef) error {
	if err := validateKubeRef(ref); err != nil {
		return err
	}
	unlock, err := k.lock(ctx, ref)
	if err != nil {
		return err
	}
	defer unlock()

	slots, _, err := k.locate(ctx, ref)
	if err != nil {
		return err
	}
	var cleanupErrors []error
	for _, slot := range slots {
		if !slot.matchesRevision(ref) {
			idle, err := k.slotIsIdle(ctx, slot)
			if err != nil {
				cleanupErrors = append(cleanupErrors, err)
			}
			if !idle {
				continue
			}
		}
		cleanupErrors = append(cleanupErrors, k.cleanSlot(ctx, slot))
	}
	return errors.Join(cleanupErrors...)
}

func (k *kubeRuntime) exists(ctx context.Context, ref PreviewRef) (bool, error) {
	if err := validateKubeRef(ref); err != nil {
		return false, err
	}
	slots, _, err := k.locate(ctx, ref)
	if err != nil {
		return false, err
	}
	for _, slot := range slots {
		if !slot.matchesRevision(ref) {
			continue
		}
		items, err := k.list(ctx, kubeDeployments, slot.namespace, slot.selector())
		if err != nil {
			return false, err
		}
		for _, item := range items {
			if slot.ownsDirectly(item) && item.DeletionTimestamp == nil {
				return true, nil
			}
		}
	}
	return false, nil
}

func (k *kubeRuntime) logs(ctx context.Context, ref PreviewRef, tail int) (string, error) {
	if err := validateKubeRef(ref); err != nil {
		return "", err
	}
	slots, _, err := k.locate(ctx, ref)
	if err != nil {
		return "", err
	}
	for _, slot := range slots {
		if !slot.matchesRevision(ref) {
			continue
		}
		body, err := k.slotLogs(ctx, slot, tail)
		if err != errKubeLogsNotFound {
			return body, err
		}
	}
	return "", errKubeLogsNotFound
}

func (k *kubeRuntime) slotLogs(ctx context.Context, slot *kubeSlot, tail int) (string, error) {
	pods, err := k.list(ctx, kubePods, slot.namespace, slot.selector())
	if err != nil {
		return "", err
	}

	// Prefer the exposed service; internal-only previews can still return logs.
	sort.Slice(pods, func(i, j int) bool {
		leftPublic := pods[i].Labels[kubePublicLabel] == "true"
		rightPublic := pods[j].Labels[kubePublicLabel] == "true"
		if leftPublic != rightPublic {
			return leftPublic
		}
		return pods[i].CreationTimestamp.After(pods[j].CreationTimestamp)
	})
	if tail <= 0 {
		tail = 200
	}
	tail = min(tail, 500)
	const maxLogBytes = 256 << 10
	for _, pod := range pods {
		service := pod.Labels[kubeAppLabel]
		if !slot.owns(pod) || !serviceNameRE.MatchString(service) || !validKubeDNSName(pod.Name) || pod.DeletionTimestamp != nil {
			continue
		}
		q := url.Values{
			"container": {service}, "tailLines": {strconv.Itoa(tail)}, "limitBytes": {strconv.Itoa(maxLogBytes)},
		}
		var body string
		err := k.client.request(ctx, http.MethodGet, kubePods.path(slot.namespace, pod.Name, "log"), q, "*/*", nil, &body, maxLogBytes)
		if kubeHTTPStatus(err, http.StatusNotFound) || kubeHTTPStatus(err, http.StatusBadRequest) {
			continue
		}
		if err != nil {
			return "", err
		}
		return scrubString(body), nil
	}
	return "", errKubeLogsNotFound
}

var errKubeLogsNotFound = errContainerNotFound

func (s *kubeSlot) matchesRevision(ref PreviewRef) bool {
	return ref.SHA == "" || s.claim.matches(ref, true)
}

func (s *kubeSlot) selector() string {
	return kubeClaimLabel + "=" + s.claim.Metadata.UID
}

func (s *kubeSlot) owns(obj kubeMeta) bool {
	return obj.Labels[kubeClaimLabel] == s.claim.Metadata.UID
}

func (s *kubeSlot) ownsDirectly(obj kubeMeta) bool {
	if !s.owns(obj) {
		return false
	}
	for _, owner := range obj.OwnerReferences {
		if owner.APIVersion == "v1" && owner.Kind == "ConfigMap" && owner.Name == kubeClaimName && owner.UID == s.claim.Metadata.UID {
			return true
		}
	}
	return false
}

func (s *kubeSlot) metadata(name string, labels map[string]string) kubeFields {
	return kubeFields{
		"name": name, "labels": labels,
		"ownerReferences": []kubeFields{{
			"apiVersion": "v1", "kind": "ConfigMap", "name": kubeClaimName, "uid": s.claim.Metadata.UID,
		}},
	}
}

func (s *kubeSlot) labels() map[string]string {
	return map[string]string{kubeClaimLabel: s.claim.Metadata.UID}
}

func (k *kubeRuntime) list(ctx context.Context, resource kubeResource, namespace, selector string) ([]kubeMeta, error) {
	var items []kubeMeta
	q := url.Values{"labelSelector": {selector}, "limit": {"50"}}
	path := resource.path(namespace)
	// Metadata-only reads avoid fetching Secret contents during cleanup.
	const metadataList = "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1"
	for range 20 {
		var page struct {
			Metadata struct {
				Continue string `json:"continue"`
			} `json:"metadata"`
			Items []struct {
				Metadata kubeMeta `json:"metadata"`
			} `json:"items"`
		}
		if err := k.client.request(ctx, http.MethodGet, path, q, metadataList, nil, &page, kubeMaxAPIResponse); err != nil {
			return nil, err
		}
		for _, item := range page.Items {
			items = append(items, item.Metadata)
		}
		if page.Metadata.Continue == "" {
			return items, nil
		}
		q.Set("continue", page.Metadata.Continue)
	}
	return nil, fmt.Errorf("kubernetes list %s exceeded page limit", path)
}

var kubeCleanupGroups = [][]kubeResource{
	{kubeIngresses, kubeJobs, kubeDeployments},
	{kubeServices, kubeSecrets},
}

func (k *kubeRuntime) checkSlotEmpty(ctx context.Context, namespace string) error {
	present, err := k.slotHasResources(ctx, namespace, kubeClaimLabel)
	if err == nil && present {
		return fmt.Errorf("kubernetes namespace %s still contains preview resources", namespace)
	}
	return err
}

func (k *kubeRuntime) slotHasResources(ctx context.Context, namespace, selector string) (bool, error) {
	for _, group := range kubeCleanupGroups {
		for _, resource := range group {
			items, err := k.list(ctx, resource, namespace, selector)
			if err != nil || len(items) > 0 {
				return len(items) > 0, err
			}
		}
	}
	pods, err := k.list(ctx, kubePods, namespace, selector)
	return len(pods) > 0, err
}

func (k *kubeRuntime) cleanSlot(ctx context.Context, slot *kubeSlot) error {
	ctx, cancel := context.WithTimeout(ctx, kubeStopTime)
	defer cancel()
	current, err := k.getClaim(ctx, slot.namespace)
	if kubeHTTPStatus(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !current.valid() || current.Metadata.UID != slot.claim.Metadata.UID ||
		current.Data["repo"] != slot.claim.Data["repo"] || current.Data["pr"] != slot.claim.Data["pr"] || current.Data["sha"] != slot.claim.Data["sha"] {
		return errors.New("kubernetes preview claim changed before cleanup")
	}
	for _, name := range kubePendingMarkers {
		if err := k.resolvePendingCreate(ctx, slot, name); err != nil {
			return err
		}
	}

	for i, group := range kubeCleanupGroups {
		for _, resource := range group {
			if err := k.deleteOwned(ctx, slot, resource); err != nil {
				return err
			}
		}
		for _, resource := range group {
			if err := k.waitGone(ctx, slot, resource); err != nil {
				return err
			}
		}
		if i == 0 {
			// Foreground deletion drains controllers and their children. Also
			// check pods before removing credentials or releasing the namespace.
			if err := k.waitGone(ctx, slot, kubePods); err != nil {
				return err
			}
		}
	}
	if err := k.checkSlotEmpty(ctx, slot.namespace); err != nil {
		return err
	}
	path := kubeConfigMaps.path(slot.namespace, kubeClaimName)
	if err := k.deleteObject(ctx, path, current.Metadata.UID); err != nil {
		return err
	}
	return waitKube(ctx, func() (bool, error) {
		claim, err := k.getClaim(ctx, slot.namespace)
		if kubeHTTPStatus(err, http.StatusNotFound) {
			return true, nil
		}
		return claim.Metadata.UID != current.Metadata.UID, err
	})
}

func (k *kubeRuntime) deleteOwned(ctx context.Context, slot *kubeSlot, resource kubeResource) error {
	items, err := k.list(ctx, resource, slot.namespace, slot.selector())
	if err != nil {
		return err
	}
	for _, item := range items {
		if !slot.ownsDirectly(item) || item.UID == "" || !validKubeDNSName(item.Name) {
			return errors.New("invalid kubernetes owned resource metadata")
		}
		if item.DeletionTimestamp == nil {
			if err := k.deleteObject(ctx, resource.path(slot.namespace, item.Name), item.UID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (k *kubeRuntime) waitGone(ctx context.Context, slot *kubeSlot, resource kubeResource) error {
	err := waitKube(ctx, func() (bool, error) {
		items, err := k.list(ctx, resource, slot.namespace, slot.selector())
		return len(items) == 0, err
	})
	if err != nil {
		return fmt.Errorf("wait for kubernetes %s cleanup: %w", resource.path(slot.namespace), err)
	}
	return nil
}

func (k *kubeRuntime) deleteObject(ctx context.Context, path, uid string) error {
	return k.deleteWithPropagation(ctx, path, uid, "Foreground")
}

func (k *kubeRuntime) deleteWithPropagation(ctx context.Context, path, uid, policy string) error {
	options := kubeFields{
		"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": policy,
		"preconditions": kubeFields{"uid": uid},
	}
	err := k.client.json(ctx, http.MethodDelete, path, options, nil)
	if kubeHTTPStatus(err, http.StatusNotFound) {
		return nil
	}
	return err
}

func waitKube(ctx context.Context, check func() (bool, error)) error {
	ticker := time.NewTicker(kubePoll)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if done, err := check(); done || err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
