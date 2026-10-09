package adapter

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/sandboxgateway"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ktesting "k8s.io/client-go/testing"
)

func repositoryFixture(t *testing.T) (*Provider, sandboxgateway.CreateSandboxRequest) {
	t.Helper()
	p, kube, _, r := fixture(t)
	p.config.RepositoryStorageClass = "longhorn"
	p.config.RepositoryStorageSize = "10Gi"
	r.WorkspaceID = "11111111-1111-4111-8111-111111111111"
	r.SessionID = "22222222-2222-4222-8222-222222222222"
	r.EnvironmentID = "33333333-3333-4333-8333-333333333333"
	kube.PrependReactor("create", "*", func(action ktesting.Action) (bool, runtime.Object, error) {
		x := action.(ktesting.CreateAction).GetObject().(*unstructured.Unstructured)
		if x.GetUID() == "" {
			x.SetUID(types.UID(x.GetName() + "-uid"))
		}
		return false, nil, nil
	})
	pool := &unstructured.Unstructured{Object: map[string]any{"apiVersion": SandboxWarmPools.GroupVersion().String(), "kind": "SandboxWarmPool", "metadata": map[string]any{"name": p.config.Pool, "namespace": p.config.Namespace}, "spec": map[string]any{"replicas": int64(0), "sandboxTemplateRef": map[string]any{"name": "base-template"}}}}
	template := &unstructured.Unstructured{Object: map[string]any{"apiVersion": SandboxTemplates.GroupVersion().String(), "kind": "SandboxTemplate", "metadata": map[string]any{"name": "base-template", "namespace": p.config.Namespace}, "spec": map[string]any{"service": true, "networkPolicyManagement": "Unmanaged", "podTemplate": map[string]any{"spec": map[string]any{
		"automountServiceAccountToken": false, "securityContext": map[string]any{"runAsNonRoot": true},
		"volumes":    []any{map[string]any{"name": "workspace", "emptyDir": map[string]any{}}},
		"containers": []any{map[string]any{"name": "runtime", "image": "qualified-runtime", "volumeMounts": []any{map[string]any{"name": "workspace", "mountPath": "/workspace"}}}},
	}}}}}
	if _, err := p.resource(SandboxWarmPools).Create(t.Context(), pool, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.resource(SandboxTemplates).Create(t.Context(), template, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	return p, r
}

func TestRepositoryStorageSurvivesClaimRecreation(t *testing.T) {
	p, r := repositoryFixture(t)
	first, err := p.CreateSandbox(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := p.load(t.Context(), first.SessionRef)
	if err != nil {
		t.Fatal(err)
	}
	poolName := p.repositoryPoolName(r.SessionID)
	if claim.GetAnnotations()[repositoryPoolAnnotation] != poolName {
		t.Fatal("claim did not select isolated pool")
	}
	volumeName := "as-session-" + strings.ReplaceAll(r.SessionID, "-", "")
	volume, err := p.resource(PersistentVolumeClaims).Get(t.Context(), volumeName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(volume.GetOwnerReferences()) != 0 {
		t.Fatal("PVC follows sandbox garbage collection")
	}
	modes, _, _ := unstructured.NestedStringSlice(volume.Object, "spec", "accessModes")
	if !reflect.DeepEqual(modes, []string{"ReadWriteOncePod"}) {
		t.Fatal("PVC permits concurrent pod attachment")
	}
	template, err := p.resource(SandboxTemplates).Get(t.Context(), poolName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(template.Object)
	if !strings.Contains(string(encoded), repositoryStoragePath) || !strings.Contains(string(encoded), "qualified-runtime") || strings.Contains(string(encoded), "privileged") {
		t.Fatal("derived runtime projection changed")
	}
	base, err := p.resource(SandboxTemplates).Get(t.Context(), "base-template", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	baseJSON, _ := json.Marshal(base.Object)
	if strings.Contains(string(baseJSON), repositoryStoragePath) {
		t.Fatal("modified deployment template")
	}
	if len(template.GetOwnerReferences()) != 1 || template.GetOwnerReferences()[0].UID != volume.GetUID() {
		t.Fatal("template must follow persistent PVC, not sandbox")
	}
	// Simulate TTL claim reclamation. A replacement identity must reuse exactly
	// the old PVC, while the provider still fences the old claim UID separately.
	if err := p.resource(Claims).Delete(t.Context(), claim.GetName(), metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	r.Generation++
	r.SandboxID = "f68448a6-9ca8-4d2a-b5f3-86a07f7b7524"
	r.IdempotencyKey = "replacement"
	if _, err := p.CreateSandbox(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	after, err := p.resource(PersistentVolumeClaims).Get(t.Context(), volumeName, metav1.GetOptions{})
	if err != nil || after.GetUID() != volume.GetUID() {
		t.Fatal("replaced persistent session volume")
	}
	foreign := r
	foreign.WorkspaceID = "44444444-4444-4444-8444-444444444444"
	foreign.SandboxID = "f78448a6-9ca8-4d2a-b5f3-86a07f7b7524"
	if _, err := p.CreateSandbox(t.Context(), foreign); err == nil {
		t.Fatal("adopted another workspace's volume")
	}
}

func TestRepositoryStorageRejectsUnsafeReuse(t *testing.T) {
	for _, change := range []string{"owner", "access-mode", "size", "template", "foreign-pool"} {
		t.Run(change, func(t *testing.T) {
			p, r := repositoryFixture(t)
			if _, err := p.CreateSandbox(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			name := "as-session-" + strings.ReplaceAll(r.SessionID, "-", "")
			v, _ := p.resource(PersistentVolumeClaims).Get(t.Context(), name, metav1.GetOptions{})
			switch change {
			case "owner":
				v.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "v1", Kind: "Pod", Name: "doomed", UID: "doomed-uid"}})
			case "access-mode":
				unstructured.SetNestedStringSlice(v.Object, []string{"ReadWriteMany"}, "spec", "accessModes")
			case "size":
				unstructured.SetNestedField(v.Object, "1Gi", "spec", "resources", "requests", "storage")
			case "template":
				x, _ := p.resource(SandboxTemplates).Get(t.Context(), p.repositoryPoolName(r.SessionID), metav1.GetOptions{})
				unstructured.SetNestedField(x.Object, "attacker", "spec", "podTemplate", "spec", "serviceAccountName")
				p.resource(SandboxTemplates).Update(t.Context(), x, metav1.UpdateOptions{})
			case "foreign-pool":
				x, _ := p.resource(SandboxWarmPools).Get(t.Context(), p.repositoryPoolName(r.SessionID), metav1.GetOptions{})
				x.SetAnnotations(map[string]string{repositoryOwnerAnnotation: "{}"})
				p.resource(SandboxWarmPools).Update(t.Context(), x, metav1.UpdateOptions{})
			}
			p.resource(PersistentVolumeClaims).Update(t.Context(), v, metav1.UpdateOptions{})
			r.SandboxID = "f88448a6-9ca8-4d2a-b5f3-86a07f7b7524"
			if _, err := p.CreateSandbox(t.Context(), r); err == nil {
				t.Fatal("unsafe persistent storage reuse accepted")
			}
		})
	}
}
