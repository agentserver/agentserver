package productiondeploy

import (
	"strconv"

	"github.com/agentserver/agentserver/v2/internal/taeimage"
)

const (
	taeProbeConnectivityAttempts    = 20
	taeProbeLifecycleAttempts       = 1
	taeNetworkProbeInputPlaceholder = "agentserver-tae-network-probe-input-placeholder"
	taeNetworkProbeJobPlaceholder   = "agentserver-tae-network-probe-job-placeholder"
)

// taeNetworkProbeResources returns the static half of the Pulumi-owned,
// one-shot probe. The generated Chart keeps it behind a closed values schema
// and creates the companion ConfigMap only when a bootstrap release receives
// an actual published policy revision. Keeping the policy revision outside
// this static document avoids evaluating deployment text with Helm tpl.
func taeNetworkProbeResources(config LoadedConfig) ([]kubeObject, error) {
	validated, err := ValidateConfig(config.Document)
	if err != nil {
		return nil, err
	}
	config = validated
	document := config.Document
	items := []kubeObject{serviceAccountResource(config, taeNetworkProbeComponent)}
	for _, profile := range config.ManagedSandboxProfiles {
		repository, repositoryErr := productionTAEManagedSandboxRepository(profile.Document.Region)
		if repositoryErr != nil {
			return nil, repositoryErr
		}
		taeSandboxImage, imageErr := taeimage.ContentTagForRepository(repository, document.Images.ManagedSandbox)
		if imageErr != nil {
			return nil, imageErr
		}
		profileItems, renderErr := taeNetworkProbeProfileResources(config, profile, taeSandboxImage)
		if renderErr != nil {
			return nil, renderErr
		}
		items = append(items, profileItems...)
	}
	return items, nil
}

func taeNetworkProbeProfileResources(
	config LoadedConfig,
	loaded LoadedManagedSandboxProfile,
	taeSandboxImage string,
) ([]kubeObject, error) {
	document := config.Document
	profile := loaded.Document
	jobName := taeNetworkProbeJobPlaceholderForRegion(profile.Region)
	inputName := taeNetworkProbeInputPlaceholderForRegion(profile.Region)
	component := taeNetworkProbeProfileComponent(profile.Region)
	labels := componentLabels(component)
	material, err := secretMaterialVolume(
		"bytecloud-identity", profile.Gateway.Secret, materialProfileTAENetworkProbe, groupReadableSecretMode,
	)
	if err != nil {
		return nil, err
	}
	mounts, err := secretMaterialMounts("bytecloud-identity", "/var/run/agentserver/material", materialProfileTAENetworkProbe)
	if err != nil {
		return nil, err
	}
	proxyURL := ""
	if loaded.Proxy != nil {
		proxyURL = loaded.Proxy.URL
	}
	environment := []any{
		valueEnvironment("AGENTSERVER_V2_TAE_REGION", profile.TAE.Region),
		valueEnvironment("AGENTSERVER_V2_TAE_PSM", profile.TAE.PSM),
		valueEnvironment("AGENTSERVER_V2_TAE_SANDBOX_IMAGE", taeSandboxImage),
		valueEnvironment("AGENTSERVER_V2_TAE_SANDBOX_ID", profile.TAE.SandboxID),
		valueEnvironment("AGENTSERVER_V2_TAE_SANDBOX_REVISION_ID", profile.TAE.RevisionID),
		valueEnvironment("AGENTSERVER_V2_TAE_CONTROL_PLANE_URL", profile.TAE.ControlPlaneURL),
		valueEnvironment("AGENTSERVER_V2_TAE_DATA_PLANE_SUFFIX", profile.TAE.DataPlaneSuffix),
		valueEnvironment("AGENTSERVER_V2_TAE_AUTH_MODE", "bytecloud-app-aksk-v1"),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_SITE", profile.TAE.ByteCloudSite),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_JWT_ENDPOINT", profile.TAE.ByteCloudJWTEndpoint),
		valueEnvironment("AGENTSERVER_V2_TAE_PROXY_URL", proxyURL),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_ACCESS_KEY_ID_FILE", serviceMaterialPath("bytecloud-access-key-id")),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_SECRET_ACCESS_KEY_FILE", serviceMaterialPath("bytecloud-secret-access-key")),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_JWT_TIMEOUT", "10s"),
		valueEnvironment("AGENTSERVER_V2_TAE_CONTROL_TIMEOUT", "60s"),
		valueEnvironment("AGENTSERVER_V2_TAE_RESPONSE_HEADER_TIMEOUT", "30s"),
		configMapEnvironment("AGENTSERVER_V2_TAE_PROBE_POLICY_REVISION", inputName, "policy-revision"),
		valueEnvironment("AGENTSERVER_V2_TAE_PROBE_LARK_SKILL_SHA256", document.Managed.Lark.SkillSHA256),
		valueEnvironment("AGENTSERVER_V2_TAE_PROBE_CONNECTIVITY_ATTEMPTS", strconv.Itoa(taeProbeConnectivityAttempts)),
		valueEnvironment("AGENTSERVER_V2_TAE_PROBE_LIFECYCLE_ATTEMPTS", strconv.Itoa(taeProbeLifecycleAttempts)),
		valueEnvironment("AGENTSERVER_V2_TAE_PROBE_READY_TIMEOUT", "5m"),
		fieldEnvironment("AGENTSERVER_V2_TAE_PROBE_POD_NAMESPACE", "metadata.namespace"),
		fieldEnvironment("AGENTSERVER_V2_TAE_PROBE_POD_NAME", "metadata.name"),
		fieldEnvironment("AGENTSERVER_V2_TAE_PROBE_POD_UID", "metadata.uid"),
		fieldEnvironment("AGENTSERVER_V2_TAE_PROBE_NODE_NAME", "spec.nodeName"),
		fieldEnvironment("AGENTSERVER_V2_TAE_PROBE_SERVICE_ACCOUNT", "spec.serviceAccountName"),
	}
	egress := append([]any(nil), dnsEgress(document.Network)...)
	if loaded.Proxy != nil {
		egress = append(egress, namespacedPodTCPEgress(loaded.Proxy.Namespace, loaded.Proxy.PodSelector, loaded.Proxy.Port))
	}
	egress = append(egress, externalEgress(profile.SandboxExternalEgress)...)
	return []kubeObject{
		networkPolicy(config, jobName+"-egress", matchComponent(component), nil, egress),
		{
			"apiVersion": "batch/v1", "kind": "Job",
			"metadata": metadata(jobName, document.Namespace, labels, map[string]string{
				"agentserver.dev/managed-sandbox-region": profile.Region,
			}),
			"spec": kubeObject{
				"backoffLimit": 0, "activeDeadlineSeconds": 7200,
				"template": kubeObject{
					"metadata": kubeObject{"labels": labels},
					"spec": kubeObject{
						"serviceAccountName": taeNetworkProbeComponent, "automountServiceAccountToken": false,
						"restartPolicy": "Never", "enableServiceLinks": false,
						"terminationGracePeriodSeconds": 30,
						"nodeSelector":                  productionNodeSelector(document.Platform),
						"securityContext": kubeObject{
							"runAsUser": int64(ServiceUID), "runAsGroup": int64(ServiceGID), "runAsNonRoot": true,
							"fsGroup": int64(ServiceGID), "fsGroupChangePolicy": "OnRootMismatch",
							"seccompProfile": kubeObject{"type": "RuntimeDefault"},
						},
						"containers": []any{kubeObject{
							"name": taeNetworkProbeComponent, "image": document.Images.Service, "imagePullPolicy": "IfNotPresent",
							"command": []any{"/usr/local/bin/sandbox-gateway"}, "args": []any{"probe-network"},
							"env": environment, "resources": resources(document.Resources.SandboxGateway),
							"securityContext": runtimeSecurityContext(ServiceUID, ServiceGID),
							"volumeMounts":    append(mounts, kubeObject{"name": "scratch", "mountPath": "/tmp"}),
						}},
						"volumes": []any{material, emptyDirVolume("scratch", "Memory", document.Resources.ScratchTmpfs)},
					},
				},
			},
		},
	}, nil
}

func taeNetworkProbeProfileComponent(region string) string {
	return taeNetworkProbeComponent + "-" + region
}

func taeNetworkProbeJobPlaceholderForRegion(region string) string {
	return taeNetworkProbeJobPlaceholder + "-" + region
}

func taeNetworkProbeInputPlaceholderForRegion(region string) string {
	return taeNetworkProbeInputPlaceholder + "-" + region
}

func fieldEnvironment(name, fieldPath string) kubeObject {
	return kubeObject{
		"name": name,
		"valueFrom": kubeObject{"fieldRef": kubeObject{
			"apiVersion": "v1", "fieldPath": fieldPath,
		}},
	}
}

func configMapEnvironment(name, configMapName, key string) kubeObject {
	return kubeObject{
		"name": name,
		"valueFrom": kubeObject{"configMapKeyRef": kubeObject{
			"name": configMapName, "key": key, "optional": false,
		}},
	}
}
