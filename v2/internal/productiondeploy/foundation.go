package productiondeploy

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
		serviceAccountResource(config, coreComponent, config.Document.Objects.CoreRoleARN),
		serviceAccountResource(config, browserComponent, ""),
		serviceAccountResource(config, executorComponent, ""),
		serviceAccountResource(config, harnessComponent, config.Document.Objects.HarnessPoolRoleARN),
		serviceAccountResource(config, llmproxyComponent, ""),
		serviceAccountResource(config, bootstrapServiceAccount, ""),
		configMapResource(config, context.bootstrapConfigName, map[string]string{
			"bootstrap.json": string(context.bootstrapJSON),
		}),
		configMapResource(config, context.harnessConfigName, map[string]string{
			"worker-deployment.json": string(context.workerJSON),
			"network-guard.json":     string(context.networkGuardJSON),
		}),
		internalService(config, coreComponent, config.Document.Services.Core),
		browserPublicService(config),
		internalService(config, executorComponent, InternalServiceDocument{
			ClusterIP: config.Document.Services.ExecutorGateway.ClusterIP,
			Port:      config.Document.Services.ExecutorGateway.Port,
		}),
		executorPublicService(config),
		internalService(config, llmproxyComponent, config.Document.Services.LLMProxy),
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

func browserPublicService(config LoadedConfig) kubeObject {
	service := config.Document.Services.BrowserGateway
	return kubeObject{
		"apiVersion": "v1", "kind": "Service",
		"metadata": metadata(browserComponent, config.Document.Namespace, componentLabels(browserComponent), map[string]string{
			"external-dns.alpha.kubernetes.io/hostname": service.PublicHostname,
		}),
		"spec": kubeObject{
			"type": "LoadBalancer", "clusterIP": service.ClusterIP, "externalTrafficPolicy": "Local",
			"loadBalancerSourceRanges": stringSliceAny(config.Document.Network.BrowserIngressCIDRs),
			"selector":                 selectorLabels(browserComponent),
			"ports": []any{kubeObject{
				"name": "https", "protocol": "TCP", "port": 443, "targetPort": "https",
			}},
		},
	}
}

func executorPublicService(config LoadedConfig) kubeObject {
	service := config.Document.Services.ExecutorGateway
	return kubeObject{
		"apiVersion": "v1", "kind": "Service",
		"metadata": metadata(executorComponent+"-public", config.Document.Namespace, componentLabels(executorComponent), map[string]string{
			"external-dns.alpha.kubernetes.io/hostname": service.PublicHostname,
		}),
		"spec": kubeObject{
			"type": "LoadBalancer", "externalTrafficPolicy": "Local",
			"loadBalancerSourceRanges": stringSliceAny(config.Document.Network.ExecutorIngressCIDRs),
			"selector":                 selectorLabels(executorComponent),
			"ports": []any{kubeObject{
				"name": "https", "protocol": "TCP", "port": 443, "targetPort": "https",
			}},
		},
	}
}

func stringSliceAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}
