/*
Dependency Graph Engine
======================
Builds an in-memory Directed Acyclic Graph (DAG) from pod annotations
at RUNTIME during spike events. The graph is ephemeral - it exists
only during the spike window and is cleared on dissolution.

Required by Section 2D of the professional review:
"Graph must not be static YAML-only. The graph must exist in
memory only during spike window."

Annotations used:
  nexus.io/depends-on: "paymentservice,currencyservice"
  nexus.io/service-group: "checkout-flow"

If no annotations are found, falls back to well-known Online Boutique
dependency patterns for the research experiment.
*/

package main

import (
	"context"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	// Annotation keys for dependency declaration
	AnnotationDependsOn    = "nexus.io/depends-on"
	AnnotationServiceGroup = "nexus.io/service-group"
)

// RuntimeGroup represents a dynamically-discovered coordination group
type RuntimeGroup struct {
	Name     string
	Services []string
}

// DependencyGraph builds and holds the in-memory service DAG
type DependencyGraph struct {
	clientset *kubernetes.Clientset
	groups    []RuntimeGroup
	built     bool
}

// NewDependencyGraph creates a new (empty) dependency graph
func NewDependencyGraph(clientset *kubernetes.Clientset) *DependencyGraph {
	return &DependencyGraph{
		clientset: clientset,
		groups:    make([]RuntimeGroup, 0),
		built:     false,
	}
}

// BuildFromAnnotations scans all pods in the cluster for nexus.io annotations
// and constructs the dependency graph at runtime
func (dg *DependencyGraph) BuildFromAnnotations(ctx context.Context) error {
	klog.Info("Building dependency graph from pod annotations...")

	// List all pods across all namespaces
	pods, err := dg.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	// Build groups from annotations
	groupMap := make(map[string]map[string]bool) // groupName → set of services

	for _, pod := range pods.Items {
		if pod.Annotations == nil {
			continue
		}

		// Check for service group annotation
		groupName := pod.Annotations[AnnotationServiceGroup]
		if groupName == "" {
			continue
		}

		serviceName := extractServiceName(pod.Name)

		if _, exists := groupMap[groupName]; !exists {
			groupMap[groupName] = make(map[string]bool)
		}
		groupMap[groupName][serviceName] = true

		// Also add dependencies declared via depends-on
		if depsStr, ok := pod.Annotations[AnnotationDependsOn]; ok {
			deps := strings.Split(depsStr, ",")
			for _, dep := range deps {
				dep = strings.TrimSpace(dep)
				if dep != "" {
					groupMap[groupName][dep] = true
				}
			}
		}
	}

	// Convert map to RuntimeGroups
	dg.groups = make([]RuntimeGroup, 0, len(groupMap))
	for name, services := range groupMap {
		svcList := make([]string, 0, len(services))
		for svc := range services {
			svcList = append(svcList, svc)
		}

		dg.groups = append(dg.groups, RuntimeGroup{
			Name:     name,
			Services: svcList,
		})
		klog.Infof("Discovered coordination group '%s': %v", name, svcList)
	}

	// If no annotations found, use well-known defaults for the experiment
	if len(dg.groups) == 0 {
		klog.Info("No annotations found, using well-known Online Boutique dependencies")
		dg.loadExperimentDefaults()
	}

	dg.built = true
	klog.Infof("Dependency graph built: %d coordination groups", len(dg.groups))
	return nil
}

// loadExperimentDefaults sets up well-known dependencies for the research
// These are used ONLY when no pod annotations exist (experiment mode)
func (dg *DependencyGraph) loadExperimentDefaults() {
	dg.groups = []RuntimeGroup{
		{
			Name:     "checkout-flow",
			Services: []string{"cartservice", "paymentservice", "checkoutservice", "currencyservice"},
		},
		{
			Name:     "product-browsing",
			Services: []string{"frontend", "productcatalogservice", "recommendationservice"},
		},
	}

	klog.Info("Loaded experiment defaults:")
	for _, group := range dg.groups {
		klog.Infof("  - %s: %v", group.Name, group.Services)
	}
}

// GetGroups returns all discovered coordination groups
func (dg *DependencyGraph) GetGroups() []RuntimeGroup {
	return dg.groups
}

// IsBuilt returns whether the graph has been constructed
func (dg *DependencyGraph) IsBuilt() bool {
	return dg.built
}

// GetGroup returns the coordination group for a given pod
func (dg *DependencyGraph) GetGroup(pod *v1.Pod) *RuntimeGroup {
	serviceName := extractServiceName(pod.Name)
	for i := range dg.groups {
		for _, svc := range dg.groups[i].Services {
			if svc == serviceName {
				return &dg.groups[i]
			}
		}
	}
	return nil
}

// IsInGroup checks if a pod belongs to any coordination group
func (dg *DependencyGraph) IsInGroup(pod *v1.Pod) bool {
	return dg.GetGroup(pod) != nil
}

// GetGroupMembers returns all service names in the same group as the pod
func (dg *DependencyGraph) GetGroupMembers(pod *v1.Pod) []string {
	group := dg.GetGroup(pod)
	if group == nil {
		return []string{}
	}
	return group.Services
}

// Clear frees all in-memory graph data
// Called when spike window ends and gang is dissolved
func (dg *DependencyGraph) Clear() {
	dg.groups = make([]RuntimeGroup, 0)
	dg.built = false
	klog.Info("Dependency graph cleared — all in-memory DAG data freed")
}

// extractServiceName extracts the service name from a pod name
// Online Boutique pods follow pattern: servicename-hash-hash
func extractServiceName(podName string) string {
	parts := strings.Split(podName, "-")
	if len(parts) >= 3 {
		// Handle multi-word service names like "redis-cart"
		// Try to match known patterns
		for i := len(parts) - 1; i >= 2; i-- {
			candidate := strings.Join(parts[:i], "-")
			if isKnownService(candidate) {
				return candidate
			}
		}
		// Default: use first part
		return parts[0]
	}
	if len(parts) > 0 {
		return parts[0]
	}
	return podName
}

// isKnownService checks if a name matches a known Online Boutique service
func isKnownService(name string) bool {
	known := map[string]bool{
		"cartservice":           true,
		"paymentservice":        true,
		"checkoutservice":       true,
		"currencyservice":       true,
		"frontend":              true,
		"productcatalogservice": true,
		"recommendationservice": true,
		"emailservice":          true,
		"shippingservice":       true,
		"adservice":             true,
		"redis-cart":            true,
		"loadgenerator":         true,
	}
	return known[name]
}
