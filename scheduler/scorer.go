/*
Node Scorer
===========
Scores nodes based on gang member locality for the Scheduler Extender.
The scoring formula prioritizes co-location of dependent services.

Scoring Formula:
  Score = (GangMembersOnNode × 100) + (AvailableCPU × 10) + (AvailableMemory × 1)

This ensures that nodes hosting more gang members are strongly preferred,
with resource availability as a secondary tiebreaker.

Returns scores in Kubernetes Extender HostPriority format.
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

// NodeScorer scores nodes based on gang locality and resource availability
type NodeScorer struct {
	clientset   *kubernetes.Clientset
	gangManager *GangManager
}

// NewNodeScorer creates a new node scorer
func NewNodeScorer(clientset *kubernetes.Clientset, gangManager *GangManager) *NodeScorer {
	return &NodeScorer{
		clientset:   clientset,
		gangManager: gangManager,
	}
}

// ScoreForExtender scores all nodes for a pod in Extender-compatible format
func (ns *NodeScorer) ScoreForExtender(ctx context.Context, pod *v1.Pod, nodes *v1.NodeList, gang *Gang) []HostPriority {
	priorities := make([]HostPriority, 0, len(nodes.Items))

	for _, node := range nodes.Items {
		score := ns.scoreNode(ctx, pod, &node, gang)
		priorities = append(priorities, HostPriority{
			Host:  node.Name,
			Score: score,
		})
	}

	return priorities
}

// scoreNode calculates the placement score for a pod on a specific node
func (ns *NodeScorer) scoreNode(ctx context.Context, pod *v1.Pod, node *v1.Node, gang *Gang) int64 {
	localityScore := ns.calculateLocalityScore(ctx, node, gang)
	resourceScore := ns.calculateResourceScore(node, pod)

	totalScore := localityScore + resourceScore

	klog.V(3).Infof("Score for node %s: locality=%d, resource=%d, total=%d",
		node.Name, localityScore, resourceScore, totalScore)

	return totalScore
}

// calculateLocalityScore scores a node based on how many gang members run on it
// Gang members on node × 100 — this heavily favors co-location
func (ns *NodeScorer) calculateLocalityScore(ctx context.Context, node *v1.Node, gang *Gang) int64 {
	memberCount := ns.countGangMembersOnNode(ctx, node, gang)
	return int64(memberCount * 100)
}

// countGangMembersOnNode counts how many gang member pods are running on a node
func (ns *NodeScorer) countGangMembersOnNode(ctx context.Context, node *v1.Node, gang *Gang) int {
	if gang == nil || len(gang.Members) == 0 {
		return 0
	}

	// List pods running on this node
	pods, err := ns.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + node.Name,
	})
	if err != nil {
		klog.Warningf("Failed to list pods on node %s: %v", node.Name, err)
		return 0
	}

	// Count matching gang members
	count := 0
	for _, pod := range pods.Items {
		podService := extractServiceName(pod.Name)
		for _, gangMember := range gang.Members {
			if strings.EqualFold(podService, gangMember) {
				count++
				break
			}
		}
	}

	return count
}

// calculateResourceScore scores based on available CPU and memory
// CPU weight: 10 points per 100m available
// Memory weight: 1 point per 100Mi available
func (ns *NodeScorer) calculateResourceScore(node *v1.Node, pod *v1.Pod) int64 {
	alloc := node.Status.Allocatable

	cpuMillis := alloc.Cpu().MilliValue()
	memBytes := alloc.Memory().Value()

	// Normalize CPU: 10 points per 100m (1 core = 100 points)
	cpuScore := int64(cpuMillis / 100 * 10)

	// Normalize Memory: 1 point per 100Mi
	memScore := int64(memBytes / (100 * 1024 * 1024))

	// Cap individual scores to prevent overwhelming locality
	if cpuScore > 100 {
		cpuScore = 100
	}
	if memScore > 50 {
		memScore = 50
	}

	return cpuScore + memScore
}
