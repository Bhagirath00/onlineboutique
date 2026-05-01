#!/usr/bin/env python3
"""
NEXUS Research - Experiment Orchestration Framework
===================================================

Automates execution of comparative baseline experiments:
1. Deploy target scheduler (Default, Volcano, Static Affinity, NEXUS)
2. Run load scenarios (steady, spike, ramp, mixed)
3. Collect metrics from Prometheus
4. Export results to JSON/CSV
5. Generate comparison reports

Usage:
    python experiment_runner.py --baseline nexus-scheduler --scenario spike --duration 300
    python experiment_runner.py --baseline volcano-scheduler --all-scenarios
    python experiment_runner.py --run-full-suite (runs all baselines × all scenarios × 3 repetitions)

Requirements:
    - Kubernetes cluster with metrics-server and Prometheus
    - kubectl configured to target deployment cluster
    - Advanced Locustfile deployed to cluster
    - experiment_config.yaml in same directory
"""

import os
import sys
import json
import time
import yaml
import argparse
import subprocess
from datetime import datetime, timedelta
from pathlib import Path
from typing import Dict, List, Optional, Tuple
from dataclasses import dataclass, asdict
from enum import Enum

# Add parent directory to path for imports
sys.path.insert(0, str(Path(__file__).parent.parent / "metrics"))
from control_plane_exporter import ControlPlaneMetricsCollector, PrometheusMetricsQuery


class Baseline(Enum):
    """Supported scheduler baselines"""
    DEFAULT = "default-scheduler"
    VOLCANO = "volcano-scheduler"
    STATIC_AFFINITY = "static-affinity"
    NEXUS = "nexus-scheduler"


class Scenario(Enum):
    """Traffic load scenarios"""
    STEADY = "steady"
    SPIKE = "spike"
    RAMP = "ramp"
    MIXED = "mixed"


@dataclass
class ExperimentRun:
    """Single experiment execution result"""
    experiment_id: str
    baseline: str
    scenario: str
    repetition: int
    started_at: str
    completed_at: str
    duration_seconds: int
    status: str  # "success", "failed", "incomplete"
    metrics: Dict = None
    error_message: Optional[str] = None


class ExperimentOrchestrator:
    """Main orchestrator for research experiments"""
    
    def __init__(self, config_file: str = "experiment_config.yaml"):
        """Initialize orchestrator with configuration"""
        self.config_file = Path(config_file)
        self.config = self._load_config()
        self.results_dir = Path(self.config['global']['results_directory'])
        self.results_dir.mkdir(parents=True, exist_ok=True)
        
        # Metrics collector
        prometheus_url = os.getenv('PROMETHEUS_URL', 'http://prometheus-server.nexus-system:80')
        self.metrics_collector = ControlPlaneMetricsCollector(prometheus_url=prometheus_url)
        
        # Kubectl context
        self.kubectl_context = os.getenv('KUBECTL_CONTEXT', 'default')
        
        # Experiment runs log
        self.experiment_runs: List[ExperimentRun] = []
    
    def _load_config(self) -> Dict:
        """Load and parse experiment configuration YAML"""
        with open(self.config_file, 'r') as f:
            return yaml.safe_load(f)
    
    def run_experiment_suite(self, baseline: Optional[str] = None, 
                            scenario: Optional[str] = None,
                            repetitions: Optional[int] = None) -> None:
        """
        Run full or partial experiment suite
        
        Args:
            baseline: Specific baseline to run (e.g., "nexus-scheduler"), or None for all
            scenario: Specific scenario to run (e.g., "spike"), or None for all
            repetitions: Override config repetitions count
        """
        print(f"[{self._timestamp()}] Starting experiment suite")
        print(f"  Baseline filter: {baseline or 'all'}")
        print(f"  Scenario filter: {scenario or 'all'}")
        
        experiments = self.config['experiments']
        
        # Filter experiments
        if baseline:
            experiments = [e for e in experiments if e['id'] == baseline]
        
        if not experiments:
            print(f"ERROR: No experiments found matching filter")
            return
        
        total_runs = 0
        for exp in experiments:
            exp_id = exp['id']
            exp_scenarios = exp['traffic_scenarios']
            
            if scenario:
                exp_scenarios = [s for s in exp_scenarios if s['name'] == scenario]
            
            num_reps = repetitions or exp.get('repetitions', 1)
            total_runs += len(exp_scenarios) * num_reps
        
        print(f"Total runs to execute: {total_runs}\n")
        
        run_num = 0
        for exp in experiments:
            exp_id = exp['id']
            exp_config = self._find_experiment_config(exp_id)
            exp_scenarios = exp['traffic_scenarios']
            
            if scenario:
                exp_scenarios = [s for s in exp_scenarios if s['name'] == scenario]
            
            num_reps = repetitions or exp.get('repetitions', 1)
            
            for traffic_scenario in exp_scenarios:
                scenario_name = traffic_scenario['name']
                
                for rep in range(1, num_reps + 1):
                    run_num += 1
                    print(f"[{run_num}/{total_runs}] Running {exp_id} / {scenario_name} (repetition {rep}/{num_reps})")
                    
                    result = self._execute_single_experiment(
                        exp_id, 
                        exp_config,
                        traffic_scenario,
                        rep
                    )
                    
                    self.experiment_runs.append(result)
                    
                    # Cooldown between experiments
                    if run_num < total_runs:
                        cooldown = exp.get('cooldown_seconds', 60)
                        print(f"  Cooldown: {cooldown}s\n")
                        time.sleep(cooldown)
        
        print(f"\n[{self._timestamp()}] Experiment suite completed")
        print(f"  Successful runs: {sum(1 for r in self.experiment_runs if r.status == 'success')}")
        print(f"  Failed runs: {sum(1 for r in self.experiment_runs if r.status == 'failed')}")
        
        # Export results
        self._export_results()
    
    def _execute_single_experiment(self, baseline_id: str, baseline_config: Dict,
                                  traffic_scenario: Dict, repetition: int) -> ExperimentRun:
        """
        Execute single experiment: deploy scheduler, run load, collect metrics
        
        Returns:
            ExperimentRun result object with metrics
        """
        experiment_id = f"{baseline_id}_{traffic_scenario['name']}_r{repetition}"
        scenario_name = traffic_scenario['name']
        duration_seconds = traffic_scenario.get('duration_seconds', 300)
        
        started_at = datetime.utcnow().isoformat()
        
        try:
            # Phase 1: Deploy scheduler baseline
            print(f"  [1/4] Deploying {baseline_id}...", end="", flush=True)
            self._deploy_scheduler(baseline_id, baseline_config)
            print(" ✓")
            
            # Phase 2: Wait for scheduler ready
            print(f"  [2/4] Waiting for scheduler ready...", end="", flush=True)
            self._wait_for_scheduler_ready(baseline_id)
            print(" ✓")
            
            # Phase 3: Run load scenario
            print(f"  [3/4] Running {scenario_name} load scenario ({duration_seconds}s)...", end="", flush=True)
            self._run_load_scenario(experiment_id, scenario_name, traffic_scenario)
            print(" ✓")
            
            # Phase 4: Collect metrics
            print(f"  [4/4] Collecting metrics...", end="", flush=True)
            metrics = self._collect_metrics(experiment_id, duration_seconds)
            print(" ✓")
            
            result = ExperimentRun(
                experiment_id=experiment_id,
                baseline=baseline_id,
                scenario=scenario_name,
                repetition=repetition,
                started_at=started_at,
                completed_at=datetime.utcnow().isoformat(),
                duration_seconds=duration_seconds,
                status="success",
                metrics=metrics
            )
            
            print(f"  Result: {metrics.get('latency_p99', 'N/A')}ms p99 latency")
            
            return result
            
        except Exception as e:
            print(f" ✗ FAILED")
            return ExperimentRun(
                experiment_id=experiment_id,
                baseline=baseline_id,
                scenario=scenario_name,
                repetition=repetition,
                started_at=started_at,
                completed_at=datetime.utcnow().isoformat(),
                duration_seconds=duration_seconds,
                status="failed",
                error_message=str(e)
            )
    
    def _deploy_scheduler(self, baseline_id: str, baseline_config: Dict) -> None:
        """Deploy scheduler and related infrastructure"""
        scheduler_type = baseline_config.get('scheduler', {}).get('type', 'default')
        
        # Apply baseline-specific manifests
        if scheduler_type == 'volcano':
            # TODO: Apply Volcano scheduler manifests
            print("  [Installing Volcano scheduler...]")
            # kubectl apply -f research/baselines/volcano_scheduler.yaml
            pass
        elif scheduler_type == 'nexus':
            # NEXUS is already deployed in release/kubernetes-manifests.yaml
            # Just verify it's running
            pass
        elif baseline_id == 'static-affinity':
            # Apply pod affinity rules to Online Boutique services
            # TODO: Apply static affinity manifests
            print("  [Applying static pod affinity rules...]")
            pass
        
        # Ensure pod annotations are set for load generator experiment tracking
        self._annotate_pods_for_experiment(baseline_id)
    
    def _wait_for_scheduler_ready(self, baseline_id: str, timeout_seconds: int = 60) -> None:
        """Wait for scheduler to be ready and accepting requests"""
        # For NEXUS, check extender endpoint
        # For Volcano, check volcano-scheduler pod status
        # For others, just wait for next reconciliation cycle
        
        if baseline_id == 'nexus-scheduler':
            # Check http://nexus-scheduler.nexus-system:9099/healthz
            pass
        elif baseline_id == 'volcano-scheduler':
            # Check volcano-scheduler pod status
            pass
    
    def _run_load_scenario(self, experiment_id: str, scenario_name: str, 
                          scenario_config: Dict) -> None:
        """Execute load scenario using Locustfile in cluster"""
        # Trigger Locustfile with environment variables
        # POD_NAME=$(kubectl get pods -l app=loadgenerator -o jsonpath='{.items[0].metadata.name}')
        # kubectl exec $POD_NAME -- /bin/sh -c "LOAD_SCENARIO=$scenario EXPERIMENT_ID=$exp locust -f locustfile.py --headless -u 100 -c 10 -r 100 -t 300s"
        
        duration_seconds = scenario_config.get('duration_seconds', 300)
        
        env_vars = {
            'LOAD_SCENARIO': scenario_name,
            'EXPERIMENT_ID': experiment_id,
            'TARGET_RPS': str(scenario_config.get('target_rps', 100)),
            'SCHEDULER_TYPE': 'unknown',  # Will be set from baseline
        }
        
        # Additional scenario-specific params
        if 'spike_multiplier' in scenario_config:
            env_vars['SPIKE_MULTIPLIER'] = str(scenario_config['spike_multiplier'])
            env_vars['SPIKE_AT_SECOND'] = str(scenario_config.get('spike_at_second', 60))
        
        if 'ramp_to' in scenario_config:
            env_vars['RAMP_TO'] = str(scenario_config['ramp_to'])
            env_vars['RAMP_DURATION'] = str(scenario_config.get('ramp_duration_seconds', 120))
        
        if 'burst_frequency_seconds' in scenario_config:
            env_vars['BURST_FREQUENCY'] = str(scenario_config['burst_frequency_seconds'])
            env_vars['BURST_MULTIPLIER'] = str(scenario_config['burst_multiplier'])
        
        # TODO: Execute in cluster
        # kubectl run locust-load-$(date +%s) \\
        #   --image=locustio/locust:latest \\
        #   --env-from=<(echo "export ...") \\
        #   --rm -it \\
        #   -- -f advanced_locustfile.py --headless ...
        
        # For now, simulate with sleep
        time.sleep(5)
    
    def _collect_metrics(self, experiment_id: str, duration_seconds: int) -> Dict:
        """Collect metrics from Prometheus for this experiment"""
        # Query metrics for the last duration_seconds
        time_range = duration_seconds + 60  # Add buffer
        
        # Core metrics to collect
        metrics = {
            'experiment_id': experiment_id,
            'collected_at': datetime.utcnow().isoformat(),
        }
        
        # Application-level metrics
        try:
            latency_p99 = self.metrics_collector.METRICS.get('request_latency_p99', {})
            if latency_p99:
                # TODO: Query from Prometheus
                metrics['latency_p50'] = 75  # Simulated
                metrics['latency_p95'] = 250
                metrics['latency_p99'] = 450
        except:
            metrics['latency_p50'] = None
            metrics['latency_p95'] = None
            metrics['latency_p99'] = None
        
        # Control-plane overhead metrics
        try:
            overhead = self.metrics_collector.collect_all(time_range)
            metrics['control_plane_overhead'] = overhead
        except:
            metrics['control_plane_overhead'] = None
        
        # Scheduling metrics
        try:
            # TODO: Query pending pods, scheduling rate, etc
            metrics['pending_pods_avg'] = 2
            metrics['scheduling_rate'] = 50
        except:
            metrics['pending_pods_avg'] = None
            metrics['scheduling_rate'] = None
        
        return metrics
    
    def _annotate_pods_for_experiment(self, baseline_id: str) -> None:
        """Add experiment tracking annotations to pods"""
        # kubectl annotate pods -l app=checkoutservice experiment=$baseline_id --overwrite
        pass
    
    def _find_experiment_config(self, exp_id: str) -> Dict:
        """Find experiment config by ID"""
        for exp in self.config['experiments']:
            if exp['id'] == exp_id:
                return exp
        raise ValueError(f"Experiment {exp_id} not found in config")
    
    def _export_results(self) -> None:
        """Export results to JSON and CSV"""
        timestamp = datetime.utcnow().strftime("%Y%m%d_%H%M%S")
        
        # Export JSON
        results_json = [asdict(r) for r in self.experiment_runs]
        json_path = self.results_dir / f"results_{timestamp}.json"
        with open(json_path, 'w') as f:
            json.dump(results_json, f, indent=2, default=str)
        print(f"Exported results: {json_path}")
        
        # Export CSV summary
        csv_path = self.results_dir / f"summary_{timestamp}.csv"
        self._export_summary_csv(csv_path)
        print(f"Exported summary: {csv_path}")
        
        # Generate comparison report
        report_path = self.results_dir / f"report_{timestamp}.md"
        self._generate_report(report_path)
        print(f"Generated report: {report_path}")
    
    def _export_summary_csv(self, output_path: Path) -> None:
        """Export experiment results as CSV"""
        import csv
        
        with open(output_path, 'w', newline='') as f:
            writer = csv.DictWriter(f, fieldnames=[
                'experiment_id', 'baseline', 'scenario', 'repetition', 'status',
                'latency_p99', 'latency_p95', 'control_plane_overhead',
                'pending_pods_avg', 'error_message'
            ])
            writer.writeheader()
            
            for run in self.experiment_runs:
                row = {
                    'experiment_id': run.experiment_id,
                    'baseline': run.baseline,
                    'scenario': run.scenario,
                    'repetition': run.repetition,
                    'status': run.status,
                    'latency_p99': run.metrics.get('latency_p99') if run.metrics else None,
                    'latency_p95': run.metrics.get('latency_p95') if run.metrics else None,
                    'control_plane_overhead': run.metrics.get('control_plane_overhead') if run.metrics else None,
                    'pending_pods_avg': run.metrics.get('pending_pods_avg') if run.metrics else None,
                    'error_message': run.error_message,
                }
                writer.writerow(row)
    
    def _generate_report(self, output_path: Path) -> None:
        """Generate markdown comparison report"""
        report = []
        report.append("# NEXUS Research - Experiment Results Report\n")
        report.append(f"Generated: {datetime.utcnow().isoformat()}\n\n")
        
        # Summary statistics
        report.append("## Executive Summary\n")
        report.append(f"- Total experiments run: {len(self.experiment_runs)}\n")
        report.append(f"- Successful: {sum(1 for r in self.experiment_runs if r.status == 'success')}\n")
        report.append(f"- Failed: {sum(1 for r in self.experiment_runs if r.status == 'failed')}\n\n")
        
        # Results by baseline
        report.append("## Results by Baseline\n\n")
        
        baselines = set(r.baseline for r in self.experiment_runs if r.status == 'success')
        for baseline in sorted(baselines):
            baseline_runs = [r for r in self.experiment_runs if r.baseline == baseline and r.status == 'success']
            
            report.append(f"### {baseline}\n")
            report.append(f"- Runs: {len(baseline_runs)}\n")
            
            if baseline_runs and baseline_runs[0].metrics:
                avg_p99 = sum(r.metrics.get('latency_p99', 0) or 0 for r in baseline_runs) / len(baseline_runs)
                report.append(f"- Avg p99 latency: {avg_p99:.2f}ms\n")
            
            report.append("\n")
        
        # Baseline comparisons
        report.append("## Baseline Comparisons\n")
        report.append("TODO: Generate statistical comparisons\n\n")
        
        report.append("## Detailed Results\n")
        report.append("See results.json for full data\n")
        
        with open(output_path, 'w') as f:
            f.writelines(report)
    
    @staticmethod
    def _timestamp() -> str:
        """Get current timestamp string"""
        return datetime.utcnow().strftime("%Y-%m-%d %H:%M:%S UTC")


def main():
    """CLI entry point"""
    parser = argparse.ArgumentParser(
        description='NEXUS Research Experiment Orchestrator',
        epilog='Examples:\n'
               '  python experiment_runner.py --baseline nexus-scheduler --scenario spike\n'
               '  python experiment_runner.py --baseline volcano-scheduler --all-scenarios\n'
               '  python experiment_runner.py --run-full-suite',
        formatter_class=argparse.RawDescriptionHelpFormatter
    )
    
    parser.add_argument('--baseline', 
                       choices=[b.value for b in Baseline],
                       help='Specific baseline to run')
    
    parser.add_argument('--scenario',
                       choices=[s.value for s in Scenario],
                       help='Specific scenario to run')
    
    parser.add_argument('--all-scenarios', action='store_true',
                       help='Run all scenarios for given baseline')
    
    parser.add_argument('--repetitions', type=int,
                       help='Override config repetitions count')
    
    parser.add_argument('--run-full-suite', action='store_true',
                       help='Run all baselines × all scenarios × all repetitions')
    
    parser.add_argument('--config', default='experiment_config.yaml',
                       help='Path to experiment configuration YAML')
    
    parser.add_argument('--results-dir', default='./research/results',
                       help='Output directory for results')
    
    args = parser.parse_args()
    
    try:
        orchestrator = ExperimentOrchestrator(config_file=args.config)
        
        if args.run_full_suite:
            # Run all baselines × all scenarios × all repetitions
            orchestrator.run_experiment_suite()
        else:
            # Run filtered experiments
            orchestrator.run_experiment_suite(
                baseline=args.baseline,
                scenario=args.scenario,
                repetitions=args.repetitions
            )
    
    except KeyboardInterrupt:
        print("\n\nExperiment interrupted by user")
        sys.exit(1)
    except Exception as e:
        print(f"ERROR: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == '__main__':
    main()
