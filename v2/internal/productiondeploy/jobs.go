package productiondeploy

func renderMigrationJob(context renderContext) kubeObject {
	return renderOneShotJob(
		context,
		context.migrationJobName,
		migrationComponent,
		[]any{"/usr/local/bin/agentserver-core"},
		[]any{"migrate"},
		nil,
		nil,
		map[string]string{"agentserver.dev/schema-version": formatSchemaVersion(context.migrationVersion)},
	)
}

func renderBootstrapJob(context renderContext) kubeObject {
	volumes := []any{configMapVolume("bootstrap-config", context.bootstrapConfigName, map[string]string{
		"bootstrap.json": "bootstrap.json",
	})}
	mounts := []any{kubeObject{
		"name": "bootstrap-config", "mountPath": "/etc/agentserver/bootstrap.json",
		"subPath": "bootstrap.json", "readOnly": true,
	}}
	return renderOneShotJob(
		context,
		context.bootstrapJobName,
		bootstrapComponent,
		[]any{"/usr/local/bin/agentserver-core"},
		[]any{"bootstrap", "--config=/etc/agentserver/bootstrap.json"},
		volumes,
		mounts,
		map[string]string{"agentserver.dev/bootstrap-sha256": context.bootstrapHash},
	)
}

func renderOneShotJob(
	context renderContext,
	name, component string,
	command, args []any,
	extraVolumes, extraMounts []any,
	annotations map[string]string,
) kubeObject {
	config := context.config
	labels := componentLabels(component)
	serviceAccountName := bootstrapServiceAccount
	if component == migrationComponent {
		// A Helm pre-install/pre-upgrade migration runs before chart-managed
		// ServiceAccounts exist. The migration has no Kubernetes API capability
		// and receives no projected token, so the namespace's pre-existing
		// default ServiceAccount is the smallest dependency that preserves the
		// required migrate-before-rollout ordering.
		serviceAccountName = "default"
	}
	volumes := append([]any(nil), extraVolumes...)
	volumes = append(volumes, emptyDirVolume("scratch", "Memory", config.Document.Resources.ScratchTmpfs))
	mounts := append([]any(nil), extraMounts...)
	mounts = append(mounts, kubeObject{"name": "scratch", "mountPath": "/tmp"})
	return kubeObject{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": metadata(name, config.Document.Namespace, labels, annotations),
		"spec": kubeObject{
			"backoffLimit": 3, "activeDeadlineSeconds": 600,
			"template": kubeObject{
				"metadata": kubeObject{"labels": labels, "annotations": annotations},
				"spec": kubeObject{
					"serviceAccountName":           serviceAccountName,
					"automountServiceAccountToken": false,
					"restartPolicy":                "OnFailure", "enableServiceLinks": false,
					"terminationGracePeriodSeconds": 10,
					"nodeSelector":                  productionNodeSelector(),
					"securityContext": kubeObject{
						"runAsUser": int64(ServiceUID), "runAsGroup": int64(ServiceGID), "runAsNonRoot": true,
						"seccompProfile": kubeObject{"type": "RuntimeDefault"},
					},
					"containers": []any{kubeObject{
						"name": component, "image": config.Document.Images.Service,
						"command": command, "args": args,
						"env":             []any{secretEnvironment("AGENTSERVER_V2_DATABASE_URL", config.Document.Secrets.Core, "database-url")},
						"resources":       resources(config.Document.Resources.Core),
						"securityContext": runtimeSecurityContext(ServiceUID, ServiceGID),
						"volumeMounts":    mounts,
					}},
					"volumes": volumes,
				},
			},
		},
	}
}

func formatSchemaVersion(version int64) string {
	if version < 0 {
		return "invalid"
	}
	result := ""
	for value := version; value > 0; value /= 10 {
		result = string(rune('0'+value%10)) + result
	}
	if result == "" {
		result = "0"
	}
	for len(result) < 4 {
		result = "0" + result
	}
	return result
}
