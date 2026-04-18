#!/usr/bin/env python3
"""
NEXUS Control-Plane Metrics Exporter
====================================

Exports Prometheus metrics related to scheduler control-plane overhead.
Queries kube-apiserver, scheduler, and etcd metrics to measure:
- Scheduler CPU/memory consumption
- API server request rate and latency
- etcd write operations
- Scheduling throughput
- Pod pending duration

This proves NEXUS's core claim: event-driven scheduling = low overhead
"""

import urllib3
import json
import time
import os
from datetime import datetime, timedelta

urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)

class PrometheusMetricsQuery:
    """Query Prometheus for control-plane metrics"""
    
    def __init__(self, prometheus_url="http://prometheus.nexus-system:9090"):
        self.base_url = prometheus_url
        self.http = urllib3.PoolManager()
    
    def query(self, promql, time_range_seconds=300):
        """Execute PromQL query over time range"""
        try:
            end_time = datetime.now()
            start_time = end_time - timedelta(seconds=time_range_seconds)
            
            query_url = f"{self.base_url}/api/v1/query_range"
            fields = {
                'query': promql,
                'start': int(start_time.timestamp()),
                'end': int(end_time.timestamp()),
                'step': '15s'
            }
            
            response = self.http.request('GET', query_url, fields=fields)
            data = json.loads(response.data.decode('utf-8'))
            
            if data['status'] == 'success':
                return data['data']['result']
            return []
        except Exception as e:
            print(f"Error querying Prometheus: {e}")
            return []

class ControlPlaneMetricsCollector:
    """Collect and analyze control-plane overhead metrics"""
    
    METRICS = {
        # Scheduler metrics
        "scheduler_cpu_usage": {
            "query": "rate(container_cpu_usage_seconds_total{pod=~'nexus-scheduler.*'}[5m])",
            "help": "Scheduler CPU consumption rate (cores)"
        },
        "scheduler_memory_usage": {
            "query": "container_memory_usage_bytes{pod=~'nexus-scheduler.*'} / 1024 / 1024",
            "help": "Scheduler memory usage (MB)"
        },
        "scheduler_goroutines": {
            "query": "go_goroutines{job='nexus-scheduler'}",
            "help": "Number of goroutines in scheduler"
        },
        
        # API Server metrics
        "apiserver_request_rate": {
            "query": "rate(apiserver_request_total[5m])",
            "help": "API server request rate (requests/sec)"
        },
        "apiserver_request_latency_p99": {
            "query": "histogram_quantile(0.99, rate(apiserver_request_duration_seconds_bucket[5m]))",
            "help": "API server request latency p99 (seconds)"
        },
        "apiserver_request_latency_p95": {
            "query": "histogram_quantile(0.95, rate(apiserver_request_duration_seconds_bucket[5m]))",
            "help": "API server request latency p95 (seconds)"
        },
        
        # etcd metrics
        "etcd_write_rate": {
            "query": "rate(etcd_server_has_leader[5m])",
            "help": "Indicator of etcd activity"
        },
        "etcd_commit_duration_p99": {
            "query": "histogram_quantile(0.99, rate(etcd_server_commit_duration_seconds_bucket[5m]))",
            "help": "etcd commit latency p99 (seconds)"
        },
        
        # Scheduling throughput
        "pod_scheduling_rate": {
            "query": "rate(scheduler_pod_scheduling_attempts_total[5m])",
            "help": "Pod scheduling rate (pods/sec)"
        },
        "pod_pending_duration": {
            "query": "increase(pod_scheduling_latency_seconds_sum[5m])",
            "help": "Cumulative pod pending duration (seconds)"
        },
        
        # NEXUS specific
        "nexus_extender_calls": {
            "query": "rate(nexus_filter_calls_total[5m]) + rate(nexus_prioritize_calls_total[5m])",
            "help": "NEXUS extender call rate (calls/sec)"
        },
        "nexus_activation_count": {
            "query": "nexus_spike_events_total",
            "help": "Total NEXUS spike detection events"
        }
    }
    
    def __init__(self):
        self.prometheus = PrometheusMetricsQuery()
        self.results = {}
    
    def collect_all(self, time_range=300):
        """Collect all control-plane metrics"""
        print("\nCollecting Control-Plane Metrics...")
        print("=" * 80)
        
        for metric_name, config in self.METRICS.items():
            print(f"  Querying: {metric_name}")
            results = self.prometheus.query(config['query'], time_range)
            self.results[metric_name] = {
                'help': config['help'],
                'data': results,
                'timestamp': datetime.now().isoformat()
            }
        
        return self.results
    
    def print_summary(self):
        """Print human-readable summary"""
        print("\n" + "=" * 80)
        print("CONTROL-PLANE OVERHEAD SUMMARY")
        print("=" * 80)
        
        for metric_name, data in self.results.items():
            print(f"\n{metric_name}:")
            print(f"  Description: {data['help']}")
            
            if data['data']:
                for entry in data['data']:
                    value = entry.get('value', [None, None])[1]
                    print(f"  Value: {value}")
            else:
                print(f"  Value: No data")
    
    def export_json(self, filename="control_plane_metrics.json"):
        """Export metrics to JSON"""
        with open(filename, 'w') as f:
            json.dump(self.results, f, indent=2)
        print(f"\nMetrics exported to: {filename}")
    
    def export_csv(self, filename="control_plane_metrics.csv"):
        """Export metrics to CSV for analysis"""
        import csv
        
        with open(filename, 'w', newline='') as f:
            writer = csv.writer(f)
            writer.writerow(['Metric', 'Description', 'Value', 'Timestamp'])
            
            for metric_name, data in self.results.items():
                if data['data']:
                    for entry in data['data']:
                        value = entry.get('value', [None, None])[1]
                        writer.writerow([
                            metric_name,
                            data['help'],
                            value,
                            data['timestamp']
                        ])
        
        print(f"Metrics exported to: {filename}")

class OverheadAnalyzer:
    """Analyze overhead between schedulers"""
    
    def compare_schedulers(self, default_metrics, volcano_metrics, nexus_metrics):
        """Compare metrics across schedulers"""
        print("\n" + "=" *  80)
        print("SCHEDULER COMPARISON - CONTROL-PLANE OVERHEAD")
        print("=" * 80)
        
        # Key metrics to compare
        comparison_metrics = [
            "scheduler_cpu_usage",
            "scheduler_memory_usage",
            "apiserver_request_rate",
            "pod_scheduling_rate",
            "pod_pending_duration"
        ]
        
        schedulers = {
            "Default": default_metrics,
            "Volcano": volcano_metrics,
            "NEXUS": nexus_metrics
        }
        
        for metric in comparison_metrics:
            print(f"\n{metric}:")
            for sched_name, metrics in schedulers.items():
                if metric in metrics and metrics[metric]['data']:
                    value = metrics[metric]['data'][0].get('value', [None, None])[1]
                    print(f"  {sched_name:10}: {value}")
    
    def calculate_overhead_ratio(self, baseline_value, test_value):
        """Calculate overhead as percentage"""
        if baseline_value == 0:
            return float('inf')
        return ((test_value - baseline_value) / baseline_value) * 100

if __name__ == "__main__":
    # Collect metrics
    collector = ControlPlaneMetricsCollector()
    collector.collect_all(time_range=300)
    
    # Print summary
    collector.print_summary()
    
    # Export for analysis
    collector.export_json("control_plane_metrics.json")
    collector.export_csv("control_plane_metrics.csv")
    
    print("\n✓ Metrics collection complete")
