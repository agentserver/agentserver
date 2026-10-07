package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/k8sruntime"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
)

type runtimeRecorder struct {
	calls    int
	endpoint Endpoint
	request  executionbackend.StartProcessRequest
	boot     string
}

func (r *runtimeRecorder) Identity(context.Context, Endpoint) (string, error) {
	if r.boot != "" {
		return r.boot, nil
	}
	return strings.Repeat("a", 64), nil
}
func (r *runtimeRecorder) Bind(context.Context, Endpoint, k8sruntime.Binding) error { return nil }

func (r *runtimeRecorder) StartProcess(_ context.Context, e Endpoint, req executionbackend.StartProcessRequest) (executionbackend.Exchange, error) {
	r.calls++
	r.endpoint = e
	r.request = req
	return nil, nil
}
func (r *runtimeRecorder) SignalProcess(context.Context, Endpoint, executionbackend.SignalProcessRequest) (executionbackend.Exchange, error) {
	r.calls++
	return nil, nil
}
func (r *runtimeRecorder) ReadFile(context.Context, Endpoint, executionbackend.ReadFileRequest) (executionbackend.Exchange, error) {
	r.calls++
	return nil, nil
}

var testNow = time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)

func fixture(t *testing.T) (*Provider, *dynamicfake.FakeDynamicClient, *runtimeRecorder, sandboxgateway.CreateSandboxRequest) {
	t.Helper()
	kube := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	kube.PrependReactor("create", "sandboxclaims", func(action ktesting.Action) (bool, runtime.Object, error) {
		x := action.(ktesting.CreateAction).GetObject().(*unstructured.Unstructured)
		x.SetUID(types.UID("claim-uid"))
		x.SetResourceVersion("1")
		x.SetGeneration(1)
		return false, nil, nil
	})
	recorder := &runtimeRecorder{}
	p, err := New(kube, recorder, Config{Namespace: "agentserver-sandboxes", Pool: "managed-cli-v1", Region: "sg", Scope: "sg-managed-cli", RuntimePort: 8443, Now: func() time.Time { return testNow }})
	if err != nil {
		t.Fatal(err)
	}
	r := sandboxgateway.CreateSandboxRequest{Generation: 1, SandboxID: "f58448a6-9ca8-4d2a-b5f3-86a07f7b7524", IdempotencyKey: "create-key", WorkspaceID: "workspace-1", SessionID: "session-1", EnvironmentID: "environment-1", Region: "sg", PSM: "sg-managed-cli", TTL: 30 * time.Minute}
	return p, kube, recorder, r
}
func findRequest(r sandboxgateway.CreateSandboxRequest) sandboxgateway.FindSandboxRequest {
	return sandboxgateway.FindSandboxRequest{Generation: r.Generation, SandboxID: r.SandboxID, IdempotencyKey: r.IdempotencyKey, WorkspaceID: r.WorkspaceID, SessionID: r.SessionID, EnvironmentID: r.EnvironmentID, Region: r.Region, PSM: r.PSM}
}

func TestCreateIsIdentityBoundAndIdempotent(t *testing.T) {
	p, kube, _, r := fixture(t)
	sb, err := p.CreateSandbox(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	if sb.State != sandboxgateway.ProviderSandboxCreating || sb.ExecutionReady || sb.SessionRef != "agentserver-sandboxes/as-"+r.SandboxID+"/claim-uid" {
		t.Fatalf("unexpected state: %+v", sb)
	}
	again, err := p.CreateSandbox(t.Context(), r)
	if err != nil || again != sb {
		t.Fatalf("duplicate create: %+v %v", again, err)
	}
	claim, err := p.resource(Claims).Get(t.Context(), "as-"+r.SandboxID, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _, _ := unstructured.NestedMap(claim.Object, "spec")
	if len(spec) != 2 || spec["warmPoolRef"] == nil || spec["lifecycle"] == nil {
		t.Fatalf("unexpected claim fields: %v", spec)
	}
	if strings.Contains(fmt.Sprint(claim.Object), "SECRET") {
		t.Fatal("credentials in claim")
	}
	r.WorkspaceID = "other-workspace"
	if _, err := p.CreateSandbox(t.Context(), r); err == nil {
		t.Fatal("accepted collision with other workspace")
	}
	if _, err := p.FindSandbox(t.Context(), findRequest(r)); err == nil {
		t.Fatal("adopted foreign workspace")
	}
	for _, a := range kube.Actions() {
		if a.GetResource().Resource == "pods" && a.GetVerb() != "get" {
			t.Fatalf("unexpected Pod mutation: %v", a)
		}
	}
}

func controllerObjects(t *testing.T, p *Provider, r sandboxgateway.CreateSandboxRequest) (*unstructured.Unstructured, *unstructured.Unstructured) {
	t.Helper()
	claim, _ := p.resource(Claims).Get(t.Context(), "as-"+r.SandboxID, metav1.GetOptions{})
	sb := &unstructured.Unstructured{Object: map[string]any{"apiVersion": Sandboxes.GroupVersion().String(), "kind": "Sandbox", "metadata": map[string]any{"name": "sb-test", "namespace": p.config.Namespace}, "status": map[string]any{"service": "sb-test", "conditions": []any{map[string]any{"type": "Ready", "status": "True", "observedGeneration": int64(1)}}}}}
	sb.SetUID("sandbox-uid")
	sb.SetGeneration(1)
	setOwner(sb, claim, "SandboxClaim", Claims.GroupVersion().String())
	pod := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "sb-test", "namespace": p.config.Namespace}, "status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "containerStatuses": []any{map[string]any{"name": "runtime", "ready": true, "containerID": "containerd://runtime-1"}}}}}
	pod.SetUID("pod-uid")
	pod.SetLabels(map[string]string{"agents.x-k8s.io/sandbox-name-hash": "test-selector"})
	setOwner(pod, sb, "Sandbox", Sandboxes.GroupVersion().String())
	svc := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": "sb-test", "namespace": p.config.Namespace}, "spec": map[string]any{"clusterIP": "None", "selector": map[string]any{"agents.x-k8s.io/sandbox-name-hash": "test-selector"}}}}
	setOwner(svc, sb, "Sandbox", Sandboxes.GroupVersion().String())
	for _, entry := range []struct {
		kind string
		obj  *unstructured.Unstructured
	}{{"sandbox", sb}, {"pod", pod}, {"svc", svc}} {
		gvr := Sandboxes
		if entry.kind == "pod" {
			gvr = Pods
		}
		if entry.kind == "svc" {
			gvr = Services
		}
		if _, err := p.resource(gvr).Create(t.Context(), entry.obj, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	unstructured.SetNestedField(claim.Object, "sb-test", "status", "sandbox", "name")
	// Deliberately malicious status addresses must never be used for routing.
	unstructured.SetNestedField(claim.Object, "metadata.invalid", "status", "sandbox", "serviceFQDN")
	unstructured.SetNestedSlice(claim.Object, []any{"169.254.169.254"}, "status", "sandbox", "podIPs")
	if _, err := p.resource(Claims).Update(t.Context(), claim, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	return sb, pod
}
func setOwner(obj, owner *unstructured.Unstructured, kind, version string) {
	yes := true
	obj.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: version, Kind: kind, Name: owner.GetName(), UID: owner.GetUID(), Controller: &yes}})
}

func processRequest(r sandboxgateway.CreateSandboxRequest) executionbackend.StartProcessRequest {
	return executionbackend.StartProcessRequest{
		Target:    executionbackend.Target{Kind: executionbackend.KindKubernetes, ID: r.SandboxID, Generation: 1, EnvironmentID: r.EnvironmentID},
		Operation: executionbackend.OperationContext{WorkspaceID: r.WorkspaceID, SessionID: r.SessionID, RunID: "run-1", RunAttemptID: "attempt-1", RunAttemptGeneration: 1, ExecutionID: "execution-1", OperationID: "operation-1", MutationKey: "mutation-1"},
		RequestID: "request-1", ProcessID: "process-1", Executable: "bkectl", Arguments: []string{"version"}, WorkingDirectory: "/workspace", WorkspaceRoot: "/workspace", Platform: "linux-amd64", Timeout: time.Minute, OutputLimitBytes: 1024,
	}
}

func TestReadyRequiresOwnedResourcesAndPinsIncarnation(t *testing.T) {
	p, _, recorder, r := fixture(t)
	sb, err := p.CreateSandbox(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	controllerObjects(t, p, r)
	state, err := p.GetSandbox(t.Context(), sb.SessionRef)
	if err != nil || !state.ExecutionReady {
		t.Fatalf("ready: %+v %v", state, err)
	}
	req := processRequest(r)
	_, err = p.StartProcess(t.Context(), sandboxgateway.StartProcessProviderRequest{SessionRef: sb.SessionRef, Request: req})
	if err != nil {
		t.Fatal(err)
	}
	if recorder.calls != 1 || recorder.endpoint.Origin != "https://sb-test.agentserver-sandboxes.svc.cluster.local:8443" || recorder.endpoint.PodUID != "pod-uid" {
		t.Fatalf("dispatch: %+v", recorder)
	}
	claim, _ := p.load(t.Context(), sb.SessionRef)
	var pin incarnation
	if json.Unmarshal([]byte(claim.GetAnnotations()[incarnationAnnotation]), &pin) != nil || pin.ContainerID != "containerd://runtime-1" {
		t.Fatal("incarnation was not persisted")
	}
	// Even the same Pod UID with a restarted container is a different process
	// generation. A replacement cannot receive an old operation.
	pod, _ := p.resource(Pods).Get(t.Context(), "sb-test", metav1.GetOptions{})
	unstructured.SetNestedSlice(pod.Object, []any{map[string]any{"name": "runtime", "ready": true, "containerID": "containerd://runtime-2"}}, "status", "containerStatuses")
	p.resource(Pods).Update(t.Context(), pod, metav1.UpdateOptions{})
	restarted, err := New(p.kube, recorder, p.config)
	if err != nil {
		t.Fatal(err)
	}
	state, err = restarted.GetSandbox(t.Context(), sb.SessionRef)
	if err != nil || state.State != sandboxgateway.ProviderSandboxFailed {
		t.Fatalf("restart observation: %+v %v", state, err)
	}
	_, err = restarted.StartProcess(t.Context(), sandboxgateway.StartProcessProviderRequest{SessionRef: sb.SessionRef, Request: req})
	if !executionbackend.ProvesNotSent(err) || recorder.calls != 1 {
		t.Fatalf("replayed operation after restart: %v calls=%d", err, recorder.calls)
	}
}

func TestDispatchRejectsCrossSessionAndBackend(t *testing.T) {
	for _, field := range []string{"workspace", "session", "environment", "target", "kind"} {
		t.Run(field, func(t *testing.T) {
			p, _, recorder, r := fixture(t)
			sb, _ := p.CreateSandbox(t.Context(), r)
			controllerObjects(t, p, r)
			req := processRequest(r)
			switch field {
			case "workspace":
				req.Operation.WorkspaceID = "other"
			case "session":
				req.Operation.SessionID = "other"
			case "environment":
				req.Target.EnvironmentID = "other"
			case "target":
				req.Target.ID = "other"
			case "kind":
				req.Target.Kind = executionbackend.KindTAE
			}
			_, err := p.StartProcess(t.Context(), sandboxgateway.StartProcessProviderRequest{SessionRef: sb.SessionRef, Request: req})
			if !executionbackend.ProvesNotSent(err) || recorder.calls != 0 {
				t.Fatalf("dispatched invalid %s: %v", field, err)
			}
		})
	}
}

func TestReadinessDoesNotTrustStatusOrForeignOwners(t *testing.T) {
	for _, which := range []string{"sandbox-owner", "pod-owner", "service-owner", "service-selector", "stale-condition"} {
		t.Run(which, func(t *testing.T) {
			p, _, _, r := fixture(t)
			sb, _ := p.CreateSandbox(t.Context(), r)
			controllerObjects(t, p, r)
			gvr := Sandboxes
			if which == "pod-owner" {
				gvr = Pods
			}
			if strings.HasPrefix(which, "service-") {
				gvr = Services
			}
			obj, _ := p.resource(gvr).Get(t.Context(), "sb-test", metav1.GetOptions{})
			switch which {
			case "stale-condition":
				obj.SetGeneration(2)
			case "service-selector":
				unstructured.SetNestedStringMap(obj.Object, map[string]string{"app": "foreign"}, "spec", "selector")
			default:
				obj.SetOwnerReferences(nil)
			}
			p.resource(gvr).Update(t.Context(), obj, metav1.UpdateOptions{})
			state, err := p.GetSandbox(t.Context(), sb.SessionRef)
			if err == nil && state.ExecutionReady {
				t.Fatalf("accepted %s", which)
			}
		})
	}
}

func TestRenewUsesCurrentVersionAndNeverResurrectsExpiredClaims(t *testing.T) {
	p, kube, _, r := fixture(t)
	sb, _ := p.CreateSandbox(t.Context(), r)
	renewed, err := p.SetSandboxTimeout(t.Context(), sandboxgateway.SetSandboxTimeoutProviderRequest{SessionRef: sb.SessionRef, TTL: time.Hour})
	if err != nil || !renewed.ExpiresAt.Equal(testNow.Add(time.Hour)) {
		t.Fatalf("renew: %+v %v", renewed, err)
	}
	shorter, err := p.SetSandboxTimeout(t.Context(), sandboxgateway.SetSandboxTimeoutProviderRequest{SessionRef: sb.SessionRef, TTL: 30 * time.Second})
	if err != nil || shorter.ExpiresAt != renewed.ExpiresAt {
		t.Fatal("renew shortened TTL")
	}
	kube.PrependReactor("update", "sandboxclaims", func(action ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(Claims.GroupResource(), "claim", errors.New("conflict"))
	})
	if _, err := p.SetSandboxTimeout(t.Context(), sandboxgateway.SetSandboxTimeoutProviderRequest{SessionRef: sb.SessionRef, TTL: time.Hour}); err == nil {
		t.Fatal("swallowed resourceVersion conflict")
	}
	p.config.Now = func() time.Time { return testNow.Add(2 * time.Hour) }
	if _, err := p.SetSandboxTimeout(t.Context(), sandboxgateway.SetSandboxTimeoutProviderRequest{SessionRef: sb.SessionRef, TTL: time.Hour}); err == nil {
		t.Fatal("resurrected expired claim")
	}
}

func TestDeleteIsUIDScopedAndSupportsLostCreateResponse(t *testing.T) {
	p, kube, _, r := fixture(t)
	sb, _ := p.CreateSandbox(t.Context(), r)
	if err := p.DeleteSandbox(t.Context(), sandboxgateway.DeleteSandboxProviderRequest{Identity: findRequest(r)}); err != nil {
		t.Fatal(err)
	}
	var deletion ktesting.DeleteAction
	for _, a := range kube.Actions() {
		if a.GetVerb() == "delete" {
			deletion = a.(ktesting.DeleteAction)
		}
	}
	if deletion == nil {
		t.Fatal("delete not sent")
	}
	opts := deletion.GetDeleteOptions()
	if opts.Preconditions == nil || *opts.Preconditions.UID != "claim-uid" || *opts.Preconditions.ResourceVersion != "1" || *opts.PropagationPolicy != metav1.DeletePropagationForeground {
		t.Fatalf("unsafe deletion: %+v", opts)
	}
	if err := p.DeleteSandbox(t.Context(), sandboxgateway.DeleteSandboxProviderRequest{SessionRef: sb.SessionRef, Identity: findRequest(r)}); err != nil {
		t.Fatal(err)
	}
}

func TestReplacedClaimCannotBeDeletedOrAdopted(t *testing.T) {
	p, _, _, r := fixture(t)
	sb, _ := p.CreateSandbox(t.Context(), r)
	claim, _ := p.load(t.Context(), sb.SessionRef)
	claim.SetUID("replacement-uid")
	p.resource(Claims).Update(t.Context(), claim, metav1.UpdateOptions{})
	if _, err := p.GetSandbox(t.Context(), sb.SessionRef); err == nil {
		t.Fatal("adopted replacement")
	}
	if err := p.DeleteSandbox(t.Context(), sandboxgateway.DeleteSandboxProviderRequest{SessionRef: sb.SessionRef, Identity: findRequest(r)}); err == nil {
		t.Fatal("deleted replacement")
	}
	if _, err := p.resource(Claims).Get(t.Context(), claim.GetName(), metav1.GetOptions{}); err != nil {
		t.Fatal("replacement was removed")
	}
}

func TestCreateTimeoutIsAmbiguousAndDoesNotLeakResponse(t *testing.T) {
	p, kube, _, r := fixture(t)
	kube.PrependReactor("create", "sandboxclaims", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("transport timeout SECRET")
	})
	_, err := p.CreateSandbox(t.Context(), r)
	var pe *sandboxgateway.ProviderError
	if !errors.As(err, &pe) || !pe.Ambiguous || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("unsafe classification: %v", err)
	}
}
