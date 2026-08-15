package productiondeploy

import (
	"strings"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func renderHydraMigrationJob(context renderContext) kubeObject {
	document := context.config.Document
	labels := componentLabels(hydraMigrationComponent)
	return kubeObject{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": metadata(context.hydraMigrationJobName, document.Namespace, labels, map[string]string{
			"agentserver.dev/hydra-version": "26.2.0",
		}),
		"spec": kubeObject{
			"backoffLimit": 3, "activeDeadlineSeconds": 600,
			"template": kubeObject{
				"metadata": kubeObject{"labels": labels},
				"spec": kubeObject{
					"serviceAccountName": "default", "automountServiceAccountToken": false,
					"restartPolicy": "OnFailure", "enableServiceLinks": false,
					"terminationGracePeriodSeconds": 10,
					"nodeSelector":                  productionNodeSelector(document.Platform),
					"securityContext": kubeObject{
						"runAsUser": int64(HydraUID), "runAsGroup": int64(HydraGID), "runAsNonRoot": true,
						"seccompProfile": kubeObject{"type": "RuntimeDefault"},
					},
					"containers": []any{kubeObject{
						"name": hydraMigrationComponent, "image": document.Images.Hydra, "imagePullPolicy": "IfNotPresent",
						"command": []any{"/usr/bin/hydra"}, "args": []any{"migrate", "sql", "up", "-e", "--yes"},
						"env":             []any{secretEnvironment("DSN", document.Secrets.Hydra, "database-url")},
						"resources":       resources(document.Resources.Hydra),
						"securityContext": runtimeSecurityContext(HydraUID, HydraGID),
						"volumeMounts":    []any{kubeObject{"name": "scratch", "mountPath": "/tmp"}},
					}},
					"volumes": []any{emptyDirVolume("scratch", "Memory", document.Resources.ScratchTmpfs)},
				},
			},
		},
	}
}

func renderHydraClientSetupJob(context renderContext) kubeObject {
	document := context.config.Document
	labels := componentLabels(hydraSetupComponent)
	adminOrigin := internalOrigin(HydraInternalHost, document.Services.Hydra.AdminPort)
	platformFlags := hydraPublicClientFlags(
		"AgentServer Platform", corecontract.PlatformOAuthScopes(), corecontract.PlatformOAuthAudience,
		"https://"+document.Ingress.FrontendHostname+"/",
	)
	browserFlags := hydraPublicClientFlags(
		"AgentServer Browser", corecontract.BrowserOAuthScopes(), corecontract.BrowserOAuthAudience,
		"https://"+document.Ingress.BrowserFrontendHostname+"/",
	)
	script := "set -eu\n" +
		"endpoint='" + adminOrigin + "'\n" +
		"reconcile_client() {\n" +
		"  client_id=\"$1\"\n" +
		"  shift\n" +
		"  if /usr/bin/hydra update oauth2-client \"$client_id\" --endpoint \"$endpoint\" \"$@\"; then\n" +
		"    return 0\n" +
		"  fi\n" +
		"  /usr/bin/hydra create oauth2-client --endpoint \"$endpoint\" --id \"$client_id\" \"$@\"\n" +
		"}\n" +
		"reconcile_client '" + document.OAuth.Hydra.PlatformClientID + "' " + platformFlags + "\n" +
		"reconcile_client '" + document.OAuth.Hydra.BrowserClientID + "' " + browserFlags + "\n"
	return kubeObject{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": metadata(context.hydraSetupJobName, document.Namespace, labels, map[string]string{
			"agentserver.dev/hydra-client-profile": "platform-browser-v1",
		}),
		"spec": kubeObject{
			"backoffLimit": 5, "activeDeadlineSeconds": 300,
			"template": kubeObject{
				"metadata": kubeObject{"labels": labels},
				"spec": kubeObject{
					"serviceAccountName": hydraSetupComponent, "automountServiceAccountToken": false,
					"restartPolicy": "OnFailure", "enableServiceLinks": false,
					"terminationGracePeriodSeconds": 10,
					"nodeSelector":                  productionNodeSelector(document.Platform),
					"securityContext": kubeObject{
						"runAsUser": int64(HydraUID), "runAsGroup": int64(HydraGID), "runAsNonRoot": true,
						"fsGroup": int64(HydraGID), "fsGroupChangePolicy": "OnRootMismatch",
						"seccompProfile": kubeObject{"type": "RuntimeDefault"},
					},
					"hostAliases": hostAliases(map[string]string{HydraInternalHost: document.Services.Hydra.ClusterIP}),
					"containers": []any{kubeObject{
						"name": hydraSetupComponent, "image": document.Images.Hydra, "imagePullPolicy": "IfNotPresent",
						"command": []any{"/bin/sh"}, "args": []any{"-ec", script},
						"env":             []any{valueEnvironment("SSL_CERT_FILE", "/var/run/agentserver/hydra/ca.crt")},
						"resources":       resources(document.Resources.Hydra),
						"securityContext": runtimeSecurityContext(HydraUID, HydraGID),
						"volumeMounts": []any{
							kubeObject{"name": "hydra-material", "mountPath": "/var/run/agentserver/hydra", "readOnly": true},
							kubeObject{"name": "scratch", "mountPath": "/tmp"},
						},
					}},
					"volumes": []any{
						hydraMaterialVolume(document.Secrets.Hydra, false),
						emptyDirVolume("scratch", "Memory", document.Resources.ScratchTmpfs),
					},
				},
			},
		},
	}
}

func hydraPublicClientFlags(name string, scopes []string, audience, redirectURI string) string {
	flags := []string{
		"--name '" + name + "'",
		"--grant-type authorization_code",
		"--response-type code",
		"--redirect-uri '" + redirectURI + "'",
		"--token-endpoint-auth-method none",
		"--audience " + audience,
		"--access-token-strategy opaque",
		"--subject-type public",
	}
	for _, scope := range scopes {
		flags = append(flags, "--scope "+scope)
	}
	return strings.Join(flags, " ")
}

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
		secretEnvironment("AGENTSERVER_V2_EXTERNAL_OIDC_ISSUER", context.config.Document.Secrets.Core, "external-oidc-issuer"),
		secretEnvironment("AGENTSERVER_V2_EXTERNAL_OIDC_SUBJECT", context.config.Document.Secrets.Core, "external-oidc-subject"),
	)
}

func renderManagedEnvironmentBootstrapJob(context renderContext, environment managedEnvironmentRender) kubeObject {
	volumes := []any{configMapVolume("managed-environment-config", context.managedEnvironmentConfigName, map[string]string{
		environment.FileName: environment.FileName,
	})}
	mounts := []any{kubeObject{
		"name": "managed-environment-config", "mountPath": "/etc/agentserver/managed-environment.json",
		"subPath": environment.FileName, "readOnly": true,
	}}
	return renderOneShotJob(
		context, environment.JobName, managedEnvironmentBootstrapComponent,
		[]any{"/usr/local/bin/agentserver-core"},
		[]any{"bootstrap-managed-environment", "--config=/etc/agentserver/managed-environment.json"},
		volumes, mounts,
		map[string]string{"agentserver.dev/managed-environment-sha256": environment.Hash},
	)
}

func renderOneShotJob(
	context renderContext,
	name, component string,
	command, args []any,
	extraVolumes, extraMounts []any,
	annotations map[string]string,
	extraEnv ...any,
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
					"nodeSelector":                  productionNodeSelector(config.Document.Platform),
					"securityContext": kubeObject{
						"runAsUser": int64(ServiceUID), "runAsGroup": int64(ServiceGID), "runAsNonRoot": true,
						"seccompProfile": kubeObject{"type": "RuntimeDefault"},
					},
					"containers": []any{kubeObject{
						"name": component, "image": config.Document.Images.Service,
						"command": command, "args": args,
						"env":             append([]any{secretEnvironment("AGENTSERVER_V2_DATABASE_URL", config.Document.Secrets.Core, "database-url")}, extraEnv...),
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
