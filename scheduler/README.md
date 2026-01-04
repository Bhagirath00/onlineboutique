# NEXUS Scheduler

**Event-Driven Lightweight Kubernetes Scheduler for Flash-Sale Workloads**

## Overview

NEXUS is a custom Kubernetes scheduler designed for PhD research comparing scheduling strategies under flash-sale traffic conditions. Unlike Volcano (always-on batch scheduler), NEXUS uses an **event-driven architecture** that:

- **IDLE Mode**: Near-zero CPU overhead when cluster is stable
- **ACTIVE Mode**: Triggers gang scheduling only during traffic spikes
- **Auto-cooldown**: Returns to idle after spike subsides

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    NEXUS Scheduler                          │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│   ┌─────────────┐      ┌─────────────┐      ┌───────────┐  │
│   │   Pod       │      │   Spike     │      │   Gang    │  │
│   │  Informer   │─────▶│  Detector   │─────▶│ Scheduler │  │
│   └─────────────┘      └─────────────┘      └───────────┘  │
│          │                    │                    │        │
│          ▼                    ▼                    ▼        │
│   ┌─────────────┐      ┌─────────────┐      ┌───────────┐  │
│   │  Pending    │      │   State     │      │   Node    │  │
│   │   Queue     │      │   Machine   │      │  Selector │  │
│   └─────────────┘      │(IDLE/ACTIVE)│      └───────────┘  │
│                        └─────────────┘                      │
│                                                             │
│   ┌─────────────────────────────────────────────────────┐  │
│   │                  Metrics Exporter (:9099)           │  │
│   │  • nexus_scheduler_state                            │  │
│   │  • nexus_pending_pods                               │  │
│   │  • nexus_pods_scheduled_total                       │  │
│   │  • nexus_state_changes_total                        │  │
│   └─────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

## Key Features

| Feature | Description |
|---------|-------------|
| **Event-Driven** | Uses K8s informers, not polling |
| **State Machine** | IDLE ↔ ACTIVE based on pending pod count |
| **Gang Scheduling** | Schedules pods together during spikes |
| **Prometheus Metrics** | Full observability for research |
| **Low Overhead** | <50m CPU when idle |

## How It Works

### State Machine
```
                          pending pods >= 5
                    ┌─────────────────────────┐
                    │                         ▼
              ┌─────┴─────┐             ┌───────────┐
              │   IDLE    │             │  ACTIVE   │
              │           │             │  (Gang)   │
              └───────────┘             └─────┬─────┘
                    ▲                         │
                    │  pending = 0 && cooldown
                    └─────────────────────────┘
```

### Scheduling Behavior

| State | Behavior |
|-------|----------|
| **IDLE** | Schedule pods one-by-one (normal) |
| **ACTIVE** | Gang schedule all pending pods together |

## Files

```
scheduler/
├── main.go           # Scheduler implementation
├── go.mod            # Go module definition
├── Dockerfile        # Container build
├── deployment.yaml   # Kubernetes manifests
└── README.md         # This file
```

## Usage

### 1. Build Docker Image
```bash
cd scheduler
docker build -t nexus-scheduler:latest .
```

### 2. Push to ECR (after terraform apply)
```bash
aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin <account>.dkr.ecr.us-east-1.amazonaws.com
docker tag nexus-scheduler:latest <account>.dkr.ecr.us-east-1.amazonaws.com/nexus-scheduler:latest
docker push <account>.dkr.ecr.us-east-1.amazonaws.com/nexus-scheduler:latest
```

### 3. Deploy to Kubernetes
```bash
kubectl apply -f deployment.yaml
```

### 4. Configure Pods to Use NEXUS
Add this to your pod spec:
```yaml
spec:
  schedulerName: nexus-scheduler
```

## Metrics

Access at `http://<pod-ip>:9099/metrics`

| Metric | Type | Description |
|--------|------|-------------|
| `nexus_scheduler_state` | Gauge | 0=IDLE, 1=ACTIVE |
| `nexus_pending_pods` | Gauge | Current pending pod count |
| `nexus_pods_scheduled_total` | Counter | Total pods scheduled |
| `nexus_state_changes_total` | Counter | State transitions |

## Configuration

Edit these constants in `main.go`:

| Constant | Default | Description |
|----------|---------|-------------|
| `spikeThreshold` | 5 | Pending pods to trigger ACTIVE |
| `cooldownDuration` | 30s | Wait before returning to IDLE |

## Comparison with Volcano

| Aspect | Default | Volcano | NEXUS |
|--------|---------|---------|-------|
| Idle CPU | 10m | 50m+ | **<10m** |
| Gang Support | ❌ | ✅ | ✅ |
| Event-Driven | ❌ | ❌ | **✅** |
| Spike Detection | ❌ | ❌ | **✅** |
| Control-Plane Overhead | Low | High | **Adaptive** |
