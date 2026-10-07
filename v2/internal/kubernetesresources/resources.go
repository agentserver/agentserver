// Package kubernetesresources renders namespace-scoped Agent Sandbox workloads.
package kubernetesresources

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

const ControllerVersion = "v1.0.5"

const (
	BubblewrapSeccompProfile  = "agentserver/bwrap-v1.json"
	BubblewrapAppArmorProfile = "agentserver-bwrap-v1"
	BubblewrapNodeLabel       = "agentserver.byted.bps.dev/bwrap-profile"
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
var dnsName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)

type Config struct {
	Namespace             string
	TemplateName          string
	RuntimeImage          string
	RuntimeTLSSecret      string
	GatewayNamespace      string
	GatewayServiceAccount string
	GatewayIdentity       string
	// Empty intentionally omits runtimeClassName (the cluster default). It
	// does NOT create a RuntimeClass called "default" or claim VM isolation.
	RuntimeClassName  string
	BubblewrapProfile bool
	RuntimeProxyURL   string
}

func Resources(c Config) ([]map[string]any, error) {
	for _, s := range []string{c.Namespace, c.TemplateName, c.RuntimeTLSSecret, c.GatewayNamespace, c.GatewayServiceAccount} {
		if s == "" || !dnsLabel.MatchString(s) || len(s) > 63 {
			return nil, errors.New("sandbox deployment names must be DNS labels")
		}
	}
	if c.RuntimeImage == "" || strings.ContainsAny(c.RuntimeImage, " \t\r\n\x00") {
		return nil, errors.New("runtime image must be explicitly configured")
	}
	if c.RuntimeProxyURL != "" {
		u, err := url.Parse(c.RuntimeProxyURL)
		if err != nil || u.Scheme != "socks5h" || !dnsName.MatchString(u.Hostname()) || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("invalid runtime SOCKS5 proxy")
		}
	}
	identity, err := url.Parse(c.GatewayIdentity)
	if err != nil || identity.Scheme != "spiffe" || identity.Host == "" || identity.Path != "/ns/"+c.GatewayNamespace+"/sa/"+c.GatewayServiceAccount || identity.RawQuery != "" || identity.Fragment != "" || identity.User != nil {
		return nil, errors.New("runtime gateway SPIFFE identity must match the gateway service account")
	}
	if c.RuntimeClassName != "" && !dnsName.MatchString(c.RuntimeClassName) || len(c.RuntimeClassName) > 253 {
		return nil, errors.New("invalid runtime class")
	}
	podLabels := map[string]any{"app.kubernetes.io/name": "agentserver-sandbox-runtime", "app.kubernetes.io/part-of": "agentserver-v2"}
	spec := map[string]any{
		"automountServiceAccountToken": false, "enableServiceLinks": false,
		"serviceAccountName": "sandbox-runtime", "restartPolicy": "Never",
		"nodeSelector":    map[string]any{"kubernetes.io/os": "linux", "kubernetes.io/arch": "amd64"},
		"securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": int64(10000), "runAsGroup": int64(10000), "fsGroup": int64(10000), "seccompProfile": map[string]any{"type": "RuntimeDefault"}},
		"containers": []any{map[string]any{
			"name": "runtime", "image": c.RuntimeImage, "imagePullPolicy": "IfNotPresent",
			"command":         []any{"/usr/local/bin/agentserver-k8s-runtime"},
			"ports":           []any{map[string]any{"name": "runtime", "containerPort": int64(8443)}},
			"env":             []any{map[string]any{"name": "AGENTSERVER_SANDBOX_POD_UID", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.uid"}}}, map[string]any{"name": "AGENTSERVER_SANDBOX_GATEWAY_IDENTITY", "value": c.GatewayIdentity}},
			"securityContext": map[string]any{"readOnlyRootFilesystem": true, "allowPrivilegeEscalation": false, "capabilities": map[string]any{"drop": []any{"ALL"}}},
			"resources":       map[string]any{"requests": map[string]any{"cpu": "250m", "memory": "256Mi", "ephemeral-storage": "256Mi"}, "limits": map[string]any{"cpu": "2", "memory": "2Gi", "ephemeral-storage": "4Gi"}},
			"volumeMounts":    []any{map[string]any{"name": "workspace", "mountPath": "/workspace"}, map[string]any{"name": "scratch", "mountPath": "/tmp"}, map[string]any{"name": "runtime-tls", "mountPath": "/var/run/agentserver/runtime-tls", "readOnly": true}},
			"readinessProbe":  map[string]any{"httpGet": map[string]any{"path": "/readyz", "port": int64(8443), "scheme": "HTTPS"}, "periodSeconds": int64(5)},
		}},
		"volumes": []any{map[string]any{"name": "workspace", "emptyDir": map[string]any{"sizeLimit": "2Gi"}}, map[string]any{"name": "scratch", "emptyDir": map[string]any{"sizeLimit": "256Mi"}}, map[string]any{"name": "runtime-tls", "secret": map[string]any{"secretName": c.RuntimeTLSSecret, "defaultMode": int64(0440)}}},
	}
	if c.RuntimeClassName != "" {
		spec["runtimeClassName"] = c.RuntimeClassName
	}
	if c.RuntimeProxyURL != "" {
		container := spec["containers"].([]any)[0].(map[string]any)
		container["env"] = append(container["env"].([]any), map[string]any{"name": "AGENTSERVER_SANDBOX_HTTP_PROXY", "value": c.RuntimeProxyURL})
	}
	if c.BubblewrapProfile {
		security := spec["securityContext"].(map[string]any)
		security["seccompProfile"] = map[string]any{"type": "Localhost", "localhostProfile": BubblewrapSeccompProfile}
		security["appArmorProfile"] = map[string]any{"type": "Localhost", "localhostProfile": BubblewrapAppArmorProfile}
		spec["nodeSelector"].(map[string]any)[BubblewrapNodeLabel] = "v1"
	}
	obj := func(api, kind, name string, body map[string]any) map[string]any {
		body["apiVersion"] = api
		body["kind"] = kind
		body["metadata"] = map[string]any{"name": name, "namespace": c.Namespace, "labels": map[string]any{"app.kubernetes.io/part-of": "agentserver-v2"}}
		return body
	}
	selector := map[string]any{"matchLabels": podLabels}
	return []map[string]any{
		obj("v1", "ServiceAccount", "sandbox-runtime", map[string]any{"automountServiceAccountToken": false}),
		obj("extensions.agents.x-k8s.io/v1beta1", "SandboxTemplate", c.TemplateName, map[string]any{"spec": map[string]any{
			"service": true, "networkPolicyManagement": "Unmanaged", "envVarsInjectionPolicy": "Disallowed", "volumeClaimTemplatesPolicy": "Disallowed",
			"podTemplate": map[string]any{"metadata": map[string]any{"labels": podLabels}, "spec": spec},
		}}),
		// Zero spares means demand-only allocation, not disabled execution.
		// User data must never be returned to a warm pool after claim release.
		obj("extensions.agents.x-k8s.io/v1beta1", "SandboxWarmPool", c.TemplateName, map[string]any{"spec": map[string]any{"replicas": int64(0), "sandboxTemplateRef": map[string]any{"name": c.TemplateName}}}),
		obj("rbac.authorization.k8s.io/v1", "Role", "sandbox-lifecycle", map[string]any{"rules": []any{
			map[string]any{"apiGroups": []any{"extensions.agents.x-k8s.io"}, "resources": []any{"sandboxclaims"}, "verbs": []any{"create", "get", "list", "watch", "update", "delete"}},
			map[string]any{"apiGroups": []any{"agents.x-k8s.io"}, "resources": []any{"sandboxes"}, "verbs": []any{"get", "list", "watch"}},
			map[string]any{"apiGroups": []any{""}, "resources": []any{"pods", "services"}, "verbs": []any{"get", "list", "watch"}},
		}}),
		obj("rbac.authorization.k8s.io/v1", "RoleBinding", "sandbox-lifecycle", map[string]any{
			"roleRef":  map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "sandbox-lifecycle"},
			"subjects": []any{map[string]any{"kind": "ServiceAccount", "name": c.GatewayServiceAccount, "namespace": c.GatewayNamespace}},
		}),
		obj("networking.k8s.io/v1", "NetworkPolicy", "sandbox-default-deny", map[string]any{"spec": map[string]any{"podSelector": map[string]any{}, "policyTypes": []any{"Ingress", "Egress"}}}),
		obj("networking.k8s.io/v1", "NetworkPolicy", "sandbox-runtime", map[string]any{"spec": map[string]any{
			"podSelector": selector, "policyTypes": []any{"Ingress", "Egress"},
			"ingress": []any{map[string]any{"from": []any{map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": c.GatewayNamespace}}, "podSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": "sandbox-gateway-k8s"}}}}, "ports": []any{map[string]any{"protocol": "TCP", "port": int64(8443)}}}},
			"egress":  []any{map[string]any{"to": []any{map[string]any{"namespaceSelector": map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": "kube-system"}}, "podSelector": map[string]any{"matchLabels": map[string]any{"k8s-app": "kube-dns"}}}}, "ports": []any{map[string]any{"protocol": "UDP", "port": int64(53)}, map[string]any{"protocol": "TCP", "port": int64(53)}}}},
		}}),
	}, nil
}
