// Package adapter implements Agent Sandbox lifecycle using namespaced CRDs.
// Kubernetes credentials never cross this boundary into a sandbox or model.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/k8sruntime"
	"github.com/agentserver/agentserver/v2/internal/repositorycheckout"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway"
	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
)

var (
	Claims    = schema.GroupVersionResource{Group: "extensions.agents.x-k8s.io", Version: "v1beta1", Resource: "sandboxclaims"}
	Sandboxes = schema.GroupVersionResource{Group: "agents.x-k8s.io", Version: "v1beta1", Resource: "sandboxes"}
	Pods      = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	Services  = schema.GroupVersionResource{Version: "v1", Resource: "services"}
)

const (
	managedLabel          = "agentserver.dev/managed-by"
	managedValue          = "k8s-sandbox-gateway"
	identityAnnotation    = "agentserver.dev/create-identity"
	incarnationAnnotation = "agentserver.dev/runtime-incarnation"
	maxTTL                = 24 * time.Hour
)

// Config contains deployment-owned choices, never arguments from a tool call.
// The pool points at an immutable template revision. RuntimeClass belongs in
// that template; an omitted runtimeClassName uses the cluster default.
type Config struct {
	Namespace              string
	Pool                   string
	Region                 string
	Scope                  string
	ClusterDomain          string
	RuntimePort            int
	Now                    func() time.Time
	RepositoryStorageClass string
	RepositoryStorageSize  string
}

// Endpoint is resolved from authoritative CR/Pod ownership, not caller URLs or
// a status-provided address. The runtime must verify all incarnation fields.
type Endpoint struct {
	Origin      string
	ClaimUID    string
	SandboxUID  string
	PodUID      string
	ContainerID string
	BootID      string
}

// RuntimeClient implements the authenticated process/files protocol. It must
// not automatically retry process start. The Kubernetes API is lifecycle-only;
// this provider intentionally has no pods/exec or pods/portforward path.
type RuntimeClient interface {
	Identity(context.Context, Endpoint) (string, error)
	Bind(context.Context, Endpoint, k8sruntime.Binding) error
	StartProcess(context.Context, Endpoint, executionbackend.StartProcessRequest) (executionbackend.Exchange, error)
	SignalProcess(context.Context, Endpoint, executionbackend.SignalProcessRequest) (executionbackend.Exchange, error)
	ReadFile(context.Context, Endpoint, executionbackend.ReadFileRequest) (executionbackend.Exchange, error)
}
type RepositoryRuntimeClient interface {
	PrepareRepository(context.Context, Endpoint, k8sruntime.PrepareRepositoryRequest) (k8sruntime.RepositoryState, error)
}

type Provider struct {
	kube    dynamic.Interface
	runtime RuntimeClient
	config  Config
}

type createIdentity struct {
	Generation     int64  `json:"generation"`
	SandboxID      string `json:"sandboxId"`
	IdempotencyKey string `json:"idempotencyKey"`
	WorkspaceID    string `json:"workspaceId"`
	SessionID      string `json:"sessionId"`
	EnvironmentID  string `json:"environmentId"`
	Region         string `json:"region"`
	Scope          string `json:"scope"`
}

type incarnation struct {
	SandboxUID  string `json:"sandboxUid"`
	PodUID      string `json:"podUid"`
	ContainerID string `json:"containerId"`
	BootID      string `json:"bootId"`
}

func New(kube dynamic.Interface, runtime RuntimeClient, config Config) (*Provider, error) {
	if kube == nil || runtime == nil {
		return nil, errors.New("Kubernetes client and runtime client are required")
	}
	if !dnsLabel(config.Namespace) || !dnsLabel(config.Pool) || !supportedRegion(config.Region) || !dnsLabel(config.Scope) {
		return nil, errors.New("Kubernetes provider requires a namespace, immutable pool, supported region (cn or sg) and scope")
	}
	if config.ClusterDomain == "" {
		config.ClusterDomain = "cluster.local"
	}
	if len(validation.IsDNS1123Subdomain(config.ClusterDomain)) != 0 || config.RuntimePort < 1 || config.RuntimePort > 65535 {
		return nil, errors.New("Kubernetes runtime domain or port is invalid")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if err := validateRepositoryStorageConfig(config); err != nil {
		return nil, err
	}
	return &Provider{kube: kube, runtime: runtime, config: config}, nil
}

func supportedRegion(region string) bool { return region == "cn" || region == "sg" }

func dnsLabel(s string) bool { return s != "" && len(validation.IsDNS1123Label(s)) == 0 }

func (p *Provider) resource(gvr schema.GroupVersionResource) dynamic.ResourceInterface {
	return p.kube.Resource(gvr).Namespace(p.config.Namespace)
}

func (p *Provider) identity(r sandboxgateway.FindSandboxRequest) (createIdentity, error) {
	i := createIdentity{r.Generation, r.SandboxID, r.IdempotencyKey, r.WorkspaceID, r.SessionID, r.EnvironmentID, r.Region, r.PSM}
	if i.Generation < 1 || !dnsLabel("as-"+i.SandboxID) || i.Region != p.config.Region || i.Scope != p.config.Scope {
		return i, failure("invalid_create_identity", false)
	}
	for _, value := range []string{i.IdempotencyKey, i.WorkspaceID, i.SessionID, i.EnvironmentID} {
		if value == "" || len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") {
			return i, failure("invalid_create_identity", false)
		}
	}
	return i, nil
}

func ttlValid(ttl time.Duration) bool {
	return ttl >= 30*time.Second && ttl <= maxTTL && ttl%time.Second == 0
}

func (p *Provider) CreateSandbox(ctx context.Context, r sandboxgateway.CreateSandboxRequest) (sandboxgateway.ProviderSandbox, error) {
	i, err := p.identity(sandboxgateway.FindSandboxRequest{Generation: r.Generation, SandboxID: r.SandboxID, IdempotencyKey: r.IdempotencyKey,
		WorkspaceID: r.WorkspaceID, SessionID: r.SessionID, EnvironmentID: r.EnvironmentID, Region: r.Region, PSM: r.PSM})
	if err != nil {
		return sandboxgateway.ProviderSandbox{}, err
	}
	if r.SessionRef != "" || !ttlValid(r.TTL) {
		return sandboxgateway.ProviderSandbox{}, failure("invalid_create_request", false)
	}
	// Resolve a pre-existing identity before provisioning durable resources.
	// Repeated create must not allocate another session's storage on a claim
	// name collision, nor require a template read to observe an existing claim.
	if existing, lookupErr := p.resource(Claims).Get(ctx, "as-"+i.SandboxID, metav1.GetOptions{}); lookupErr == nil {
		if err := p.matchIdentity(existing, i); err != nil {
			return sandboxgateway.ProviderSandbox{}, err
		}
		return p.observe(ctx, existing)
	} else if !apierrors.IsNotFound(lookupErr) {
		return sandboxgateway.ProviderSandbox{}, apiFailure(lookupErr, "create_lookup", true)
	}
	encoded, _ := json.Marshal(i)
	pool := p.config.Pool
	if p.config.RepositoryStorageClass != "" {
		pool, err = p.ensureRepositoryPool(ctx, i)
		if err != nil {
			return sandboxgateway.ProviderSandbox{}, err
		}
	}
	claim := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": Claims.GroupVersion().String(), "kind": "SandboxClaim",
		"metadata": map[string]any{"name": "as-" + i.SandboxID, "namespace": p.config.Namespace,
			"labels": map[string]any{managedLabel: managedValue}, "annotations": map[string]any{identityAnnotation: string(encoded)}},
		"spec": map[string]any{"warmPoolRef": map[string]any{"name": pool},
			"lifecycle": map[string]any{"shutdownTime": p.config.Now().UTC().Add(r.TTL).Format(time.RFC3339), "shutdownPolicy": "DeleteForeground"}},
	}}
	if pool != p.config.Pool {
		annotations := claim.GetAnnotations()
		annotations[repositoryPoolAnnotation] = pool
		claim.SetAnnotations(annotations)
	}
	created, err := p.resource(Claims).Create(ctx, claim, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		created, err = p.resource(Claims).Get(ctx, claim.GetName(), metav1.GetOptions{})
		if err != nil {
			return sandboxgateway.ProviderSandbox{}, apiFailure(err, "create_lookup", true)
		}
		if err := p.matchIdentity(created, i); err != nil {
			return sandboxgateway.ProviderSandbox{}, err
		}
	} else if err != nil {
		return sandboxgateway.ProviderSandbox{}, apiFailure(err, "create", true)
	}
	if err := p.matchIdentity(created, i); err != nil {
		return sandboxgateway.ProviderSandbox{}, err
	}
	return p.observe(ctx, created)
}

func (p *Provider) FindSandbox(ctx context.Context, r sandboxgateway.FindSandboxRequest) (sandboxgateway.ProviderSandbox, error) {
	i, err := p.identity(r)
	if err != nil {
		return sandboxgateway.ProviderSandbox{}, err
	}
	claim, err := p.resource(Claims).Get(ctx, "as-"+i.SandboxID, metav1.GetOptions{})
	if err != nil {
		return sandboxgateway.ProviderSandbox{}, apiFailure(err, "find", false)
	}
	if err := p.matchIdentity(claim, i); err != nil {
		return sandboxgateway.ProviderSandbox{}, err
	}
	return p.observe(ctx, claim)
}

func (p *Provider) PrepareRepository(ctx context.Context, request sandboxgateway.PrepareRepositoryProviderRequest) (sandboxcontract.PrepareRepositoryResponse, error) {
	runtime, ok := p.runtime.(RepositoryRuntimeClient)
	if !ok {
		return sandboxcontract.PrepareRepositoryResponse{}, errors.New("runtime repository preparation is unavailable")
	}
	claim, err := p.load(ctx, request.SessionRef)
	if err != nil {
		return sandboxcontract.PrepareRepositoryResponse{}, err
	}
	endpoint, ready, err := p.endpoint(ctx, claim)
	if err != nil || !ready {
		if err != nil {
			return sandboxcontract.PrepareRepositoryResponse{}, err
		}
		return sandboxcontract.PrepareRepositoryResponse{}, errors.New("sandbox runtime is not ready")
	}
	input := k8sruntime.PrepareRepositoryRequest{Session: request.Request.Session, Ref: request.Request.Ref, CheckoutID: request.Request.CheckoutID, Source: workspacerepository.Source{URL: request.Request.Source.URL, Ref: request.Request.Source.Ref, WorkingDirectory: request.Request.Source.WorkingDirectory, CredentialBindingID: request.Request.Source.CredentialBindingID}}
	if request.Request.Credential != nil {
		input.Credential = &repositorycheckout.Credential{Username: request.Request.Credential.Username, Token: request.Request.Credential.Token}
	}
	state, err := runtime.PrepareRepository(ctx, endpoint, input)
	if err != nil {
		return sandboxcontract.PrepareRepositoryResponse{}, err
	}
	repoCtx := sandboxcontract.RepositoryContext{Version: state.Context.Version, WorkingDirectory: state.Context.WorkingDirectory}
	for _, item := range state.Context.Instructions {
		repoCtx.Instructions = append(repoCtx.Instructions, struct {
			Path string `json:"path"`
			Text string `json:"text"`
		}{Path: item.Path, Text: item.Text})
	}
	for _, item := range state.Context.Skills {
		repoCtx.Skills = append(repoCtx.Skills, struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Path        string `json:"path"`
		}{Name: item.Name, Description: item.Description, Path: item.Path})
	}
	return sandboxcontract.PrepareRepositoryResponse{CheckoutID: state.CheckoutID, Commit: state.Commit, Created: state.Created, Context: repoCtx}, nil
}

func (p *Provider) matchIdentity(claim *unstructured.Unstructured, expected createIdentity) error {
	actual, err := p.readIdentity(claim)
	if err != nil {
		return err
	}
	if actual != expected {
		return failure("create_identity_conflict", false)
	}
	return nil
}

func (p *Provider) readIdentity(claim *unstructured.Unstructured) (createIdentity, error) {
	var i createIdentity
	if claim.GetNamespace() != p.config.Namespace || claim.GetUID() == "" || claim.GetLabels()[managedLabel] != managedValue {
		return i, failure("claim_ownership_mismatch", false)
	}
	if json.Unmarshal([]byte(claim.GetAnnotations()[identityAnnotation]), &i) != nil || claim.GetName() != "as-"+i.SandboxID || i.Region != p.config.Region || i.Scope != p.config.Scope {
		return i, failure("claim_identity_mismatch", false)
	}
	pool, _, _ := unstructured.NestedString(claim.Object, "spec", "warmPoolRef", "name")
	expectedPool := p.config.Pool
	if stored := claim.GetAnnotations()[repositoryPoolAnnotation]; stored != "" {
		if p.config.RepositoryStorageClass == "" || !repositoryUUID.MatchString(i.SessionID) {
			return i, failure("claim_repository_profile_mismatch", false)
		}
		expectedPool = p.repositoryPoolName(i.SessionID)
		if stored != expectedPool {
			return i, failure("claim_repository_profile_mismatch", false)
		}
	}
	if pool != expectedPool {
		return i, failure("claim_template_mismatch", false)
	}
	return i, nil
}

func (p *Provider) reference(claim *unstructured.Unstructured) string {
	return p.config.Namespace + "/" + claim.GetName() + "/" + string(claim.GetUID())
}

func (p *Provider) load(ctx context.Context, ref string) (*unstructured.Unstructured, error) {
	parts := strings.Split(ref, "/")
	if len(parts) != 3 || parts[0] != p.config.Namespace || !dnsLabel(parts[1]) || parts[2] == "" {
		return nil, failure("invalid_session_reference", false)
	}
	claim, err := p.resource(Claims).Get(ctx, parts[1], metav1.GetOptions{})
	if err != nil {
		return nil, apiFailure(err, "get", false)
	}
	if string(claim.GetUID()) != parts[2] {
		return nil, failure("claim_uid_mismatch", false)
	}
	if _, err := p.readIdentity(claim); err != nil {
		return nil, err
	}
	return claim, nil
}

func (p *Provider) GetSandbox(ctx context.Context, ref string) (sandboxgateway.ProviderSandbox, error) {
	claim, err := p.load(ctx, ref)
	if err != nil {
		return sandboxgateway.ProviderSandbox{}, err
	}
	return p.observe(ctx, claim)
}

func claimExpiry(claim *unstructured.Unstructured) (time.Time, error) {
	timestamp, _, _ := unstructured.NestedString(claim.Object, "spec", "lifecycle", "shutdownTime")
	deadline, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return time.Time{}, failure("invalid_claim_expiry", false)
	}
	return deadline, nil
}

func (p *Provider) observe(ctx context.Context, claim *unstructured.Unstructured) (sandboxgateway.ProviderSandbox, error) {
	if _, err := p.readIdentity(claim); err != nil {
		return sandboxgateway.ProviderSandbox{}, err
	}
	deadline, err := claimExpiry(claim)
	if err != nil {
		return sandboxgateway.ProviderSandbox{}, err
	}
	result := sandboxgateway.ProviderSandbox{SessionRef: p.reference(claim), State: sandboxgateway.ProviderSandboxCreating,
		Root: "/workspace", ExpiresAt: deadline, ProviderStatusClass: "creating"}
	if claim.GetDeletionTimestamp() != nil {
		result.State = sandboxgateway.ProviderSandboxDeleting
		result.ProviderStatusClass = "deleting"
		return result, nil
	}
	if !deadline.After(p.config.Now()) {
		result.State = sandboxgateway.ProviderSandboxFailed
		result.ProviderStatusClass = "failed"
		return result, nil
	}
	endpoint, ready, err := p.endpoint(ctx, claim)
	if err != nil {
		return result, err
	}
	if !ready {
		return result, nil
	}
	bound := incarnation{endpoint.SandboxUID, endpoint.PodUID, endpoint.ContainerID, endpoint.BootID}
	annotations := claim.GetAnnotations()
	var existing incarnation
	if encoded := annotations[incarnationAnnotation]; encoded != "" {
		if json.Unmarshal([]byte(encoded), &existing) != nil || existing != bound {
			result.State = sandboxgateway.ProviderSandboxFailed
			result.ProviderStatusClass = "failed"
			return result, nil
		}
	} else {
		// Pin on the API object, not in process memory: a gateway restart must
		// not adopt a replacement Pod/container as the same Core generation.
		encoded, _ := json.Marshal(bound)
		annotations[incarnationAnnotation] = string(encoded)
		claim = claim.DeepCopy()
		claim.SetAnnotations(annotations)
		if _, err := p.resource(Claims).Update(ctx, claim, metav1.UpdateOptions{}); err != nil {
			return result, apiFailure(err, "bind_incarnation", true)
		}
	}
	i, err := p.readIdentity(claim)
	if err != nil {
		return result, err
	}
	if err := p.runtime.Bind(ctx, endpoint, k8sruntime.Binding{
		Identity: k8sruntime.Identity{PodUID: endpoint.PodUID, BootID: endpoint.BootID},
		Session:  sandboxcontract.SessionIdentity{WorkspaceID: i.WorkspaceID, SessionID: i.SessionID, EnvironmentID: i.EnvironmentID},
		Ref:      sandboxcontract.SandboxRef{SandboxID: i.SandboxID, TargetGeneration: i.Generation, BackendKind: executionbackend.KindKubernetes},
	}); err != nil {
		return result, failure("runtime_binding_failed", false)
	}
	result.State = sandboxgateway.ProviderSandboxReady
	result.ProviderStatusClass = "ready"
	result.ExecutionReady = true
	return result, nil
}

// endpoint distrusts status.serviceFQDN and podIPs: neither is dispatch authority.
func (p *Provider) endpoint(ctx context.Context, claim *unstructured.Unstructured) (Endpoint, bool, error) {
	name, _, _ := unstructured.NestedString(claim.Object, "status", "sandbox", "name")
	if name == "" {
		return Endpoint{}, false, nil
	}
	if !dnsLabel(name) {
		return Endpoint{}, false, failure("invalid_sandbox_name", false)
	}
	sandbox, err := p.resource(Sandboxes).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Endpoint{}, false, nil
	}
	if err != nil {
		return Endpoint{}, false, apiFailure(err, "sandbox_get", false)
	}
	if !ownedBy(sandbox, "SandboxClaim", Claims.GroupVersion().String(), claim.GetUID()) {
		return Endpoint{}, false, failure("sandbox_ownership_mismatch", false)
	}
	if sandbox.GetDeletionTimestamp() != nil || !readyCondition(sandbox) {
		return Endpoint{}, false, nil
	}
	service, _, _ := unstructured.NestedString(sandbox.Object, "status", "service")
	if !dnsLabel(service) {
		return Endpoint{}, false, nil
	}
	// The v1beta1 controller uses the Sandbox name as Pod name. A warm-pool
	// adoption may carry the controller-owned legacy Pod-name annotation.
	podName := name
	if legacy := sandbox.GetAnnotations()["agents.x-k8s.io/pod-name"]; legacy != "" {
		podName = legacy
	}
	if !dnsLabel(podName) {
		return Endpoint{}, false, failure("invalid_pod_name", false)
	}
	pod, err := p.resource(Pods).Get(ctx, podName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Endpoint{}, false, nil
	}
	if err != nil {
		return Endpoint{}, false, apiFailure(err, "pod_get", false)
	}
	if !ownedBy(pod, "Sandbox", Sandboxes.GroupVersion().String(), sandbox.GetUID()) {
		return Endpoint{}, false, failure("pod_ownership_mismatch", false)
	}
	if pod.GetDeletionTimestamp() != nil || !readyCondition(pod) {
		return Endpoint{}, false, nil
	}
	statuses, _, _ := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
	containerID := ""
	for _, item := range statuses {
		s, ok := item.(map[string]any)
		if ok && s["name"] == "runtime" && s["ready"] == true {
			containerID, _ = s["containerID"].(string)
		}
	}
	if containerID == "" || sandbox.GetUID() == "" || pod.GetUID() == "" {
		return Endpoint{}, false, nil
	}
	svc, err := p.resource(Services).Get(ctx, service, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Endpoint{}, false, nil
	}
	if err != nil {
		return Endpoint{}, false, apiFailure(err, "service_get", false)
	}
	if !ownedBy(svc, "Sandbox", Sandboxes.GroupVersion().String(), sandbox.GetUID()) {
		return Endpoint{}, false, failure("service_ownership_mismatch", false)
	}
	selectors, _, _ := unstructured.NestedStringMap(svc.Object, "spec", "selector")
	if len(selectors) == 0 {
		return Endpoint{}, false, failure("service_selector_missing", false)
	}
	for key, value := range selectors {
		if pod.GetLabels()[key] != value {
			return Endpoint{}, false, failure("service_selector_mismatch", false)
		}
	}
	clusterIP, _, _ := unstructured.NestedString(svc.Object, "spec", "clusterIP")
	if clusterIP != "None" || svc.GetDeletionTimestamp() != nil {
		return Endpoint{}, false, failure("invalid_sandbox_service", false)
	}
	endpoint := Endpoint{Origin: fmt.Sprintf("https://%s.%s.svc.%s:%d", service, p.config.Namespace, p.config.ClusterDomain, p.config.RuntimePort),
		ClaimUID: string(claim.GetUID()), SandboxUID: string(sandbox.GetUID()), PodUID: string(pod.GetUID()), ContainerID: containerID}
	boot, err := p.runtime.Identity(ctx, endpoint)
	if err != nil {
		return Endpoint{}, false, failure("runtime_identity_unavailable", false)
	}
	if len(boot) != 64 {
		return Endpoint{}, false, failure("invalid_runtime_identity", false)
	}
	endpoint.BootID = boot
	return endpoint, true, nil
}

func readyCondition(obj *unstructured.Unstructured) bool {
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, value := range conditions {
		c, ok := value.(map[string]any)
		if ok && c["type"] == "Ready" && c["status"] == "True" {
			if obj.GetKind() != "Pod" {
				generation, ok := c["observedGeneration"].(int64)
				if !ok || generation != obj.GetGeneration() {
					return false
				}
			}
			return true
		}
	}
	return false
}

func ownedBy(obj *unstructured.Unstructured, kind, apiVersion string, uid types.UID) bool {
	if uid == "" {
		return false
	}
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Controller != nil && *ref.Controller && ref.Kind == kind && ref.APIVersion == apiVersion && ref.UID == uid {
			return true
		}
	}
	return false
}

func (p *Provider) SetSandboxTimeout(ctx context.Context, r sandboxgateway.SetSandboxTimeoutProviderRequest) (sandboxgateway.ProviderSandbox, error) {
	if !ttlValid(r.TTL) {
		return sandboxgateway.ProviderSandbox{}, failure("invalid_ttl", false)
	}
	claim, err := p.load(ctx, r.SessionRef)
	if err != nil {
		return sandboxgateway.ProviderSandbox{}, err
	}
	expiry, err := claimExpiry(claim)
	if err != nil {
		return sandboxgateway.ProviderSandbox{}, err
	}
	if claim.GetDeletionTimestamp() != nil || !expiry.After(p.config.Now()) {
		return sandboxgateway.ProviderSandbox{}, failure("claim_expired", false)
	}
	deadline := p.config.Now().UTC().Add(r.TTL)
	if deadline.Before(expiry) {
		deadline = expiry
	}
	if err := unstructured.SetNestedField(claim.Object, deadline.Format(time.RFC3339), "spec", "lifecycle", "shutdownTime"); err != nil {
		return sandboxgateway.ProviderSandbox{}, failure("invalid_claim", false)
	}
	updated, err := p.resource(Claims).Update(ctx, claim, metav1.UpdateOptions{})
	if err != nil {
		return sandboxgateway.ProviderSandbox{}, apiFailure(err, "renew", true)
	}
	return p.observe(ctx, updated)
}

func (p *Provider) DeleteSandbox(ctx context.Context, r sandboxgateway.DeleteSandboxProviderRequest) error {
	i, err := p.identity(r.Identity)
	if err != nil {
		return err
	}
	var claim *unstructured.Unstructured
	if r.SessionRef != "" {
		claim, err = p.load(ctx, r.SessionRef)
	} else {
		claim, err = p.resource(Claims).Get(ctx, "as-"+i.SandboxID, metav1.GetOptions{})
	}
	if errors.Is(err, sandboxgateway.ErrProviderSandboxNotFound) || apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return apiFailure(err, "delete_lookup", false)
	}
	if err := p.matchIdentity(claim, i); err != nil {
		return err
	}
	uid := claim.GetUID()
	rv := claim.GetResourceVersion()
	policy := metav1.DeletePropagationForeground
	err = p.resource(Claims).Delete(ctx, claim.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}, PropagationPolicy: &policy})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return apiFailure(err, "delete", true)
	}
	return nil // Core still calls Get until foreground deletion is confirmed.
}

func (p *Provider) dispatchEndpoint(ctx context.Context, ref string, target executionbackend.Target, operation executionbackend.OperationContext) (Endpoint, error) {
	if target.Kind != executionbackend.KindKubernetes {
		return Endpoint{}, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "wrong_backend_kind", errors.New("Kubernetes target required"))
	}
	claim, err := p.load(ctx, ref)
	if err != nil {
		return Endpoint{}, notSent(err)
	}
	i, err := p.readIdentity(claim)
	if err != nil {
		return Endpoint{}, notSent(err)
	}
	if i.Generation != target.Generation || i.SandboxID != target.ID || i.EnvironmentID != target.EnvironmentID || i.WorkspaceID != operation.WorkspaceID || i.SessionID != operation.SessionID {
		return Endpoint{}, notSent(failure("dispatch_identity_mismatch", false))
	}
	state, err := p.observe(ctx, claim)
	if err != nil {
		return Endpoint{}, notSent(err)
	}
	if !state.ExecutionReady {
		return Endpoint{}, notSent(failure("runtime_not_ready", false))
	}
	// Re-read the persisted pin, including on the very first dispatch.
	claim, err = p.load(ctx, ref)
	if err != nil {
		return Endpoint{}, notSent(err)
	}
	endpoint, ready, err := p.endpoint(ctx, claim)
	if err != nil {
		return Endpoint{}, notSent(err)
	}
	var pinned incarnation
	if !ready || json.Unmarshal([]byte(claim.GetAnnotations()[incarnationAnnotation]), &pinned) != nil || !reflect.DeepEqual(pinned, incarnation{endpoint.SandboxUID, endpoint.PodUID, endpoint.ContainerID, endpoint.BootID}) {
		return Endpoint{}, notSent(failure("runtime_replaced", false))
	}
	return endpoint, nil
}

func (p *Provider) StartProcess(ctx context.Context, r sandboxgateway.StartProcessProviderRequest) (executionbackend.Exchange, error) {
	if err := r.Request.Validate(); err != nil {
		return nil, notSent(err)
	}
	e, err := p.dispatchEndpoint(ctx, r.SessionRef, r.Request.Target, r.Request.Operation)
	if err != nil {
		return nil, err
	}
	return p.runtime.StartProcess(ctx, e, r.Request)
}
func (p *Provider) SignalProcess(ctx context.Context, r sandboxgateway.SignalProcessProviderRequest) (executionbackend.Exchange, error) {
	if err := r.Request.Validate(); err != nil {
		return nil, notSent(err)
	}
	e, err := p.dispatchEndpoint(ctx, r.SessionRef, r.Request.Target, r.Request.Operation)
	if err != nil {
		return nil, err
	}
	return p.runtime.SignalProcess(ctx, e, r.Request)
}
func (p *Provider) ReadFile(ctx context.Context, r sandboxgateway.ReadFileProviderRequest) (executionbackend.Exchange, error) {
	if err := r.Request.Validate(); err != nil {
		return nil, notSent(err)
	}
	e, err := p.dispatchEndpoint(ctx, r.SessionRef, r.Request.Target, r.Request.Operation)
	if err != nil {
		return nil, err
	}
	return p.runtime.ReadFile(ctx, e, r.Request)
}

func notSent(err error) error {
	return executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "kubernetes_target_unavailable", err)
}
func failure(code string, ambiguous bool) error {
	return &sandboxgateway.ProviderError{Code: code, Ambiguous: ambiguous, Cause: errors.New(code)}
}
func apiFailure(err error, action string, mutation bool) error {
	if apierrors.IsNotFound(err) {
		return sandboxgateway.ErrProviderSandboxNotFound
	}
	// Never retain API response bodies: they may echo a PodSpec or credentials.
	code := "kubernetes_" + action + "_unavailable"
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) || apierrors.IsInvalid(err) || apierrors.IsConflict(err) {
		return failure("kubernetes_"+action+"_rejected", false)
	}
	return failure(code, mutation)
}

var _ sandboxgateway.Provider = (*Provider)(nil)
