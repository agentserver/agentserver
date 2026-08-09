package productiondeploy

import "strconv"

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
	configSHA256 := canonicalDigest(document)
	jobName := taeNetworkProbeJobPlaceholder
	labels := componentLabels(taeNetworkProbeComponent)
	material, err := secretMaterialVolume(
		"bytecloud-identity", document.Secrets.SandboxGateway, materialProfileTAENetworkProbe, groupReadableSecretMode,
	)
	if err != nil {
		return nil, err
	}
	mounts, err := secretMaterialMounts("bytecloud-identity", "/var/run/agentserver/material", materialProfileTAENetworkProbe)
	if err != nil {
		return nil, err
	}
	environment := []any{
		valueEnvironment("AGENTSERVER_V2_TAE_SANDBOX_IMAGE", document.Images.ManagedSandbox),
		valueEnvironment("AGENTSERVER_V2_TAE_AUTH_MODE", "bytecloud-app-aksk-v1"),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_SITE", "i18n-tt"),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_JWT_ENDPOINT", ProductionByteCloudJWTEndpoint),
		valueEnvironment("AGENTSERVER_V2_TAE_PROXY_URL", ProductionTAEProxyURL),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_ACCESS_KEY_ID_FILE", serviceMaterialPath("bytecloud-access-key-id")),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_SECRET_ACCESS_KEY_FILE", serviceMaterialPath("bytecloud-secret-access-key")),
		valueEnvironment("AGENTSERVER_V2_TAE_BYTECLOUD_JWT_TIMEOUT", "10s"),
		valueEnvironment("AGENTSERVER_V2_TAE_CONTROL_TIMEOUT", "60s"),
		valueEnvironment("AGENTSERVER_V2_TAE_RESPONSE_HEADER_TIMEOUT", "30s"),
		valueEnvironment("AGENTSERVER_V2_TAE_PROBE_DEPLOYMENT_CONFIG_SHA256", configSHA256),
		configMapEnvironment("AGENTSERVER_V2_TAE_PROBE_POLICY_REVISION", taeNetworkProbeInputPlaceholder, "policy-revision"),
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
	egress = append(egress, namespacedPodTCPEgress(
		ProductionTAEProxyNamespace,
		map[string]string{"app": ProductionTAEProxyPodApp},
		ProductionTAEProxyPort,
	))
	items := []kubeObject{
		serviceAccountResource(config, taeNetworkProbeComponent),
		networkPolicy(config, jobName+"-egress", matchComponent(taeNetworkProbeComponent), nil, egress),
		{
			"apiVersion": "batch/v1", "kind": "Job",
			"metadata": metadata(jobName, document.Namespace, labels, map[string]string{
				"agentserver.dev/config-sha256": configSHA256,
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
	}
	return items, nil
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
