# Kubernetes Scheduler Research Methodology

## Research Objective
Compare scheduling performance and control-plane overhead between:
1. **Default Kubernetes Scheduler** (baseline)
2. **Volcano Scheduler** (batch/gang scheduling)
3. **NEXUS Scheduler** (event-driven, custom)

---

## 1. Metrics Framework

### 1.1 Scheduler Performance Metrics

| Metric | Description | Prometheus Query |
|--------|-------------|------------------|
| **Scheduling Latency** | Time from pod creation to node binding | `histogram_quantile(0.99, sum(rate(scheduler_scheduling_duration_seconds_bucket[5m])) by (le))` |
| **Scheduling Attempts** | Total scheduling attempts | `sum(scheduler_schedule_attempts_total) by (result)` |
| **Pending Pods** | Pods waiting to be scheduled | `scheduler_pending_pods` |
| **Preemption Attempts** | Pod preemption count | `scheduler_preemption_attempts_total` |

### 1.2 Control-Plane Overhead Metrics

| Metric | Description | Prometheus Query |
|--------|-------------|------------------|
| **Scheduler CPU** | CPU usage of kube-scheduler | `sum(rate(container_cpu_usage_seconds_total{container="kube-scheduler"}[5m])) * 1000` |
| **Scheduler Memory** | Memory usage in MB | `container_memory_working_set_bytes{container="kube-scheduler"} / 1024 / 1024` |
| **API Server Requests** | Requests/sec to API server | `sum(rate(apiserver_request_total[5m])) by (verb)` |
| **API Server Latency** | API response time | `histogram_quantile(0.99, sum(rate(apiserver_request_duration_seconds_bucket[5m])) by (le))` |
| **etcd Requests** | etcd operations/sec | `sum(rate(etcd_request_duration_seconds_count[5m]))` |

### 1.3 Node Resource Utilization

| Metric | Description | Prometheus Query |
|--------|-------------|------------------|
| **Node CPU %** | CPU utilization per node | `100 - (avg by(node)(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)` |
| **Node Memory %** | Memory utilization per node | `(1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes) * 100` |
| **Pod Count per Node** | Distribution of pods | `count by(node)(kube_pod_info)` |

### 1.4 Application Performance (from Locust)

| Metric | Source | Description |
|--------|--------|-------------|
| **p50/p95/p99 Latency** | Locust CSV | Response time percentiles |
| **RPS** | Locust CSV | Requests per second |
| **Failure Rate** | Locust CSV | % of failed requests |
| **Total Requests** | Locust CSV | Total requests during test |

---

## 2. Data Collection Procedure

### 2.1 Before Each Experiment

```bash
# 1. Deploy fresh cluster
terraform apply -auto-approve

# 2. Configure kubectl
aws eks update-kubeconfig --region us-east-1 --name online-boutique-cluster

# 3. Install Prometheus/Grafana
helm install monitoring prometheus-community/kube-prometheus-stack -n monitoring --create-namespace

# 4. Deploy application (without scheduler change)
kubectl apply -f release/kubernetes-manifests.yaml

# 5. Wait for pods to be Ready
kubectl wait --for=condition=ready pod --all --timeout=300s
```

### 2.2 During Experiment

```bash
# 1. Start Grafana port-forward
kubectl port-forward svc/monitoring-grafana -n monitoring 3000:80

# 2. Start Prometheus port-forward
kubectl port-forward svc/monitoring-kube-prometheus-prometheus -n monitoring 9090:9090

# 3. Run Locust load test
locust -f src/loadgenerator/locustfile.py \
  --host=http://<FRONTEND_URL> \
  --headless -u 100 -r 10 -t 5m \
  --csv=data/<scheduler_name>/results

# 4. Capture events for Gantt chart
kubectl get events --sort-by=.lastTimestamp -o json > data/<scheduler_name>/events.json

# 5. Capture pod timeline
python research/gantt_generator.py --output data/<scheduler_name>/gantt_chart.png
```

### 2.3 After Experiment

```bash
# 1. Export Prometheus metrics
python research/export_metrics.py --scheduler <scheduler_name>

# 2. Take Grafana screenshots
# - Scheduler CPU/Memory dashboard
# - API Server dashboard
# - Node overview dashboard

# 3. Generate comparison report
python research/generate_report.py
```

---

## 3. Experimental Design

### 3.1 Test Parameters

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| **Cluster Size** | 4 nodes (t3.small) | Sufficient for testing, cost-effective |
| **Test Duration** | 5 minutes | Long enough for steady-state |
| **User Count** | 100 concurrent | Simulates moderate flash-sale |
| **Ramp-up Rate** | 10 users/sec | Gradual spike simulation |
| **Warmup Period** | 30 seconds | Allow system stabilization |

### 3.2 Test Scenarios

| Scenario | Description |
|----------|-------------|
| **Baseline** | Normal traffic (10 users) |
| **Moderate Spike** | 100 users, gradual ramp |
| **Flash Sale** | 500 users, instant spike |
| **Sustained Load** | 200 users, 10 min duration |

### 3.3 Controlled Variables

- Same cluster configuration
- Same application deployment
- Same Locust workload
- Same node count (4 nodes)
- Same instance type (t3.small)

---

## 4. Data Analysis

### 4.1 Key Comparisons

| Comparison | Metrics | Expected Outcome |
|------------|---------|------------------|
| **Scheduling Speed** | Scheduling latency, pending duration | Volcano slower due to gang coordination |
| **Overhead** | Scheduler CPU/memory | Volcano higher (always-on coordination) |
| **Fairness** | Pod distribution across nodes | Volcano more balanced (gang aware) |
| **Application Impact** | p99 latency, error rate | Should be similar |

### 4.2 Visualization

1. **Gantt Chart**: Pod scheduling timeline
2. **Line Charts**: CPU/memory over time
3. **Bar Charts**: Latency comparison (p50, p95, p99)
4. **Heatmaps**: Resource utilization across nodes

---

## 5. Expected Findings

Based on scheduler architecture:

| Aspect | Default | Volcano | NEXUS (Expected) |
|--------|---------|---------|------------------|
| **Idle CPU** | ~10m | ~50m+ | <10m |
| **Spike Response** | Uncoordinated | Coordinated | Event-triggered |
| **Gang Scheduling** | ❌ | ✅ | ✅ (on spike) |
| **Scheduling Latency** | Fast | Slower | Adaptive |
| **Control-Plane Load** | Low | High | Low (when idle) |

---

## 6. File Structure

```
research/
├── methodology.md           # This document
├── gantt_generator.py       # Gantt chart generator
├── export_metrics.py        # Prometheus metric exporter
├── generate_report.py       # Comparison report generator
└── queries/
    └── prometheus_queries.yaml  # All Prometheus queries

data/
├── default_scheduler/
│   ├── events.json
│   ├── gantt_chart.png
│   ├── metrics.json
│   └── results_*.csv
├── volcano_scheduler/
│   └── ...
└── nexus_scheduler/
    └── ...
```
