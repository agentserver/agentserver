package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	PersistentVolumeClaims = schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}
	SandboxTemplates       = schema.GroupVersionResource{Group: "extensions.agents.x-k8s.io", Version: "v1beta1", Resource: "sandboxtemplates"}
	SandboxWarmPools       = schema.GroupVersionResource{Group: "extensions.agents.x-k8s.io", Version: "v1beta1", Resource: "sandboxwarmpools"}
	repositoryUUID         = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

const (
	repositoryPoolAnnotation  = "agentserver.dev/repository-pool"
	repositoryOwnerAnnotation = "agentserver.dev/repository-owner"
	repositoryStoragePath     = "/var/lib/agentserver/repositories"
)

type repositoryOwner struct {
	WorkspaceID   string `json:"workspaceId"`
	SessionID     string `json:"sessionId"`
	EnvironmentID string `json:"environmentId"`
	Region        string `json:"region"`
	Scope         string `json:"scope"`
}

func validateRepositoryStorageConfig(c Config) error {
	if c.RepositoryStorageClass == "" && c.RepositoryStorageSize == "" {
		return nil
	}
	if !dnsLabel(c.RepositoryStorageClass) {
		return errors.New("repository storage class must be explicitly configured")
	}
	size, err := resource.ParseQuantity(c.RepositoryStorageSize)
	if err != nil || size.Sign() <= 0 {
		return errors.New("repository storage size must be a positive Kubernetes quantity")
	}
	return nil
}

func (p *Provider) repositoryPoolName(session string) string {
	// This digest is a DNS naming suffix for the immutable template revision,
	// not a build artifact hash gate. Full ownership/spec equality is checked.
	revision := sha256.Sum256([]byte(p.config.Pool))
	return "as-repo-" + strings.ReplaceAll(session, "-", "") + "-" + hex.EncodeToString(revision[:5])
}

// ensureRepositoryPool uses an independent PVC. It must never be owned by a
// SandboxClaim/Sandbox, whose TTL deletion would erase session changes. RWOP
// fences concurrent Pods even on one node; there is deliberately no RWO/RWX
// fallback. Idle cleanup deletes claims only, retaining this PVC and its data.
func (p *Provider) ensureRepositoryPool(ctx context.Context, i createIdentity) (string, error) {
	for _, id := range []string{i.WorkspaceID, i.SessionID, i.EnvironmentID} {
		if !repositoryUUID.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
			return "", failure("invalid_repository_owner", false)
		}
	}
	owner := repositoryOwner{i.WorkspaceID, i.SessionID, i.EnvironmentID, i.Region, i.Scope}
	ownerJSON, _ := json.Marshal(owner)
	volumeName := "as-session-" + strings.ReplaceAll(i.SessionID, "-", "")
	poolName := p.repositoryPoolName(i.SessionID)
	object := func(api, kind, name string, spec map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": api, "kind": kind, "metadata": map[string]any{"name": name, "namespace": p.config.Namespace, "labels": map[string]any{managedLabel: managedValue}, "annotations": map[string]any{repositoryOwnerAnnotation: string(ownerJSON)}}, "spec": spec}}
	}
	// Read only a deployment-selected template, never a user-provided Pod spec.
	basePool, err := p.resource(SandboxWarmPools).Get(ctx, p.config.Pool, metav1.GetOptions{})
	if err != nil {
		return "", apiFailure(err, "repository_base_pool", true)
	}
	baseName, _, _ := unstructured.NestedString(basePool.Object, "spec", "sandboxTemplateRef", "name")
	if !dnsLabel(baseName) {
		return "", failure("repository_base_template_invalid", false)
	}
	base, err := p.resource(SandboxTemplates).Get(ctx, baseName, metav1.GetOptions{})
	if err != nil {
		return "", apiFailure(err, "repository_base_template", true)
	}
	spec, found, err := unstructured.NestedMap(base.Object, "spec")
	if err != nil || !found {
		return "", failure("repository_base_template_invalid", false)
	}
	if err := injectRepositoryVolume(spec, volumeName); err != nil {
		return "", err
	}
	volume := object("v1", "PersistentVolumeClaim", volumeName, map[string]any{
		"storageClassName": p.config.RepositoryStorageClass, "accessModes": []any{"ReadWriteOncePod"}, "volumeMode": "Filesystem",
		"resources": map[string]any{"requests": map[string]any{"storage": p.config.RepositoryStorageSize}},
	})
	actual, err := p.createRepositoryObject(ctx, PersistentVolumeClaims, volume)
	if err != nil {
		return "", err
	}
	if err := p.matchRepositoryOwner(actual, volume); err != nil {
		return "", err
	}
	if len(actual.GetOwnerReferences()) != 0 {
		return "", failure("repository_volume_has_gc_owner", false)
	}
	for _, field := range []string{"storageClassName", "accessModes", "volumeMode", "selector", "dataSource", "dataSourceRef"} {
		wanted, _, _ := unstructured.NestedFieldNoCopy(volume.Object, "spec", field)
		got, _, _ := unstructured.NestedFieldNoCopy(actual.Object, "spec", field)
		if !reflect.DeepEqual(wanted, got) {
			return "", failure("repository_volume_spec_conflict", false)
		}
	}
	storage, _, _ := unstructured.NestedString(actual.Object, "spec", "resources", "requests", "storage")
	quantity, err := resource.ParseQuantity(storage)
	wantQuantity, _ := resource.ParseQuantity(p.config.RepositoryStorageSize)
	if err != nil || quantity.Cmp(wantQuantity) != 0 {
		return "", failure("repository_volume_size_conflict", false)
	}
	// Pools/templates may be garbage-collected only when the persistent volume
	// is explicitly removed, never when an idle sandbox is reaped.
	controller := true
	owners := []metav1.OwnerReference{{APIVersion: "v1", Kind: "PersistentVolumeClaim", Name: actual.GetName(), UID: actual.GetUID(), Controller: &controller}}
	template := object(SandboxTemplates.GroupVersion().String(), "SandboxTemplate", poolName, spec)
	template.SetOwnerReferences(owners)
	pool := object(SandboxWarmPools.GroupVersion().String(), "SandboxWarmPool", poolName, map[string]any{"replicas": int64(0), "sandboxTemplateRef": map[string]any{"name": poolName}})
	pool.SetOwnerReferences(owners)
	for _, entry := range []struct {
		gvr schema.GroupVersionResource
		obj *unstructured.Unstructured
	}{{SandboxTemplates, template}, {SandboxWarmPools, pool}} {
		actual, err := p.createRepositoryObject(ctx, entry.gvr, entry.obj)
		if err != nil {
			return "", err
		}
		if err := p.matchRepositoryOwner(actual, entry.obj); err != nil {
			return "", err
		}
		if !reflect.DeepEqual(actual.GetOwnerReferences(), owners) || !reflect.DeepEqual(actual.Object["spec"], entry.obj.Object["spec"]) {
			return "", failure("repository_pool_spec_conflict", false)
		}
	}
	return poolName, nil
}

func (p *Provider) createRepositoryObject(ctx context.Context, gvr schema.GroupVersionResource, desired *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	obj, err := p.resource(gvr).Create(ctx, desired, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		obj, err = p.resource(gvr).Get(ctx, desired.GetName(), metav1.GetOptions{})
	}
	if err != nil {
		return nil, apiFailure(err, "repository_resource", true)
	}
	return obj, nil
}
func (p *Provider) matchRepositoryOwner(actual, desired *unstructured.Unstructured) error {
	if actual.GetUID() == "" || actual.GetDeletionTimestamp() != nil || actual.GetNamespace() != p.config.Namespace || actual.GetName() != desired.GetName() || actual.GetLabels()[managedLabel] != managedValue || actual.GetAnnotations()[repositoryOwnerAnnotation] != desired.GetAnnotations()[repositoryOwnerAnnotation] {
		return failure("repository_owner_conflict", false)
	}
	return nil
}

func injectRepositoryVolume(spec map[string]any, volumeName string) error {
	const volume = "repository-storage"
	volumes, _, err := unstructured.NestedSlice(spec, "podTemplate", "spec", "volumes")
	if err != nil {
		return failure("repository_base_volumes_invalid", false)
	}
	for _, item := range volumes {
		v, ok := item.(map[string]any)
		if !ok || v["name"] == volume {
			return failure("repository_base_volume_conflict", false)
		}
	}
	volumes = append(volumes, map[string]any{"name": volume, "persistentVolumeClaim": map[string]any{"claimName": volumeName}})
	if unstructured.SetNestedSlice(spec, volumes, "podTemplate", "spec", "volumes") != nil {
		return failure("repository_base_volumes_invalid", false)
	}
	containers, _, err := unstructured.NestedSlice(spec, "podTemplate", "spec", "containers")
	if err != nil {
		return failure("repository_base_runtime_invalid", false)
	}
	found := false
	for _, entry := range containers {
		container, ok := entry.(map[string]any)
		if !ok {
			return failure("repository_base_runtime_invalid", false)
		}
		if container["name"] != "runtime" {
			continue
		}
		if found {
			return failure("repository_base_runtime_invalid", false)
		}
		found = true
		mounts, _, _ := unstructured.NestedSlice(container, "volumeMounts")
		for _, item := range mounts {
			m, ok := item.(map[string]any)
			if !ok || m["name"] == volume || m["mountPath"] == repositoryStoragePath {
				return failure("repository_base_mount_conflict", false)
			}
		}
		mounts = append(mounts, map[string]any{"name": volume, "mountPath": repositoryStoragePath})
		container["volumeMounts"] = mounts
		env, _, _ := unstructured.NestedSlice(container, "env")
		for _, item := range env {
			v, ok := item.(map[string]any)
			if !ok || v["name"] == "AGENTSERVER_REPOSITORY_STORAGE" {
				return failure("repository_base_env_conflict", false)
			}
		}
		container["env"] = append(env, map[string]any{"name": "AGENTSERVER_REPOSITORY_STORAGE", "value": repositoryStoragePath})
	}
	if !found || unstructured.SetNestedSlice(spec, containers, "podTemplate", "spec", "containers") != nil {
		return failure("repository_base_runtime_invalid", false)
	}
	return nil
}
