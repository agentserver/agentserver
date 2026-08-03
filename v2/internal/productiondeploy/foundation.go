package productiondeploy

import "github.com/agentserver/agentserver/v2/internal/executorgateway"

const (
	coreComponent           = "agentserver-core"
	platformComponent       = "platform-gateway"
	browserComponent        = "browser-gateway"
	executorComponent       = "executor-gateway"
	harnessComponent        = "harness-pool"
	llmproxyComponent       = "llmproxy"
	hydraComponent          = "hydra"
	hydraMigrationComponent = "hydra-migrate"
	hydraSetupComponent     = "hydra-client-setup"
	migrationComponent      = "agentserver-migrate"
	bootstrapComponent      = "agentserver-bootstrap"
	bootstrapServiceAccount = "agentserver-bootstrap"
)

func renderFoundation(context renderContext) []kubeObject {
	config := context.config
	items := []kubeObject{
		namespaceResource(config),
		serviceAccountResource(config, coreComponent),
		serviceAccountResource(config, platformComponent),
		serviceAccountResource(config, browserComponent),
		serviceAccountResource(config, executorComponent),
		serviceAccountResource(config, harnessComponent),
		serviceAccountResource(config, llmproxyComponent),
		serviceAccountResource(config, hydraComponent),
		serviceAccountResource(config, hydraSetupComponent),
		serviceAccountResource(config, bootstrapServiceAccount),
		configMapResource(config, context.bootstrapConfigName, map[string]string{
			"bootstrap.json": string(context.bootstrapJSON),
		}),
		configMapResource(config, context.harnessConfigName, map[string]string{
			"worker-deployment.json": string(context.workerJSON),
			"network-guard.json":     string(context.networkGuardJSON),
		}),
		internalService(config, coreComponent, config.Document.Services.Core),
		publicHTTPService(config, platformComponent, config.Document.Services.PlatformGateway),
		browserService(config),
		executorService(config),
		internalService(config, llmproxyComponent, config.Document.Services.LLMProxy),
		hydraService(config),
		frontendHTTPRoute(config),
		browserFrontendHTTPRoute(config),
		browserHTTPRoute(config),
		executorHTTPRoute(config),
		hydraHTTPRoute(config),
	}
	items = append(items, renderNetworkPolicies(context)...)
	return items
}

func hydraService(config LoadedConfig) kubeObject {
	service := config.Document.Services.Hydra
	return kubeObject{
		"apiVersion": "v1", "kind": "Service",
		"metadata": metadata(hydraComponent, config.Document.Namespace, componentLabels(hydraComponent), nil),
		"spec": kubeObject{
			"type": "ClusterIP", "clusterIP": service.ClusterIP,
			"selector": selectorLabels(hydraComponent),
			"ports": []any{
				kubeObject{"name": "http-public", "appProtocol": "http", "protocol": "TCP", "port": int(service.PublicPort), "targetPort": "http-public"},
				kubeObject{"name": "https-admin", "protocol": "TCP", "port": int(service.AdminPort), "targetPort": "https-admin"},
			},
		},
	}
}

func internalService(config LoadedConfig, component string, service InternalServiceDocument) kubeObject {
	return kubeObject{
		"apiVersion": "v1", "kind": "Service",
		"metadata": metadata(component, config.Document.Namespace, componentLabels(component), nil),
		"spec": kubeObject{
			"type": "ClusterIP", "clusterIP": service.ClusterIP,
			"selector": selectorLabels(component),
			"ports": []any{kubeObject{
				"name": "https", "protocol": "TCP", "port": int(service.Port), "targetPort": "https",
			}},
		},
	}
}

func browserService(config LoadedConfig) kubeObject {
	return publicHTTPService(config, browserComponent, config.Document.Services.BrowserGateway)
}

func publicHTTPService(config LoadedConfig, component string, service InternalServiceDocument) kubeObject {
	return kubeObject{
		"apiVersion": "v1", "kind": "Service",
		"metadata": metadata(component, config.Document.Namespace, componentLabels(component), nil),
		"spec": kubeObject{
			"type": "ClusterIP", "clusterIP": service.ClusterIP,
			"selector": selectorLabels(component),
			"ports": []any{kubeObject{
				"name": "http", "protocol": "TCP", "port": int(service.Port), "targetPort": "http",
			}},
		},
	}
}

func executorService(config LoadedConfig) kubeObject {
	service := config.Document.Services.ExecutorGateway
	return kubeObject{
		"apiVersion": "v1", "kind": "Service",
		"metadata": metadata(executorComponent, config.Document.Namespace, componentLabels(executorComponent), nil),
		"spec": kubeObject{
			"type": "ClusterIP", "clusterIP": service.ClusterIP,
			"selector": selectorLabels(executorComponent),
			"ports": []any{
				kubeObject{"name": "http-agentx", "protocol": "TCP", "port": int(service.PublicPort), "targetPort": "http-agentx"},
				kubeObject{"name": "https-mcp", "protocol": "TCP", "port": int(service.InternalPort), "targetPort": "https-mcp"},
			},
		},
	}
}

func frontendHTTPRoute(config LoadedConfig) kubeObject {
	return httpRoute(config, "agentserver-platform", config.Document.Ingress.FrontendHostname, platformComponent,
		config.Document.Services.PlatformGateway.Port, []kubeObject{
			pathMatch("Exact", "/"), pathMatch("Exact", "/index.html"), pathMatch("Exact", "/readyz"),
			pathMatch("PathPrefix", "/platform"), pathMatch("PathPrefix", "/auth"),
			pathMatch("PathPrefix", "/v2"),
		})
}

func browserFrontendHTTPRoute(config LoadedConfig) kubeObject {
	return httpRoute(config, "agentserver-browser", config.Document.Ingress.BrowserFrontendHostname, browserComponent,
		config.Document.Services.BrowserGateway.Port, []kubeObject{
			pathMatch("Exact", "/"), pathMatch("Exact", "/index.html"), pathMatch("Exact", "/readyz"),
			pathMatch("PathPrefix", "/reference"), pathMatch("Exact", "/auth/config"),
		})
}

func browserHTTPRoute(config LoadedConfig) kubeObject {
	return httpRoute(config, "agentserver-browser-api", config.Document.Ingress.BrowserHostname, browserComponent,
		config.Document.Services.BrowserGateway.Port, []kubeObject{pathMatch("PathPrefix", "/v2")})
}

func executorHTTPRoute(config LoadedConfig) kubeObject {
	return httpRoute(config, "agentserver-executor-agentx", config.Document.Ingress.ExecutorHostname, executorComponent,
		config.Document.Services.ExecutorGateway.PublicPort, []kubeObject{
			pathMatch("Exact", executorgateway.AgentxEnrollmentPath),
			pathMatch("Exact", executorgateway.AgentxChallengePath),
			pathMatch("Exact", executorgateway.AgentxConnectPath),
		})
}

func hydraHTTPRoute(config LoadedConfig) kubeObject {
	return httpRoute(config, "agentserver-hydra-public", config.Document.Ingress.HydraHostname, hydraComponent,
		config.Document.Services.Hydra.PublicPort, []kubeObject{pathMatch("PathPrefix", "/")})
}

func httpRoute(config LoadedConfig, name, hostname, backend string, port uint16, matches []kubeObject) kubeObject {
	matchValues := make([]any, len(matches))
	for index := range matches {
		matchValues[index] = matches[index]
	}
	return kubeObject{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "HTTPRoute",
		"metadata": metadata(name, config.Document.Namespace, map[string]string{
			"app.kubernetes.io/part-of": "agentserver-v2", "app.kubernetes.io/managed-by": "agentserver-deploy",
		}, nil),
		"spec": kubeObject{
			"parentRefs": []any{kubeObject{
				"group": "gateway.networking.k8s.io", "kind": "Gateway",
				"namespace": config.Document.Ingress.GatewayNamespace, "name": config.Document.Ingress.GatewayName,
				"sectionName": config.Document.Ingress.GatewaySection,
			}},
			"hostnames": []any{hostname},
			"rules": []any{kubeObject{
				"matches":     matchValues,
				"backendRefs": []any{kubeObject{"group": "", "kind": "Service", "name": backend, "port": int(port)}},
			}},
		},
	}
}

func pathMatch(kind, value string) kubeObject {
	return kubeObject{"path": kubeObject{"type": kind, "value": value}}
}
