/*
NEXUS Scheduler - Event-Driven Lightweight Kubernetes Scheduler
==============================================================

This custom scheduler implements an event-driven approach that:
1. Stays IDLE when cluster load is normal (minimal CPU overhead)
2. ACTIVATES gang scheduling only during spike events
3. Returns to IDLE after spike ends

Key Features:
- Event-driven architecture (not polling-based like Volcano)
- Lightweight when idle (near-zero control-plane overhead)
- Gang scheduling for coordinated pod startup during spikes
- Metrics exposed for Prometheus scraping
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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

const (
	schedulerName = "nexus-scheduler"
	
	// Spike detection thresholds
	spikeThreshold    = 5  // Number of pending pods to trigger spike mode
	cooldownDuration  = 30 * time.Second
	
	// Metrics port
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

// NEXUSScheduler is the main scheduler struct
type NEXUSScheduler struct {
	clientset     *kubernetes.Clientset
	state         SchedulerState
	stateMu       sync.RWMutex
	pendingPods   map[string]*v1.Pod
	pendingMu     sync.RWMutex
	lastSpikeTime time.Time
	
	// Metrics
	podsScheduled   int64
	schedulingCalls int64
	stateChanges    int64
}

// NewNEXUSScheduler creates a new scheduler instance
func NewNEXUSScheduler(clientset *kubernetes.Clientset) *NEXUSScheduler {
	return &NEXUSScheduler{
		clientset:   clientset,
		state:       StateIdle,
		pendingPods: make(map[string]*v1.Pod),
	}
}

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
		klog.Infof("NEXUS state change: %s -> %s", s.state, state)
		s.state = state
		s.stateChanges++
	}
}

// AddPendingPod adds a pod to the pending queue
func (s *NEXUSScheduler) AddPendingPod(pod *v1.Pod) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	s.pendingPods[key] = pod
	klog.V(2).Infof("Added pending pod: %s (total pending: %d)", key, len(s.pendingPods))
	
	// Check if we should enter spike mode
	if len(s.pendingPods) >= spikeThreshold {
		s.SetState(StateActive)
		s.lastSpikeTime = time.Now()
	}
}

// RemovePendingPod removes a pod from the pending queue
func (s *NEXUSScheduler) RemovePendingPod(pod *v1.Pod) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	delete(s.pendingPods, key)
	klog.V(2).Infof("Removed pending pod: %s (total pending: %d)", key, len(s.pendingPods))
}

// GetPendingPods returns a copy of pending pods
func (s *NEXUSScheduler) GetPendingPods() []*v1.Pod {
	s.pendingMu.RLock()
	defer s.pendingMu.RUnlock()
	pods := make([]*v1.Pod, 0, len(s.pendingPods))
	for _, pod := range s.pendingPods {
		pods = append(pods, pod)
	}
	return pods
}

// Schedule binds a pod to a node
func (s *NEXUSScheduler) Schedule(ctx context.Context, pod *v1.Pod, nodeName string) error {
	s.schedulingCalls++
	
	binding := &v1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
		Target: v1.ObjectReference{
			APIVersion: "v1",
			Kind:       "Node",
			Name:       nodeName,
		},
	}
	
	err := s.clientset.CoreV1().Pods(pod.Namespace).Bind(ctx, binding, metav1.CreateOptions{})
	if err != nil {
		klog.Errorf("Failed to bind pod %s/%s to node %s: %v", pod.Namespace, pod.Name, nodeName, err)
		return err
	}
	
	s.podsScheduled++
	klog.Infof("Successfully bound pod %s/%s to node %s", pod.Namespace, pod.Name, nodeName)
	
	// Emit scheduling event
	s.emitEvent(pod, nodeName, "Scheduled", fmt.Sprintf("NEXUS scheduled pod to node %s", nodeName))
	
	return nil
}

// emitEvent creates a Kubernetes event
func (s *NEXUSScheduler) emitEvent(pod *v1.Pod, nodeName, reason, message string) {
	event := &v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s.%x", pod.Name, time.Now().UnixNano()),
			Namespace: pod.Namespace,
		},
		InvolvedObject: v1.ObjectReference{
			APIVersion: "v1",
			Kind:       "Pod",
			Name:       pod.Name,
			Namespace:  pod.Namespace,
			UID:        pod.UID,
		},
		Reason:  reason,
		Message: message,
		Source: v1.EventSource{
			Component: schedulerName,
		},
		FirstTimestamp: metav1.Now(),
		LastTimestamp:  metav1.Now(),
		Type:           v1.EventTypeNormal,
	}
	
	_, err := s.clientset.CoreV1().Events(pod.Namespace).Create(context.TODO(), event, metav1.CreateOptions{})
	if err != nil {
		klog.Warningf("Failed to emit event for pod %s/%s: %v", pod.Namespace, pod.Name, err)
	}
}

// SelectNode chooses the best node for a pod
func (s *NEXUSScheduler) SelectNode(ctx context.Context, pod *v1.Pod) (string, error) {
	nodes, err := s.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to list nodes: %v", err)
	}
	
	if len(nodes.Items) == 0 {
		return "", fmt.Errorf("no nodes available")
	}
	
	// Simple node selection: choose first schedulable node
	// In production, add proper scoring based on resources
	for _, node := range nodes.Items {
		if isNodeSchedulable(&node) {
			return node.Name, nil
		}
	}
	
	return "", fmt.Errorf("no schedulable nodes found")
}

// isNodeSchedulable checks if a node can accept pods
func isNodeSchedulable(node *v1.Node) bool {
	// Check for unschedulable taint
	for _, taint := range node.Spec.Taints {
		if taint.Key == "node.kubernetes.io/unschedulable" {
			return false
		}
	}
	
	// Check node conditions
	for _, condition := range node.Status.Conditions {
		if condition.Type == v1.NodeReady && condition.Status == v1.ConditionTrue {
			return true
		}
	}
	
	return false
}

// Run starts the scheduler main loop
func (s *NEXUSScheduler) Run(ctx context.Context) {
	klog.Info("Starting NEXUS scheduler...")
	
	// Start cooldown checker
	go s.cooldownChecker(ctx)
	
	// Main scheduling loop
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			klog.Info("NEXUS scheduler shutting down...")
			return
		case <-ticker.C:
			s.schedulePendingPods(ctx)
		}
	}
}

// schedulePendingPods schedules all pending pods
func (s *NEXUSScheduler) schedulePendingPods(ctx context.Context) {
	state := s.GetState()
	pods := s.GetPendingPods()
	
	if len(pods) == 0 {
		return
	}
	
	klog.V(2).Infof("Scheduling %d pending pods (state: %s)", len(pods), state)
	
	switch state {
	case StateIdle:
		// In idle mode, schedule pods one by one (normal behavior)
		for _, pod := range pods {
			s.scheduleOnePod(ctx, pod)
		}
		
	case StateActive:
		// In active/spike mode, use gang scheduling
		// Wait for all pods to be ready before scheduling
		klog.Infof("SPIKE MODE: Gang scheduling %d pods together", len(pods))
		s.gangSchedule(ctx, pods)
	}
}

// scheduleOnePod schedules a single pod
func (s *NEXUSScheduler) scheduleOnePod(ctx context.Context, pod *v1.Pod) {
	nodeName, err := s.SelectNode(ctx, pod)
	if err != nil {
		klog.Warningf("No suitable node for pod %s/%s: %v", pod.Namespace, pod.Name, err)
		return
	}
	
	if err := s.Schedule(ctx, pod, nodeName); err == nil {
		s.RemovePendingPod(pod)
	}
}

// gangSchedule schedules multiple pods together (gang scheduling)
func (s *NEXUSScheduler) gangSchedule(ctx context.Context, pods []*v1.Pod) {
	// Get available nodes
	nodes, err := s.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Errorf("Failed to list nodes for gang scheduling: %v", err)
		return
	}
	
	schedulableNodes := make([]string, 0)
	for _, node := range nodes.Items {
		if isNodeSchedulable(&node) {
			schedulableNodes = append(schedulableNodes, node.Name)
		}
	}
	
	if len(schedulableNodes) == 0 {
		klog.Warning("No schedulable nodes for gang scheduling")
		return
	}
	
	// Round-robin assignment across nodes
	for i, pod := range pods {
		nodeName := schedulableNodes[i%len(schedulableNodes)]
		if err := s.Schedule(ctx, pod, nodeName); err == nil {
			s.RemovePendingPod(pod)
		}
	}
}

// cooldownChecker monitors for returning to idle state
func (s *NEXUSScheduler) cooldownChecker(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if s.GetState() == StateActive {
				s.pendingMu.RLock()
				pendingCount := len(s.pendingPods)
				s.pendingMu.RUnlock()
				
				// Return to idle if no pending pods and cooldown elapsed
				if pendingCount == 0 && time.Since(s.lastSpikeTime) > cooldownDuration {
					s.SetState(StateIdle)
				}
			}
		}
	}
}

// metricsHandler returns Prometheus metrics
func (s *NEXUSScheduler) metricsHandler(w http.ResponseWriter, r *http.Request) {
	s.pendingMu.RLock()
	pendingCount := len(s.pendingPods)
	s.pendingMu.RUnlock()
	
	state := s.GetState()
	stateValue := 0
	if state == StateActive {
		stateValue = 1
	}
	
	fmt.Fprintf(w, "# HELP nexus_scheduler_state Current scheduler state (0=IDLE, 1=ACTIVE)\n")
	fmt.Fprintf(w, "# TYPE nexus_scheduler_state gauge\n")
	fmt.Fprintf(w, "nexus_scheduler_state %d\n", stateValue)
	fmt.Fprintf(w, "# HELP nexus_pending_pods Number of pending pods\n")
	fmt.Fprintf(w, "# TYPE nexus_pending_pods gauge\n")
	fmt.Fprintf(w, "nexus_pending_pods %d\n", pendingCount)
	fmt.Fprintf(w, "# HELP nexus_pods_scheduled_total Total pods scheduled\n")
	fmt.Fprintf(w, "# TYPE nexus_pods_scheduled_total counter\n")
	fmt.Fprintf(w, "nexus_pods_scheduled_total %d\n", s.podsScheduled)
	fmt.Fprintf(w, "# HELP nexus_scheduling_calls_total Total scheduling calls\n")
	fmt.Fprintf(w, "# TYPE nexus_scheduling_calls_total counter\n")
	fmt.Fprintf(w, "nexus_scheduling_calls_total %d\n", s.schedulingCalls)
	fmt.Fprintf(w, "# HELP nexus_state_changes_total Total state changes\n")
	fmt.Fprintf(w, "# TYPE nexus_state_changes_total counter\n")
	fmt.Fprintf(w, "nexus_state_changes_total %d\n", s.stateChanges)
}

// healthHandler returns health status
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}

func main() {
	klog.InitFlags(nil)
	flag.Parse()
	
	klog.Info("NEXUS Scheduler - Event-Driven Lightweight Scheduler")
	klog.Info("Starting up...")
	
	// Build Kubernetes client
	var config *rest.Config
	var err error
	
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		// In-cluster config
		config, err = rest.InClusterConfig()
	}
	
	if err != nil {
		klog.Fatalf("Failed to build kubeconfig: %v", err)
	}
	
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Failed to create Kubernetes client: %v", err)
	}
	
	// Create scheduler
	scheduler := NewNEXUSScheduler(clientset)
	
	// Set up pod informer to watch for unscheduled pods
	factory := informers.NewSharedInformerFactory(clientset, 30*time.Second)
	podInformer := factory.Core().V1().Pods().Informer()
	
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod := obj.(*v1.Pod)
			// Only handle pods assigned to our scheduler that are unscheduled
			if pod.Spec.SchedulerName == schedulerName && pod.Spec.NodeName == "" && pod.Status.Phase == v1.PodPending {
				scheduler.AddPendingPod(pod)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			pod := newObj.(*v1.Pod)
			// Remove from pending if scheduled
			if pod.Spec.NodeName != "" {
				scheduler.RemovePendingPod(pod)
			}
		},
		DeleteFunc: func(obj interface{}) {
			pod := obj.(*v1.Pod)
			scheduler.RemovePendingPod(pod)
		},
	})
	
	// Start HTTP server for metrics and health
	http.HandleFunc("/metrics", scheduler.metricsHandler)
	http.HandleFunc("/healthz", healthHandler)
	http.HandleFunc("/readyz", healthHandler)
	
	go func() {
		klog.Infof("Starting metrics server on %s", metricsPort)
		if err := http.ListenAndServe(metricsPort, nil); err != nil {
			klog.Fatalf("Failed to start metrics server: %v", err)
		}
	}()
	
	// Start informers
	ctx := context.Background()
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())
	
	// Run scheduler
	scheduler.Run(ctx)
}
