package productiondeploy

import "github.com/agentserver/agentserver/v2/internal/publichttps"

const (
	productionPostgresClusterName = "agentserver-postgres"
	productionPostgresPort        = uint16(5432)
)

func renderNetworkPolicies(context renderContext) []kubeObject {
	config := context.config
	document := config.Document
	port := func(value uint16) []any { return []any{kubeObject{"protocol": "TCP", "port": int(value)}} }

	coreIngressComponents := []string{platformComponent, browserComponent, executorComponent, harnessComponent, llmproxyComponent}
	if managedExecutionActive(document.Managed) {
		coreIngressComponents = append(coreIngressComponents, sandboxComponent)
		if managedEgressAuthorizerEnabled(document.Managed) {
			coreIngressComponents = append(coreIngressComponents, egressComponent)
		}
	}
	coreIngress := ingressFromComponents(coreIngressComponents, document.Services.Core.Port)
	platformIngress := ingressFromGateway(document.Ingress, document.Services.PlatformGateway.Port)
	browserIngress := ingressFromGateway(document.Ingress, document.Services.BrowserGateway.Port)
	executorIngress := append(
		ingressFromGateway(document.Ingress, document.Services.ExecutorGateway.PublicPort),
		kubeObject{
			"from":  []any{kubeObject{"podSelector": kubeObject{"matchLabels": selectorLabels(harnessComponent)}}},
			"ports": port(document.Services.ExecutorGateway.InternalPort),
		},
	)
	llmIngress := ingressFromComponents([]string{harnessComponent}, document.Services.LLMProxy.Port)
	hydraIngress := append(
		ingressFromGateway(document.Ingress, document.Services.Hydra.PublicPort),
		ingressFromComponents([]string{coreComponent, hydraSetupComponent}, document.Services.Hydra.AdminPort)...,
	)

	dns := dnsEgress(document.Network)
	databaseEgress := append(append([]any(nil), dns...), postgresEgress()...)
	coreEgress := append(append([]any(nil), dns...), postgresEgress()...)
	coreEgress = append(coreEgress, externalEgress(document.Network.CoreExternalEgress)...)
	coreEgress = append(coreEgress, namespacedPodTCPEgress(
		document.Ingress.GatewayNamespace,
		document.Ingress.GatewayPodSelector,
		443,
	))
	coreEgress = append(coreEgress, publicHTTPSEgress()...)
	coreEgress = append(coreEgress, componentTCPEgress(hydraComponent, document.Services.Hydra.AdminPort))
	platformEgress := []any{componentTCPEgress(coreComponent, document.Services.Core.Port)}
	browserEgress := []any{componentTCPEgress(coreComponent, document.Services.Core.Port)}
	browserEgress = append(browserEgress, dns...)
	browserEgress = append(browserEgress, externalEgress(document.Network.BrowserExternalEgress)...)
	executorEgress := []any{componentTCPEgress(coreComponent, document.Services.Core.Port)}
	harnessEgress := []any{
		componentTCPEgress(coreComponent, document.Services.Core.Port),
		componentTCPEgress(executorComponent, document.Services.ExecutorGateway.InternalPort),
		componentTCPEgress(llmproxyComponent, document.Services.LLMProxy.Port),
	}
	harnessEgress = append(harnessEgress, dns...)
	harnessEgress = append(harnessEgress, externalEgress(document.Network.HarnessExternalEgress)...)
	// The production S3 hostname is served by the in-cluster Istio Gateway.
	// Authorize the stable Gateway workload identity instead of pinning the
	// rotating LoadBalancer/node addresses returned by DNS. Cilium evaluates
	// this peer after Service/LB translation, so Pod replacement and address
	// rotation do not require regenerating the production Chart.
	harnessEgress = append(harnessEgress, namespacedPodTCPEgress(
		document.Ingress.GatewayNamespace,
		document.Ingress.GatewayPodSelector,
		443,
	))
	llmEgress := []any{componentTCPEgress(coreComponent, document.Services.Core.Port)}
	llmEgress = append(llmEgress, dns...)
	llmEgress = append(llmEgress, publicHTTPSEgress()...)
	hydraEgress := append(append([]any(nil), dns...), postgresEgress()...)
	hydraSetupEgress := append(append([]any(nil), dns...), componentTCPEgress(hydraComponent, document.Services.Hydra.AdminPort))
	if managedExecutionActive(document.Managed) {
		executorEgress = append(executorEgress, componentTCPEgress(sandboxComponent, document.Services.SandboxGateway.Port))
		harnessEgress = append(harnessEgress, componentTCPEgress(sandboxComponent, document.Services.SandboxGateway.Port))
	}

	items := []kubeObject{
		networkPolicy(config, "agentserver-default-deny", managedNetworkSelector(), nil, nil),
		networkPolicy(config, "hydra-migrate-egress", matchComponent(hydraMigrationComponent), nil, databaseEgress),
		networkPolicy(config, "agentserver-migrate-egress", matchComponent(migrationComponent), nil, databaseEgress),
		networkPolicy(config, "hydra-client-setup-egress", matchComponent(hydraSetupComponent), nil, hydraSetupEgress),
		networkPolicy(config, "agentserver-bootstrap-egress", matchComponent(bootstrapComponent), nil, databaseEgress),
		networkPolicy(config, coreComponent, matchComponent(coreComponent), coreIngress, coreEgress),
		networkPolicy(config, platformComponent, matchComponent(platformComponent), platformIngress, platformEgress),
		networkPolicy(config, browserComponent, matchComponent(browserComponent), browserIngress, browserEgress),
		networkPolicy(config, executorComponent, matchComponent(executorComponent), executorIngress, executorEgress),
		networkPolicy(config, harnessComponent, matchComponent(harnessComponent), nil, harnessEgress),
		networkPolicy(config, llmproxyComponent, matchComponent(llmproxyComponent), llmIngress, llmEgress),
		networkPolicy(config, hydraComponent, matchComponent(hydraComponent), hydraIngress, hydraEgress),
	}
	if managedExecutionActive(document.Managed) {
		sandboxIngress := ingressFromComponents([]string{executorComponent, harnessComponent}, document.Services.SandboxGateway.Port)
		sandboxEgress := []any{componentTCPEgress(coreComponent, document.Services.Core.Port)}
		sandboxEgress = append(sandboxEgress, dns...)
		sandboxEgress = append(sandboxEgress, namespacedPodTCPEgress(
			ProductionTAEProxyNamespace,
			map[string]string{"app": ProductionTAEProxyPodApp},
			ProductionTAEProxyPort,
		))
		sandboxEgress = append(sandboxEgress, externalEgress(document.Network.SandboxExternalEgress)...)
		items = append(items,
			networkPolicy(config, "agentserver-managed-environment-bootstrap-egress", matchComponent(managedEnvironmentBootstrapComponent), nil, databaseEgress),
			networkPolicy(config, sandboxComponent, matchComponent(sandboxComponent), sandboxIngress, sandboxEgress),
		)
		if managedEgressAuthorizerEnabled(document.Managed) {
			egressIngress := ingressFromCIDRs(document.Network.EgressAuthorizerIngress, document.Services.EgressAuthorizer.Port)
			egressIngress = append(egressIngress, ingressFromGateway(document.Ingress, document.Services.EgressAuthorizer.Port)...)
			egressEgress := []any{componentTCPEgress(coreComponent, document.Services.Core.Port)}
			egressEgress = append(egressEgress, dns...)
			egressEgress = append(egressEgress, externalEgress(document.Network.EgressAuthorizerExternalEgress)...)
			items = append(items, networkPolicy(config, egressComponent, matchComponent(egressComponent), egressIngress, egressEgress))
		}
	} else if managedEgressAuthorizerEnabled(document.Managed) {
		egressIngress := ingressFromGateway(document.Ingress, document.Services.EgressAuthorizer.Port)
		items = append(items,
			networkPolicy(config, egressComponent, matchComponent(egressComponent), egressIngress, nil),
		)
	}
	return items
}

func publicHTTPSEgress() []any {
	except4 := make([]any, 0)
	except6 := make([]any, 0)
	for _, prefix := range publichttps.ForbiddenPrefixes() {
		if prefix.Addr().Is4() {
			except4 = append(except4, prefix.String())
		} else {
			except6 = append(except6, prefix.String())
		}
	}
	return []any{
		kubeObject{
			"to":    []any{kubeObject{"ipBlock": kubeObject{"cidr": "0.0.0.0/0", "except": except4}}},
			"ports": []any{kubeObject{"protocol": "TCP", "port": 443}},
		},
		kubeObject{
			"to":    []any{kubeObject{"ipBlock": kubeObject{"cidr": "::/0", "except": except6}}},
			"ports": []any{kubeObject{"protocol": "TCP", "port": 443}},
		},
	}
}

func managedNetworkSelector() kubeObject {
	return kubeObject{"matchLabels": kubeObject{"agentserver.dev/network": "managed"}}
}

func postgresEgress() []any {
	return []any{kubeObject{
		"to": []any{kubeObject{
			"podSelector": kubeObject{"matchLabels": kubeObject{"cnpg.io/cluster": productionPostgresClusterName}},
		}},
		"ports": []any{kubeObject{"protocol": "TCP", "port": int(productionPostgresPort)}},
	}}
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

func ingressFromGateway(document IngressDocument, port uint16) []any {
	return []any{kubeObject{
		"from": []any{kubeObject{
			"namespaceSelector": kubeObject{"matchLabels": kubeObject{
				"kubernetes.io/metadata.name": document.GatewayNamespace,
			}},
			"podSelector": kubeObject{"matchLabels": document.GatewayPodSelector},
		}},
		"ports": []any{kubeObject{"protocol": "TCP", "port": int(port)}},
	}}
}

func ingressFromCIDRs(prefixes []string, port uint16) []any {
	if len(prefixes) == 0 {
		return nil
	}
	from := make([]any, len(prefixes))
	for index, prefix := range prefixes {
		from[index] = kubeObject{"ipBlock": kubeObject{"cidr": prefix}}
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

func namespacedPodTCPEgress(namespace string, labels map[string]string, port uint16) kubeObject {
	return kubeObject{
		"to": []any{kubeObject{
			"namespaceSelector": kubeObject{"matchLabels": kubeObject{
				"kubernetes.io/metadata.name": namespace,
			}},
			"podSelector": kubeObject{"matchLabels": labels},
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
