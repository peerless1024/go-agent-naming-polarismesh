/*
 * Tencent is pleased to support the open source community by making
 * trpc-agent-naming-polarismesh available.
 *
 * Copyright (C) 2026 Tencent. All rights reserved.
 *
 * trpc-agent-naming-polarismesh is licensed under the Apache License Version 2.0.
 */

package metrics

import "trpc.group/trpc-go/trpc-go/metrics"

const agentPolarisMetricsKey = "trpc_agent_polaris"

// ReportInvalidAgentInstance reports an invalid A2A instance.
func ReportInvalidAgentInstance(namespace, service, instanceID string) {
	reportAgentResolverMetric("invalid_instance", namespace, service, instanceID)
}

// ReportAgentDiscoveryFallback reports a discovery fallback.
func ReportAgentDiscoveryFallback(namespace, service string) {
	reportAgentResolverMetric("discovery_fallback", namespace, service, "")
}

func reportAgentResolverMetric(name, namespace, service, instanceID string) {
	dimensions := []*metrics.Dimension{
		{Name: "namespace", Value: namespace},
		{Name: "service", Value: service},
		{Name: "instance", Value: instanceID},
	}
	indices := []*metrics.Metrics{
		metrics.NewMetrics("trpc.AgentPolarisResolver."+name, 1, metrics.PolicySUM),
	}
	_ = metrics.ReportMultiDimensionMetricsX(agentPolarisMetricsKey, dimensions, indices)
}
