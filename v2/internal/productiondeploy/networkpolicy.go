package productiondeploy

func renderNetworkPolicies(context renderContext) []kubeObject {
	config := context.config
	document := config.Document
	port := func(value uint16) []any { return []any{kubeObject{"protocol": "TCP", "port": int(value)}} }

	coreIngress := ingressFromComponents([]string{browserComponent, executorComponent, harnessComponent, llmproxyComponent}, document.Services.Core.Port)
	browserIngress := ingressFromCIDRs(document.Network.BrowserIngressCIDRs, document.Services.BrowserGateway.Port)
	executorIngress := append(
		ingressFromCIDRs(document.Network.ExecutorIngressCIDRs, document.Services.ExecutorGateway.Port),
		kubeObject{
			"from":  []any{kubeObject{"podSelector": kubeObject{"matchLabels": selectorLabels(harnessComponent)}}},
			"ports": port(document.Services.ExecutorGateway.Port),
		},
	)
	llmIngress := ingressFromComponents([]string{harnessComponent}, document.Services.LLMProxy.Port)

	dns := dnsEgress(document.Network)
	databaseEgress := append(append([]any(nil), dns...), externalEgress(document.Network.DatabaseEgress)...)
	coreEgress := append(append([]any(nil), dns...), externalEgress(document.Network.DatabaseEgress)...)
	coreEgress = append(coreEgress, externalEgress(document.Network.CoreExternalEgress)...)
	browserEgress := []any{componentTCPEgress(coreComponent, document.Services.Core.Port)}
	browserEgress = append(browserEgress, dns...)
	browserEgress = append(browserEgress, externalEgress(document.Network.BrowserExternalEgress)...)
	executorEgress := []any{componentTCPEgress(coreComponent, document.Services.Core.Port)}
	harnessEgress := []any{
		componentTCPEgress(coreComponent, document.Services.Core.Port),
		componentTCPEgress(executorComponent, document.Services.ExecutorGateway.Port),
		componentTCPEgress(llmproxyComponent, document.Services.LLMProxy.Port),
	}
	harnessEgress = append(harnessEgress, dns...)
	harnessEgress = append(harnessEgress, externalEgress(document.Network.HarnessExternalEgress)...)
	llmEgress := []any{componentTCPEgress(coreComponent, document.Services.Core.Port)}
	llmEgress = append(llmEgress, dns...)
	llmEgress = append(llmEgress, externalEgress(document.Network.LLMProxyExternalEgress)...)

	return []kubeObject{
		networkPolicy(config, "agentserver-default-deny", kubeObject{}, nil, nil),
		networkPolicy(config, "agentserver-migrate-egress", matchComponent(migrationComponent), nil, databaseEgress),
		networkPolicy(config, "agentserver-bootstrap-egress", matchComponent(bootstrapComponent), nil, databaseEgress),
		networkPolicy(config, coreComponent, matchComponent(coreComponent), coreIngress, coreEgress),
		networkPolicy(config, browserComponent, matchComponent(browserComponent), browserIngress, browserEgress),
		networkPolicy(config, executorComponent, matchComponent(executorComponent), executorIngress, executorEgress),
		networkPolicy(config, harnessComponent, matchComponent(harnessComponent), nil, harnessEgress),
		networkPolicy(config, llmproxyComponent, matchComponent(llmproxyComponent), llmIngress, llmEgress),
	}
}

func networkPolicy(config LoadedConfig, name string, selector kubeObject, ingress, egress []any) kubeObject {
	spec := kubeObject{
		"podSelector": selector,
		"policyTypes": []any{"Ingress", "Egress"},
	}
	if len(ingress) != 0 {
		spec["ingress"] = ingress
	}
	if len(egress) != 0 {
		spec["egress"] = egress
	}
	return kubeObject{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": metadata(name, config.Document.Namespace, map[string]string{
			"app.kubernetes.io/part-of":    "agentserver-v2",
			"app.kubernetes.io/managed-by": "agentserver-deploy",
		}, nil),
		"spec": spec,
	}
}

func matchComponent(component string) kubeObject {
	return kubeObject{"matchLabels": selectorLabels(component)}
}

func ingressFromComponents(components []string, port uint16) []any {
	from := make([]any, len(components))
	for index, component := range components {
		from[index] = kubeObject{"podSelector": kubeObject{"matchLabels": selectorLabels(component)}}
	}
	return []any{kubeObject{
		"from":  from,
		"ports": []any{kubeObject{"protocol": "TCP", "port": int(port)}},
	}}
}

func ingressFromCIDRs(cidrs []string, port uint16) []any {
	from := make([]any, len(cidrs))
	for index, cidr := range cidrs {
		from[index] = kubeObject{"ipBlock": kubeObject{"cidr": cidr}}
	}
	return []any{kubeObject{
		"from":  from,
		"ports": []any{kubeObject{"protocol": "TCP", "port": int(port)}},
	}}
}

func dnsEgress(document NetworkDocument) []any {
	return []any{kubeObject{
		"to": []any{
			// Some CNIs apply NetworkPolicy before Service DNAT and others after
			// it. Keep both exact authorities so DNS remains available without
			// opening arbitrary namespace egress.
			kubeObject{"ipBlock": kubeObject{"cidr": document.DNSClusterIP + "/32"}},
			kubeObject{
				"namespaceSelector": kubeObject{"matchLabels": kubeObject{
					"kubernetes.io/metadata.name": document.DNSNamespace,
				}},
				"podSelector": kubeObject{"matchLabels": document.DNSPodSelector},
			},
		},
		"ports": []any{
			kubeObject{"protocol": "UDP", "port": 53},
			kubeObject{"protocol": "TCP", "port": 53},
		},
	}}
}

func componentTCPEgress(component string, port uint16) kubeObject {
	return kubeObject{
		"to": []any{kubeObject{
			"podSelector": kubeObject{"matchLabels": selectorLabels(component)},
		}},
		"ports": []any{kubeObject{"protocol": "TCP", "port": int(port)}},
	}
}

func externalEgress(rules []EgressRuleDocument) []any {
	result := make([]any, len(rules))
	for index, rule := range rules {
		ports := make([]any, len(rule.Ports))
		for portIndex, port := range rule.Ports {
			ports[portIndex] = kubeObject{"protocol": "TCP", "port": int(port)}
		}
		result[index] = kubeObject{
			"to":    []any{kubeObject{"ipBlock": kubeObject{"cidr": rule.CIDR}}},
			"ports": ports,
		}
	}
	return result
}
