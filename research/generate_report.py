#!/usr/bin/env python3
"""
Research Report Generator
=========================

Generates a comprehensive comparison report for Kubernetes scheduler research.
Combines data from Locust, Prometheus, and pod events into a unified document.

Usage:
    python generate_report.py --data-dir data/ --output report.md
"""

import argparse
import json
import os
import glob
from datetime import datetime
from typing import Dict, List, Any, Optional


def load_locust_data(csv_path: str) -> Dict[str, Any]:
    """Load and parse Locust CSV results."""
    import csv
    
    if not os.path.exists(csv_path):
        return {}
    
    results = {}
    with open(csv_path, 'r') as f:
        reader = csv.DictReader(f)
        for row in reader:
            if row.get('Name') == 'Aggregated':
                results = {
                    'total_requests': int(row.get('Request Count', 0)),
                    'failures': int(row.get('Failure Count', 0)),
                    'median_response_time': float(row.get('Median Response Time', 0)),
                    'avg_response_time': float(row.get('Average Response Time', 0)),
                    'min_response_time': float(row.get('Min Response Time', 0)),
                    'max_response_time': float(row.get('Max Response Time', 0)),
                    'p50': float(row.get('50%', 0)),
                    'p95': float(row.get('95%', 0)),
                    'p99': float(row.get('99%', 0)),
                    'rps': float(row.get('Requests/s', 0)),
                }
    return results


def load_metrics_data(json_path: str) -> Dict[str, Any]:
    """Load Prometheus metrics from JSON."""
    if not os.path.exists(json_path):
        return {}
    
    with open(json_path, 'r') as f:
        data = json.load(f)
    return data.get('metrics', {})


def load_gantt_stats(json_path: str) -> Dict[str, Any]:
    """Load Gantt chart statistics."""
    if not os.path.exists(json_path):
        return {}
    
    with open(json_path, 'r') as f:
        return json.load(f)


def generate_report(
    schedulers: List[str],
    data_dir: str,
    output_path: str
):
    """Generate the comprehensive comparison report."""
    
    report = []
    report.append("# Kubernetes Scheduler Comparison Report")
    report.append("")
    report.append(f"**Generated:** {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    report.append("")
    
    # Load data for each scheduler
    all_data = {}
    for scheduler in schedulers:
        scheduler_dir = os.path.join(data_dir, f"{scheduler}_scheduler")
        if not os.path.exists(scheduler_dir):
            scheduler_dir = os.path.join(data_dir, scheduler)
        
        if not os.path.exists(scheduler_dir):
            print(f"Warning: No data directory found for {scheduler}")
            continue
        
        # Find Locust CSV
        locust_files = glob.glob(os.path.join(scheduler_dir, "*_stats.csv"))
        locust_data = load_locust_data(locust_files[0]) if locust_files else {}
        
        # Find metrics JSON
        metrics_file = os.path.join(scheduler_dir, f"{scheduler}_metrics.json")
        if not os.path.exists(metrics_file):
            metrics_file = os.path.join(scheduler_dir, "metrics.json")
        metrics_data = load_metrics_data(metrics_file)
        
        all_data[scheduler] = {
            'locust': locust_data,
            'metrics': metrics_data,
        }
    
    # Section 1: Executive Summary
    report.append("## Executive Summary")
    report.append("")
    report.append("This report compares the performance and overhead of different Kubernetes schedulers")
    report.append(f"under flash-sale traffic conditions. Schedulers tested: **{', '.join(schedulers)}**")
    report.append("")
    
    # Section 2: Application Performance (Locust)
    report.append("## 1. Application Performance")
    report.append("")
    report.append("Metrics from Locust load testing (100 concurrent users, 5-minute test).")
    report.append("")
    
    # Build comparison table
    report.append("| Metric | " + " | ".join(schedulers) + " |")
    report.append("|" + "---|" * (len(schedulers) + 1))
    
    locust_metrics = [
        ('Total Requests', 'total_requests', ''),
        ('Failures', 'failures', ''),
        ('Failure Rate', None, '%'),  # Calculated
        ('Median Response Time', 'median_response_time', 'ms'),
        ('p95 Response Time', 'p95', 'ms'),
        ('p99 Response Time', 'p99', 'ms'),
        ('Requests/sec', 'rps', ''),
    ]
    
    for label, key, unit in locust_metrics:
        row = [label]
        for scheduler in schedulers:
            data = all_data.get(scheduler, {}).get('locust', {})
            if key:
                value = data.get(key)
                if value is not None:
                    row.append(f"{value:.2f}{unit}" if isinstance(value, float) else f"{value}{unit}")
                else:
                    row.append("N/A")
            elif label == 'Failure Rate':
                total = data.get('total_requests', 0)
                failures = data.get('failures', 0)
                if total > 0:
                    rate = (failures / total) * 100
                    row.append(f"{rate:.2f}%")
                else:
                    row.append("N/A")
        report.append("| " + " | ".join(row) + " |")
    
    report.append("")
    
    # Section 3: Control-Plane Overhead
    report.append("## 2. Control-Plane Overhead")
    report.append("")
    report.append("Resource utilization of scheduler and control-plane components.")
    report.append("")
    
    report.append("| Component | Metric | " + " | ".join(schedulers) + " |")
    report.append("|" + "---|" * (len(schedulers) + 2))
    
    overhead_metrics = [
        ('Scheduler', 'scheduler_cpu_usage_millicores', 'CPU (millicores)'),
        ('Scheduler', 'scheduler_memory_mb', 'Memory (MB)'),
        ('API Server', 'apiserver_request_rate', 'Requests/sec'),
        ('API Server', 'apiserver_cpu_usage_millicores', 'CPU (millicores)'),
        ('etcd', 'etcd_request_rate', 'Requests/sec'),
    ]
    
    for component, key, metric_label in overhead_metrics:
        row = [component, metric_label]
        for scheduler in schedulers:
            data = all_data.get(scheduler, {}).get('metrics', {})
            value = data.get(key)
            if value is not None:
                row.append(f"{value:.2f}")
            else:
                row.append("N/A")
        report.append("| " + " | ".join(row) + " |")
    
    report.append("")
    
    # Section 4: Scheduling Performance
    report.append("## 3. Scheduling Performance")
    report.append("")
    report.append("Scheduler-specific metrics.")
    report.append("")
    
    report.append("| Metric | " + " | ".join(schedulers) + " |")
    report.append("|" + "---|" * (len(schedulers) + 1))
    
    scheduling_metrics = [
        ('Scheduling Latency (p99)', 'scheduler_scheduling_latency_p99', 's'),
        ('Scheduling Latency (p50)', 'scheduler_scheduling_latency_p50', 's'),
        ('Pending Pods', 'scheduler_pending_pods', ''),
    ]
    
    for label, key, unit in scheduling_metrics:
        row = [label]
        for scheduler in schedulers:
            data = all_data.get(scheduler, {}).get('metrics', {})
            value = data.get(key)
            if value is not None:
                row.append(f"{value:.4f}{unit}")
            else:
                row.append("N/A")
        report.append("| " + " | ".join(row) + " |")
    
    report.append("")
    
    # Section 5: Key Findings
    report.append("## 4. Key Findings")
    report.append("")
    report.append("### Observations")
    report.append("")
    report.append("1. **Scheduling Overhead**: [To be filled based on data]")
    report.append("2. **Application Impact**: [To be filled based on data]")
    report.append("3. **Gang Scheduling Effect**: [To be filled based on data]")
    report.append("")
    
    # Section 6: Recommendations
    report.append("## 5. Recommendations")
    report.append("")
    report.append("Based on the experimental results:")
    report.append("")
    report.append("- For **steady-state workloads**: [Recommendation]")
    report.append("- For **flash-sale spikes**: [Recommendation]")
    report.append("- For **batch/ML workloads**: [Recommendation]")
    report.append("")
    
    # Section 7: Appendix
    report.append("## Appendix")
    report.append("")
    report.append("### Test Configuration")
    report.append("")
    report.append("| Parameter | Value |")
    report.append("|-----------|-------|")
    report.append("| Cluster Size | 4 nodes (t3.small) |")
    report.append("| Instance Type | AWS Spot |")
    report.append("| Kubernetes Version | 1.29 |")
    report.append("| Test Duration | 5 minutes |")
    report.append("| Concurrent Users | 100 |")
    report.append("")
    
    # Write report
    with open(output_path, 'w') as f:
        f.write('\n'.join(report))
    
    print(f"✅ Report generated: {output_path}")


def main():
    parser = argparse.ArgumentParser(description="Generate scheduler comparison report")
    parser.add_argument("--data-dir", type=str, default="data", help="Directory containing scheduler data")
    parser.add_argument("--schedulers", nargs="+", default=["default", "volcano", "nexus"], 
                       help="Schedulers to compare")
    parser.add_argument("--output", type=str, default="research/scheduler_comparison_report.md",
                       help="Output report path")
    
    args = parser.parse_args()
    
    generate_report(args.schedulers, args.data_dir, args.output)


if __name__ == "__main__":
    main()
