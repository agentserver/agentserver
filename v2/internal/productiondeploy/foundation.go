package productiondeploy

import "github.com/agentserver/agentserver/v2/internal/executorgateway"

const (
	coreComponent           = "agentserver-core"
	browserComponent        = "browser-gateway"
	executorComponent       = "executor-gateway"
	harnessComponent        = "harness-pool"
	llmproxyComponent       = "llmproxy"
	migrationComponent      = "agentserver-migrate"
	bootstrapComponent      = "agentserver-bootstrap"
	bootstrapServiceAccount = "agentserver-bootstrap"
)

func renderFoundation(context renderContext) []kubeObject {
	config := context.config
	items := []kubeObject{
		namespaceResource(config),
		serviceAccountResource(config, coreComponent),
		serviceAccountResource(config, browserComponent),
		serviceAccountResource(config, executorComponent),
		serviceAccountResource(config, harnessComponent),
		serviceAccountResource(config, llmproxyComponent),
		serviceAccountResource(config, bootstrapServiceAccount),
		configMapResource(config, context.bootstrapConfigName, map[string]string{
			"bootstrap.json": string(context.bootstrapJSON),
		}),
		configMapResource(config, context.harnessConfigName, map[string]string{
			"worker-deployment.json": string(context.workerJSON),
			"network-guard.json":     string(context.networkGuardJSON),
		}),
		internalService(config, coreComponent, config.Document.Services.Core),
		browserService(config),
		executorService(config),
		internalService(config, llmproxyComponent, config.Document.Services.LLMProxy),
		frontendHTTPRoute(config),
		browserHTTPRoute(config),
		executorHTTPRoute(config),
	}
	items = append(items, renderNetworkPolicies(context)...)
	return items
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
	service := config.Document.Services.BrowserGateway
	return kubeObject{
		"apiVersion": "v1", "kind": "Service",
		"metadata": metadata(browserComponent, config.Document.Namespace, componentLabels(browserComponent), nil),
		"spec": kubeObject{
			"type": "ClusterIP", "clusterIP": service.ClusterIP,
			"selector": selectorLabels(browserComponent),
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
	return httpRoute(config, "agentserver-frontend", config.Document.Ingress.FrontendHostname, browserComponent,
		config.Document.Services.BrowserGateway.Port, []kubeObject{
			pathMatch("Exact", "/"), pathMatch("Exact", "/index.html"), pathMatch("Exact", "/readyz"),
			pathMatch("PathPrefix", "/reference"), pathMatch("PathPrefix", "/auth"), pathMatch("PathPrefix", "/oauth2"),
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
