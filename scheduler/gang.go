/*
Gang Lifecycle Manager
=====================
Implements explicit temporary gang lifecycle as required by
Section 2B of the professional review:

  Stage 1: Spike detected
  Stage 2: Dependency graph constructed
  Stage 3: Critical path identified
  Stage 4: Temporary gang created
  Stage 5: Scheduling hint injected (via Extender)
  Stage 6: Spike window ends
  Stage 7: Gang dissolved, system returns to default scheduling

KEY CONSTRAINT: Gangs are EPHEMERAL. They exist only in memory
during the spike window and are completely dissolved afterward.
*/

package main

import (
	"fmt"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
)

// GangStage represents the current lifecycle stage
type GangStage int

const (
	GangStageNone       GangStage = iota // No gang exists
	GangStageDetected                    // Spike detected
	GangStageGraphBuilt                  // Dependency graph constructed
	GangStageFormed                      // Gang created
	GangStageScheduling                  // Scheduling hints active
	GangStageCooldown                    // Spike ended, waiting
	GangStageDissolved                   // Gang dissolved
)

func (s GangStage) String() string {
	switch s {
	case GangStageNone:
		return "NONE"
	case GangStageDetected:
		return "SPIKE_DETECTED"
	case GangStageGraphBuilt:
		return "GRAPH_BUILT"
	case GangStageFormed:
		return "GANG_FORMED"
	case GangStageScheduling:
		return "SCHEDULING"
	case GangStageCooldown:
		return "COOLDOWN"
	case GangStageDissolved:
		return "DISSOLVED"
	default:
		return "UNKNOWN"
	}
}

// Gang represents a temporary group of services to be co-located
type Gang struct {
	ID        string            // Unique gang identifier
	Members   []string          // Service names in this gang
	NodePrefs map[string]int    // Node name → count of gang members on it
	CreatedAt time.Time
	Stage     GangStage
}

// GangManager handles the formation and dissolution of temporary gangs
type GangManager struct {
	mu           sync.RWMutex
	activeGangs  map[string]*Gang // gangID → Gang
	serviceToGang map[string]string // serviceName → gangID
	stage        GangStage
	metrics      *NEXUSMetrics
}

// NewGangManager creates a new gang lifecycle manager
func NewGangManager(metrics *NEXUSMetrics) *GangManager {
	return &GangManager{
		activeGangs:   make(map[string]*Gang),
		serviceToGang: make(map[string]string),
		stage:         GangStageNone,
		metrics:       metrics,
	}
}

// GetStage returns the current gang lifecycle stage
func (gm *GangManager) GetStage() GangStage {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return gm.stage
}

// FormGangs creates temporary gangs from the dependency graph
// This is called when a spike is detected and the DAG is built
func (gm *GangManager) FormGangs(groups []RuntimeGroup) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	formStart := time.Now()

	// Clear any existing gangs first
	gm.clearGangsLocked()

	for _, group := range groups {
		gangID := fmt.Sprintf("gang-%s-%d", group.Name, time.Now().UnixNano())

		gang := &Gang{
			ID:        gangID,
			Members:   group.Services,
			NodePrefs: make(map[string]int),
			CreatedAt: time.Now(),
			Stage:     GangStageFormed,
		}

		gm.activeGangs[gangID] = gang

		for _, svc := range group.Services {
			gm.serviceToGang[svc] = gangID
		}

		klog.Infof("GANG FORMED: %s with members %v", gangID, group.Services)
	}

	gm.stage = GangStageFormed

	// Record formation latency
	latencyMs := gm.metrics.GangFormationLatency.TimeSince(formStart)
	klog.Infof("Gang formation completed in %.2fms (%d gangs)", latencyMs, len(groups))
	gm.metrics.IncrementCounter("gangs_formed")
}

// GetGangForService returns the gang a service belongs to (if any)
func (gm *GangManager) GetGangForService(serviceName string) *Gang {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	gangID, exists := gm.serviceToGang[serviceName]
	if !exists {
		return nil
	}
	return gm.activeGangs[gangID]
}

// GetGangForPod returns the gang a pod belongs to based on its service name
func (gm *GangManager) GetGangForPod(pod *v1.Pod) *Gang {
	serviceName := extractServiceName(pod.Name)
	return gm.GetGangForService(serviceName)
}

// GetGangMembers returns all service names in the same gang as the pod
func (gm *GangManager) GetGangMembers(pod *v1.Pod) []string {
	gang := gm.GetGangForPod(pod)
	if gang == nil {
		return nil
	}
	return gang.Members
}

// UpdateNodePreference records that a gang member was placed on a node
func (gm *GangManager) UpdateNodePreference(serviceName, nodeName string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	gangID, exists := gm.serviceToGang[serviceName]
	if !exists {
		return
	}

	gang := gm.activeGangs[gangID]
	if gang != nil {
		gang.NodePrefs[nodeName]++
		klog.V(2).Infof("Updated node preference for gang %s: %s → %s (count: %d)",
			gangID, serviceName, nodeName, gang.NodePrefs[nodeName])
	}
}

// HasActiveGangs returns true if any gangs are currently active
func (gm *GangManager) HasActiveGangs() bool {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return len(gm.activeGangs) > 0
}

// GetActiveGangCount returns the number of active gangs
func (gm *GangManager) GetActiveGangCount() int {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return len(gm.activeGangs)
}

// DissolveAll dissolves all active gangs and clears all in-memory data
// This is called when the spike window ends and cooldown is complete
func (gm *GangManager) DissolveAll() {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	gangCount := len(gm.activeGangs)
	if gangCount == 0 {
		return
	}

	gm.clearGangsLocked()
	gm.stage = GangStageDissolved

	klog.Infof("GANGS DISSOLVED: %d gangs removed, all in-memory data freed", gangCount)
	gm.metrics.IncrementCounter("gangs_dissolved")
}

// clearGangsLocked clears all gang data (must hold write lock)
func (gm *GangManager) clearGangsLocked() {
	gm.activeGangs = make(map[string]*Gang)
	gm.serviceToGang = make(map[string]string)
}

// SetStage updates the gang lifecycle stage
func (gm *GangManager) SetStage(stage GangStage) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	klog.V(2).Infof("Gang lifecycle stage: %s → %s", gm.stage, stage)
	gm.stage = stage
}
