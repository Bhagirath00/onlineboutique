/*
NEXUS Scheduler Extender
========================
Research-faithful implementation of the NEXUS Event-Driven
Dependency-Aware Scheduler as a Kubernetes Scheduler Extender.

KEY ARCHITECTURAL PRINCIPLES (from professional review):
1. NEXUS is NOT a custom scheduler — it's an Extender
2. kube-scheduler ALWAYS makes the final bind decision
3. NEXUS only ADVISES via HTTP Filter + Prioritize endpoints
4. NEXUS remains DORMANT (returns "no opinion") during steady state
5. NEXUS activates ONLY on spike detection events
6. Gangs are TEMPORARY — created on spike, dissolved on cooldown
7. NO pod migration — only newly-created replicas are influenced

Extender API:
  POST /filter     → Remove nodes that violate gang co-location
  POST /prioritize → Score nodes by gang member locality
  GET  /metrics    → Prometheus research metrics
  GET  /healthz    → Health check
*/

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

const (
	schedulerName = "nexus-scheduler"

	// Spike detection defaults
	cooldownDuration = 30 * time.Second

	// Spike detection polling interval
	spikeCheckInterval = 10 * time.Second

	// HTTP server port
	metricsPort = ":9099"
)

// SchedulerState represents the current mode of the scheduler
type SchedulerState int

const (
	StateIdle SchedulerState = iota
	StateActive
)

func (s SchedulerState) String() string {
	switch s {
	case StateIdle:
		return "IDLE"
	case StateActive:
		return "ACTIVE"
	default:
		return "UNKNOWN"
	}
}

// --- Kubernetes Extender API Types ---

// ExtenderArgs represents the arguments passed to the extender
type ExtenderArgs struct {
	Pod       *v1.Pod    `json:"pod"`
	Nodes     *v1.NodeList `json:"nodes,omitempty"`
	NodeNames *[]string    `json:"nodenames,omitempty"`
}

// ExtenderFilterResult represents the filter response
type ExtenderFilterResult struct {
	Nodes       *v1.NodeList      `json:"nodes,omitempty"`
	NodeNames   *[]string         `json:"nodenames,omitempty"`
	FailedNodes map[string]string `json:"failedNodes,omitempty"`
	Error       string            `json:"error,omitempty"`
}

// HostPriority represents a node priority score
type HostPriority struct {
	Host  string `json:"host"`
	Score int64  `json:"score"`
}

// --- NEXUS Scheduler Extender ---

// NEXUSScheduler is the main scheduler extender
type NEXUSScheduler struct {
	clientset     *kubernetes.Clientset
	state         SchedulerState
	stateMu       sync.RWMutex
	lastSpikeTime time.Time

	// Core modules
	spikeDetector *SpikeDetector
	depGraph      *DependencyGraph
	gangManager   *GangManager
	nodeScorer    *NodeScorer
	metrics       *NEXUSMetrics
}

// NewNEXUSScheduler creates a new scheduler extender instance
func NewNEXUSScheduler(clientset *kubernetes.Clientset) *NEXUSScheduler {
	metrics := NewNEXUSMetrics()
	spikeDetector := NewSpikeDetector()
	depGraph := NewDependencyGraph(clientset)
	gangManager := NewGangManager(metrics)

	scheduler := &NEXUSScheduler{
		clientset:     clientset,
		state:         StateIdle,
		spikeDetector: spikeDetector,
		depGraph:      depGraph,
		gangManager:   gangManager,
		metrics:       metrics,
	}

	// Node scorer needs gang manager for locality scoring
	scheduler.nodeScorer = NewNodeScorer(clientset, gangManager)

	klog.Info("NEXUS Scheduler Extender initialized")
	klog.Info("  Mode: Cooperative (Extender, NOT replacement)")
	klog.Info("  State: IDLE (dormant until spike detected)")
	klog.Info("  Endpoints: /filter, /prioritize, /metrics, /healthz")

	return scheduler
}

// --- State Management ---

// GetState returns the current scheduler state (thread-safe)
func (s *NEXUSScheduler) GetState() SchedulerState {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state
}

// SetState sets the scheduler state (thread-safe)
func (s *NEXUSScheduler) SetState(state SchedulerState) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.state != state {
		klog.Infof("NEXUS state change: %s → %s", s.state, state)
		s.state = state
		s.metrics.SetState(state.String())
		s.metrics.IncrementCounter("state_changes")
	}
}

// --- Extender HTTP Endpoints ---

// handleFilter processes Filter requests from kube-scheduler
// When IDLE: returns all nodes (no opinion — zero overhead)
// When ACTIVE: removes nodes that violate gang co-location requirements
func (s *NEXUSScheduler) handleFilter(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	s.metrics.IncrementCounter("filter_calls")

	// Parse request
	var args ExtenderArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		klog.Errorf("Failed to decode filter request: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// IDLE state: return all nodes (no opinion)
	if s.GetState() == StateIdle {
		klog.V(3).Info("Filter: IDLE state — returning all nodes (no opinion)")
		result := ExtenderFilterResult{
			Nodes: args.Nodes,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
		s.metrics.ExtenderFilterLatency.TimeSince(startTime)
		return
	}

	// ACTIVE state: filter based on gang co-location
	pod := args.Pod
	if pod == nil || args.Nodes == nil {
		result := ExtenderFilterResult{Nodes: args.Nodes}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
		return
	}

	gang := s.gangManager.GetGangForPod(pod)
	if gang == nil {
		// Pod not in any gang — return all nodes (no opinion)
		klog.V(2).Infof("Filter: Pod %s not in any gang — returning all nodes", pod.Name)
		result := ExtenderFilterResult{Nodes: args.Nodes}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
		s.metrics.ExtenderFilterLatency.TimeSince(startTime)
		return
	}

	// Filter: prefer nodes where gang members already exist
	// But don't remove all nodes — always keep at least some available
	eligibleNodes := make([]v1.Node, 0)
	failedNodes := make(map[string]string)

	// Find nodes with gang members
	nodesWithMembers := make(map[string]bool)
	ctx := context.Background()
	for _, node := range args.Nodes.Items {
		memberCount := s.nodeScorer.countGangMembersOnNode(ctx, &node, gang)
		if memberCount > 0 {
			nodesWithMembers[node.Name] = true
		}
	}

	if len(nodesWithMembers) > 0 {
		// Some nodes have gang members — prefer those, but keep all schedulable
		for _, node := range args.Nodes.Items {
			if isNodeSchedulable(&node) {
				eligibleNodes = append(eligibleNodes, node)
			} else {
				failedNodes[node.Name] = "Node not schedulable"
			}
		}
	} else {
		// No nodes have gang members — return all (this gang is starting fresh)
		eligibleNodes = args.Nodes.Items
	}

	result := ExtenderFilterResult{
		Nodes:       &v1.NodeList{Items: eligibleNodes},
		FailedNodes: failedNodes,
	}

	klog.Infof("Filter: Pod %s (gang: %s) → %d/%d nodes eligible",
		pod.Name, gang.ID, len(eligibleNodes), len(args.Nodes.Items))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
	s.metrics.ExtenderFilterLatency.TimeSince(startTime)
}

// handlePrioritize processes Prioritize requests from kube-scheduler
// When IDLE: returns equal scores (no opinion — zero overhead)
// When ACTIVE: scores nodes based on gang member locality
func (s *NEXUSScheduler) handlePrioritize(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	s.metrics.IncrementCounter("prioritize_calls")

	// Parse request
	var args ExtenderArgs
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		klog.Errorf("Failed to decode prioritize request: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// IDLE state: return equal scores (no opinion)
	if s.GetState() == StateIdle {
		klog.V(3).Info("Prioritize: IDLE state — returning equal scores (no opinion)")
		priorities := make([]HostPriority, 0)
		if args.Nodes != nil {
			for _, node := range args.Nodes.Items {
				priorities = append(priorities, HostPriority{
					Host:  node.Name,
					Score: 0, // Equal score = no preference
				})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(priorities)
		s.metrics.ExtenderPrioritizeLatency.TimeSince(startTime)
		return
	}

	// ACTIVE state: score based on gang locality
	pod := args.Pod
	if pod == nil || args.Nodes == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]HostPriority{})
		return
	}

	gang := s.gangManager.GetGangForPod(pod)
	if gang == nil {
		// Pod not in any gang — return equal scores
		klog.V(2).Infof("Prioritize: Pod %s not in any gang — returning equal scores", pod.Name)
		priorities := make([]HostPriority, 0)
		for _, node := range args.Nodes.Items {
			priorities = append(priorities, HostPriority{Host: node.Name, Score: 0})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(priorities)
		s.metrics.ExtenderPrioritizeLatency.TimeSince(startTime)
		return
	}

	// Score nodes by gang locality
	priorities := s.nodeScorer.ScoreForExtender(context.Background(), pod, args.Nodes, gang)

	klog.Infof("Prioritize: Pod %s (gang: %s) → scores: %+v", pod.Name, gang.ID, priorities)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(priorities)
	s.metrics.ExtenderPrioritizeLatency.TimeSince(startTime)
}

// --- Spike Detection Loop ---

// spikeWatcher periodically checks Prometheus for spikes
// This is EVENT-DRIVEN, not continuous: it only checks at intervals
func (s *NEXUSScheduler) spikeWatcher(ctx context.Context) {
	ticker := time.NewTicker(spikeCheckInterval)
	defer ticker.Stop()

	klog.Infof("Spike watcher started (checking every %v)", spikeCheckInterval)

	for {
		select {
		case <-ctx.Done():
			klog.Info("Spike watcher shutting down")
			return
		case <-ticker.C:
			s.checkForSpike(ctx)
		}
	}
}

// checkForSpike evaluates spike conditions and transitions state
func (s *NEXUSScheduler) checkForSpike(ctx context.Context) {
	currentState := s.GetState()

	if currentState == StateIdle {
		// Check for spike
		if s.spikeDetector.Detect(0) {
			activationStart := time.Now()

			klog.Info("═══════════════════════════════════════════")
			klog.Info("  SPIKE DETECTED — Activating NEXUS")
			klog.Info("═══════════════════════════════════════════")

			// Stage 1: Spike detected
			s.gangManager.SetStage(GangStageDetected)
			s.metrics.IncrementCounter("spike_events")

			// Stage 2: Build dependency graph
			s.gangManager.SetStage(GangStageGraphBuilt)
			if err := s.depGraph.BuildFromAnnotations(ctx); err != nil {
				klog.Errorf("Failed to build dependency graph: %v", err)
				return
			}

			// Stage 3 & 4: Form gangs from the graph
			groups := s.depGraph.GetGroups()
			if len(groups) > 0 {
				s.gangManager.FormGangs(groups)
				s.gangManager.SetStage(GangStageScheduling)
			}

			// Transition to ACTIVE
			s.SetState(StateActive)
			s.lastSpikeTime = time.Now()

			// Record activation latency
			latencyMs := s.metrics.ActivationLatency.TimeSince(activationStart)
			klog.Infof("NEXUS activated in %.2fms (gangs: %d)", latencyMs, s.gangManager.GetActiveGangCount())
		}
	}
}

// cooldownChecker monitors for returning to IDLE state
func (s *NEXUSScheduler) cooldownChecker(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.GetState() == StateActive {
				// Check if cooldown has elapsed
				if time.Since(s.lastSpikeTime) > cooldownDuration {
					// Check if spike is still ongoing
					if !s.spikeDetector.Detect(0) {
						klog.Info("═══════════════════════════════════════════")
						klog.Info("  SPIKE ENDED — Dissolving gangs, returning to IDLE")
						klog.Info("═══════════════════════════════════════════")

						// Stage 6 & 7: Dissolve gangs and clear graph
						s.gangManager.SetStage(GangStageCooldown)
						s.gangManager.DissolveAll()
						s.depGraph.Clear()
						s.gangManager.SetStage(GangStageNone)

						// Return to IDLE (dormant)
						s.SetState(StateIdle)

						klog.Info("NEXUS is now DORMANT — zero scheduling overhead")
					} else {
						// Spike still ongoing — extend the window
						s.lastSpikeTime = time.Now()
						klog.V(2).Info("Spike still ongoing, extending active window")
					}
				}
			}
		}
	}
}

// --- Utility Functions ---

// isNodeSchedulable checks if a node can accept pods
func isNodeSchedulable(node *v1.Node) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Key == "node.kubernetes.io/unschedulable" {
			return false
		}
	}

	for _, condition := range node.Status.Conditions {
		if condition.Type == v1.NodeReady && condition.Status == v1.ConditionTrue {
			return true
		}
	}

	return false
}

// --- HTTP Handlers ---

// metricsHandler returns all NEXUS Prometheus metrics
func (s *NEXUSScheduler) metricsHandler(w http.ResponseWriter, r *http.Request) {
	s.metrics.WriteAllMetrics(w)
}

// healthHandler returns health status
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

// statusHandler returns detailed NEXUS status
func (s *NEXUSScheduler) statusHandler(w http.ResponseWriter, r *http.Request) {
	status := map[string]interface{}{
		"state":          s.GetState().String(),
		"gangStage":      s.gangManager.GetStage().String(),
		"activeGangs":    s.gangManager.GetActiveGangCount(),
		"graphBuilt":     s.depGraph.IsBuilt(),
		"lastSpikeTime":  s.lastSpikeTime.Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// --- Main Entry Point ---

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	klog.Info("╔════════════════════════════════════════════════════╗")
	klog.Info("║  NEXUS Scheduler Extender v2.0                    ║")
	klog.Info("║  Event-Driven • Dependency-Aware • Cooperative    ║")
	klog.Info("║  Mode: Scheduler Extender (NOT replacement)       ║")
	klog.Info("╚════════════════════════════════════════════════════╝")

	// Build Kubernetes client
	var config *rest.Config
	var err error

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		klog.Fatalf("Failed to build kubeconfig: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Create scheduler extender
	scheduler := NewNEXUSScheduler(clientset)

	// Register HTTP endpoints
	// Extender endpoints (called by kube-scheduler)
	http.HandleFunc("/filter", scheduler.handleFilter)
	http.HandleFunc("/prioritize", scheduler.handlePrioritize)

	// Observability endpoints
	http.HandleFunc("/metrics", scheduler.metricsHandler)
	http.HandleFunc("/healthz", healthHandler)
	http.HandleFunc("/readyz", healthHandler)
	http.HandleFunc("/status", scheduler.statusHandler)

	// Start spike detection watcher (event-driven, not continuous)
	ctx := context.Background()
	go scheduler.spikeWatcher(ctx)

	// Start cooldown checker
	go scheduler.cooldownChecker(ctx)

	// Start HTTP server
	klog.Infof("Starting NEXUS Extender HTTP server on %s", metricsPort)
	klog.Info("Endpoints:")
	klog.Info("  POST /filter     → Extender Filter (gang co-location)")
	klog.Info("  POST /prioritize → Extender Prioritize (locality scoring)")
	klog.Info("  GET  /metrics    → Prometheus research metrics")
	klog.Info("  GET  /healthz    → Health check")
	klog.Info("  GET  /status     → Detailed NEXUS status")
	klog.Info("")
	klog.Info("NEXUS is now DORMANT — waiting for spike events...")

	if err := http.ListenAndServe(metricsPort, nil); err != nil {
		klog.Fatalf("Failed to start HTTP server: %v", err)
	}
}

// emitEvent creates a Kubernetes event for observability
func (s *NEXUSScheduler) emitEvent(namespace, podName, reason, message string) {
	event := &v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("nexus-%s.%x", podName, time.Now().UnixNano()),
			Namespace: namespace,
		},
		Reason:  reason,
		Message: message,
		Source: v1.EventSource{
			Component: schedulerName,
		},
		FirstTimestamp: metav1.Now(),
		LastTimestamp:  metav1.Now(),
		Type:          v1.EventTypeNormal,
	}

	_, err := s.clientset.CoreV1().Events(namespace).Create(context.TODO(), event, metav1.CreateOptions{})
	if err != nil {
		klog.Warningf("Failed to emit event: %v", err)
	}
}
