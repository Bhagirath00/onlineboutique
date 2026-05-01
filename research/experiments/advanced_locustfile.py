#!/usr/bin/env python3
"""
NEXUS Research - Advanced Locust Load Generator
================================================

Generates 4 distinct traffic scenarios for research baseline comparison:
1. Steady Load: Constant RPS (baseline behavior)
2. Sudden Spike: Instant jump to 10x load (flash sale scenario)
3. Gradual Ramp: Linear increase over time (realistic scale-up)
4. Mixed Workload: Background load + burst on top (composite)

Each scenario is tagged with experiment metadata for result comparison.
"""

from locust import HttpUser, task, between, events, TaskSet
import random
import time
import os
from datetime import datetime

# Configuration
SCENARIO = os.getenv("LOAD_SCENARIO", "steady")  # steady, spike, ramp, mixed
TARGET_RPS = int(os.getenv("TARGET_RPS", "100"))
SPIKE_MULTIPLIER = int(os.getenv("SPIKE_MULTIPLIER", "10"))
EXPERIMENT_ID = os.getenv("EXPERIMENT_ID", "default")
SCHEDULER_TYPE = os.getenv("SCHEDULER_TYPE", "nexus")  # nexus, volcano, default, affinity

# Product catalog for realistic browsing
PRODUCTS = [
    '0PUK6V6EV0', '1YMWWN1N4O', '2ZYFJ3GM2N', '66VCHSJNUP',
    '6E92ZMYYFZ', '9SIQT8TOJO', 'L9ECAV7KIM', 'LS4PSXUNUM', 'OLJCESPC7Z'
]

# Global state for dynamic load adjustment
class LoadState:
    current_rps = TARGET_RPS
    start_time = time.time()
    spike_activated = False

load_state = LoadState()

@events.test_start.add_listener
def on_test_start(environment, **kwargs):
    """Log experiment metadata at start"""
    print("\n" + "="*80)
    print("NEXUS Research Experiment Started")
    print("="*80)
    print(f"Scenario:       {SCENARIO}")
    print(f"Target RPS:     {TARGET_RPS}")
    print(f"Scheduler:      {SCHEDULER_TYPE}")
    print(f"Experiment ID:  {EXPERIMENT_ID}")
    print(f"Timestamp:      {datetime.now().isoformat()}")
    print("="*80 + "\n")

@events.test_stop.add_listener
def on_test_stop(environment, **kwargs):
    """Log experiment summary at end"""
    print("\n" + "="*80)
    print("NEXUS Research Experiment Completed")
    print("="*80)
    print(f"Total duration: {time.time() - load_state.start_time:.1f}s")
    print("="*80 + "\n")

class BoutiqueUserBehavior(TaskSet):
    """User behavior mimicking Online Boutique visitors"""
    
    def on_start(self):
        """Initialize user session"""
        self.product_index = 0
    
    @task(7)
    def browse_products(self):
        """Browse product catalog"""
        product_id = random.choice(PRODUCTS)
        self.client.get(f"/product/{product_id}", 
                       headers={"X-Experiment": EXPERIMENT_ID, 
                               "X-Scheduler": SCHEDULER_TYPE})
    
    @task(2)
    def add_to_cart(self):
        """Add product to shopping cart"""
        product_id = random.choice(PRODUCTS)
        self.client.post("/cart", 
                        data={"product_id": product_id, "quantity": 1},
                        headers={"X-Experiment": EXPERIMENT_ID,
                                "X-Scheduler": SCHEDULER_TYPE})
    

class SteadyLoadUserLogic:
    """SCENARIO 1: Steady Load
    
    Maintains constant RPS throughout the experiment.
    Used to measure baseline control-plane overhead and steady-state latency.
    
    Expected: Consistent latencies, low pending pods, minimal extender calls
    """
    tasks = [BoutiqueUserBehavior]
    wait_time = between(0.5, 1.5)
    
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.meta = {
            "scenario": "steady",
            "rps_target": TARGET_RPS,
            "behavior": "constant load"
        }

class SuddenSpikeUserLogic:
    """SCENARIO 2: Sudden Spike (Flash Sale)
    
    Starts with baseline load, then instantly jumps to 10x multiplier.
    Used to measure spike detection response and gang scheduling activation.
    
    Expected: 
    - NEXUS: Detects spike, activates gang, co-locates services
    - Volcano: Coordinates but with overhead
    - Default: Services scattered across nodes, high latency
    
    Timing:
    - 0-60s: Warm-up (baseline traffic)
    - 60s: SPIKE ACTIVATES (rps → rps * SPIKE_MULTIPLIER)
    - 60-180s: Sustained high load
    - 180s+: Cool-down
    """
    tasks = [BoutiqueUserBehavior]
    wait_time = between(0.1, 0.5)  # Aggressive when active
    
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.spawn_time = time.time()
        self.meta = {
            "scenario": "spike",
            "rps_target": TARGET_RPS,
            "spike_multiplier": SPIKE_MULTIPLIER,
            "behavior": "sudden 10x jump at 60s"
        }
    
    @property
    def wait_time(self):
        """Dynamically adjust delay for spike"""
        elapsed = time.time() - self.spawn_time
        
        if elapsed > 60 and elapsed < 180:
            # Spike active: high frequency
            factor = float(SPIKE_MULTIPLIER)
        else:
            # Baseline or cooldown
            factor = 1.0
            
        base_wait = 1.0 / TARGET_RPS
        adjusted_wait = base_wait / factor
        return between(adjusted_wait * 0.5, adjusted_wait * 1.5)

class GradualRampUserLogic:
    """SCENARIO 3: Gradual Ramp
    
    Linearly increases RPS over time, simulating gradual load increase.
    Used to measure scheduler response under sustained growth.
    
    Expected:
    - NEXUS: Stays dormant until high load spike detected
    - Volcano: Continuous optimization attempt
    - Default: Increasing latency as load grows
    
    Ramp profile:
    - 0-120s: Linear increase from baseline to 10x
    - 120-240s: Sustained at 10x
    - 240s+: Cool-down
    """
    tasks = [BoutiqueUserBehavior]
    
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.spawn_time = time.time()
        self.meta = {
            "scenario": "ramp",
            "rps_target": TARGET_RPS,
            "ramp_duration": 120,
            "behavior": "linear increase over 120s to 10x"
        }
    
    @property
    def wait_time(self):
        """Dynamically adjust inter-request delay based on ramp"""
        elapsed = time.time() - self.spawn_time
        
        if elapsed < 120:
            # Ramp phase: linear increase
            ramp_factor = 1.0 + (elapsed / 120.0) * (SPIKE_MULTIPLIER - 1)
        elif elapsed < 240:
            # Sustained phase: peak load
            ramp_factor = float(SPIKE_MULTIPLIER)
        else:
            # Cool-down phase: back to baseline
            ramp_factor = 1.0
        
        # Convert RPS factor to wait time
        base_wait = 1.0 / TARGET_RPS
        adjusted_wait = base_wait / ramp_factor
        
        return between(adjusted_wait * 0.5, adjusted_wait * 1.5)

class MixedWorkloadUserLogic:
    """SCENARIO 4: Mixed Workload
    
    Simulates realistic production: background traffic + periodic bursts.
    Used to measure real-world performance and burst responsiveness.
    
    Expected:
    - NEXUS: Activates on burst, stays dormant during background load
    - Volcano: Always active
    - Default: Struggles with bursts
    
    Pattern:
    - 0-60s: Background steady load
    - 60-80s: Burst (10x spike)
    - 80-140s: Background again
    - 140-160s: Burst (10x spike)
    - 160-220s: Background
    - Repeat until end
    """
    tasks = [BoutiqueUserBehavior]
    
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.spawn_time = time.time()
        self.burst_active = False
        self.meta = {
            "scenario": "mixed",
            "rps_target": TARGET_RPS,
            "behavior": "background + periodic bursts every 60s"
        }
    
    @property
    def wait_time(self):
        """Toggle between background and burst"""
        elapsed = time.time() - self.spawn_time
        cycle_position = elapsed % 120  # 120s cycle (60s background + 20s burst)
        
        if cycle_position < 60:
            # Background load
            if self.burst_active:
                print(f"\n[BURST END] Returning to background load")
                self.burst_active = False
            return between(0.8, 1.2)  # ~75 RPS
        else:
            # Burst period
            if not self.burst_active:
                print(f"\n[BURST START] Ramping to 750 RPS")
                self.burst_active = True
            return between(0.08, 0.12)  # ~750 RPS (10x burst)

# Scenario dispatcher - Creates exactly ONE class that Locust will detect
if SCENARIO == "steady":
    class ActiveUser(HttpUser, SteadyLoadUserLogic): pass
elif SCENARIO == "spike":
    class ActiveUser(HttpUser, SuddenSpikeUserLogic): pass
elif SCENARIO == "ramp":
    class ActiveUser(HttpUser, GradualRampUserLogic): pass
elif SCENARIO == "mixed":
    class ActiveUser(HttpUser, MixedWorkloadUserLogic): pass
else:
    raise ValueError(f"Unknown scenario: {SCENARIO}")
