package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	kubePendingName     = "hatch-pending"
	kubeSeedPendingName = "hatch-seed-pending"
)

var kubePendingMarkers = []string{kubePendingName, kubeSeedPendingName}

func (s *kubeSlot) pendingName(prefix string) string {
	return prefix + "." + s.claim.Metadata.UID
}

// Persist the intent before dispatching the resource create. A timed-out
// request can still complete after a retry or a coordinator restart.
func (k *kubeRuntime) createOwned(ctx context.Context, slot *kubeSlot, resource kubeResource, name string, object kubeFields, pendingName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Finish the bounded write sequence during a graceful shutdown once the
	// intent may exist. Hard termination can still leave a marker for review.
	writeCtx, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	defer cancelWrite()
	pendingName = slot.pendingName(pendingName)
	data := map[string]string{"resource": string(resource), "name": name}
	marker := kubeObject("v1", "ConfigMap", slot.metadata(pendingName, slot.labels()), "data", data)
	marker["immutable"] = true
	path := kubeConfigMaps.path(slot.namespace, pendingName)

	var pending kubeClaim
	if err := k.client.json(writeCtx, http.MethodPost, kubeConfigMaps.path(slot.namespace), marker, &pending); err != nil {
		return err
	}
	if !validKubePending(slot, pending, pendingName) || pending.Data["resource"] != string(resource) || pending.Data["name"] != name {
		return errors.New("could not verify the kubernetes pending create")
	}

	err := k.client.json(writeCtx, http.MethodPost, resource.path(slot.namespace), object, nil)
	var uncertain *kubeUncertainCreate
	if errors.As(err, &uncertain) {
		return err
	}

	// A definitive create result allows us to clear the marker, even when the
	// original context was canceled while accepting the response.
	clearCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	return errors.Join(err, k.deleteWithPropagation(clearCtx, path, pending.Metadata.UID, "Background"))
}

// A late claim from a different revision is eligible for cleanup only if
// its coordinator never started creating resources. The PR lock excludes an
// active deploy from changing that result before cleanup starts.
func (k *kubeRuntime) slotIsIdle(ctx context.Context, slot *kubeSlot) (bool, error) {
	for _, name := range kubePendingMarkers {
		err := k.client.json(ctx, http.MethodGet, kubeConfigMaps.path(slot.namespace, slot.pendingName(name)), nil, nil)
		if !kubeHTTPStatus(err, http.StatusNotFound) {
			return false, err
		}
	}
	present, err := k.slotHasResources(ctx, slot.namespace, slot.selector())
	return !present && err == nil, err
}

func validKubePending(slot *kubeSlot, marker kubeClaim, name string) bool {
	return marker.Metadata.Name == name && marker.Metadata.UID != "" && slot.ownsDirectly(marker.Metadata)
}

func (k *kubeRuntime) resolvePendingCreate(ctx context.Context, slot *kubeSlot, pendingName string) error {
	pendingName = slot.pendingName(pendingName)
	path := kubeConfigMaps.path(slot.namespace, pendingName)
	var pending kubeClaim
	err := k.client.json(ctx, http.MethodGet, path, nil, &pending)
	if kubeHTTPStatus(err, http.StatusNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	resource := kubeResource(pending.Data["resource"])
	name := pending.Data["name"]
	if !validKubePending(slot, pending, pendingName) || !validKubeDNSName(name) || !trackedKubeResource(resource) {
		return errors.New("invalid kubernetes pending create")
	}

	// Seeing the exact owned object proves that this POST has taken effect.
	// Absence does not prove rejection, so retain the claim if unresolved.
	const metadata = "application/json;as=PartialObjectMetadata;g=meta.k8s.io;v=v1"
	var object struct {
		Metadata kubeMeta `json:"metadata"`
	}
	objectPath := resource.path(slot.namespace, name)
	err = k.client.request(ctx, http.MethodGet, objectPath, nil, metadata, nil, &object, kubeMaxAPIResponse)
	if kubeHTTPStatus(err, http.StatusNotFound) ||
		(err == nil && (!slot.ownsDirectly(object.Metadata) || object.Metadata.Name != name || object.Metadata.UID == "")) {
		return fmt.Errorf("kubernetes namespace %s remains reserved while creation of %s is unresolved", slot.namespace, objectPath)
	}
	if err != nil {
		return err
	}
	return k.deleteWithPropagation(ctx, path, pending.Metadata.UID, "Background")
}

func trackedKubeResource(resource kubeResource) bool {
	for _, group := range kubeCleanupGroups {
		for _, candidate := range group {
			if candidate == resource {
				return true
			}
		}
	}
	return false
}
