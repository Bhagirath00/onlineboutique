# Deployment Report
**Project:** Online Boutique - Cloud Native Microservices  
**Deployment Target:** AWS EKS (Elastic Kubernetes Service)  
**Region:** `us-east-1` (N. Virginia)

---

## 1. High-Level Infrastructure

I deployed a fault-tolerant, production-grade Kubernetes cluster using **Terraform (IaC)**.

| Component | Quantity | Specification | Role |
| :--- | :--- | :--- | :--- |
| **Control Plane** | 1 Cluster | **EKS v1.29** | Managed Master Nodes (API Server, Etcd) |
| **Worker Nodes** | 3 Nodes | **t3.small** (2 vCPU, 2GB RAM) | Runs application workloads |
| **Networking** | 1 VPC | `10.0.0.0/16` | Isolated Network Environment |
| **Subnets** | 6 Subnets | 3 Public / 3 Private | Public for LBs, Private for Apps |
| **Gateways** | 1 NAT GW | Single AZ | Outbound internet for private nodes |

---

## 2. Cluster Inventory (Workload Analysis) 

This data drove the capacity of all **12 microservices**.

| Service Name | Replicas | CPU Request (Min) | Memory Request (Min) | Limits (Max) |
| :--- | :--- | :--- | :--- | :--- |
| `frontend` | 1 | 100m (0.1 vCPU) | 64 Mi | 200m / 128Mi |
| `adservice` | 1 | 200m (0.2 vCPU) | 180 Mi | 300m / 300Mi |
| `cartservice` | 1 | 200m (0.2 vCPU) | 64 Mi | 300m / 128Mi |
| `checkoutservice` | 1 | 100m (0.1 vCPU) | 64 Mi | 200m / 128Mi |
| `currencyservice` | 1 | 100m (0.1 vCPU) | 64 Mi | 200m / 128Mi |
| `emailservice` | 1 | 100m (0.1 vCPU) | 64 Mi | 200m / 128Mi |
| `loadgenerator` | 1 | 300m (0.3 vCPU) | 256 Mi | 500m / 512Mi |
| `paymentservice` | 1 | 100m (0.1 vCPU) | 64 Mi | 200m / 128Mi |
| `productcatalog` | 1 | 100m (0.1 vCPU) | 64 Mi | 200m / 128Mi |
| `recommendations` | 1 | 100m (0.1 vCPU) | 220 Mi | 200m / 450Mi |
| `redis-cart` | 1 | 70m (0.07 vCPU) | 200 Mi | 125m / 256Mi |
| `shippingservice` | 1 | 100m (0.1 vCPU) | 64 Mi | 200m / 128Mi |
| **TOTALS** | **12 Pods** | **~1.57 vCPU** | **~1.36 GB** | |

---
## 3. Capacity Planning Logic 

**Question:** "Why did I select 3 nodes of `t3.small`?"

**The Math:**
1.  **Total App Usage:** ~1.57 vCPU.
2.  **Node Capacity (`t3.small`):** 2 vCPU available (but ~1.9 allocatable after OS overhead).
3.  **The Problem:** EKS installs system pods by default that consume resources:
    *   `aws-node` (CNI Networking)
    *   `kube-proxy`
    *   `coredns` (2 replicas)
    *   `metrics-server` (for HPA)

**Scenario A: 1 Node**
*   Capacity: 1.9 vCPU.
*   Demand: 1.57 (App) + 0.5 (System) = **2.07 vCPU**.
*   **Result:**  **CRASH**. The node would be Overcommitted. Pods would hang in `Pending` state.

**Scenario B: 2 Nodes**
*   Capacity: 3.8 vCPU.
*   Demand: 2.07 vCPU.
*   **Result:**  **Works**, but risky. If one node fails, we lose 50% capacity and might crash again.
*   **Headroom:** ~45%.

**Scenario C: 3 Nodes (Our Choice)**
*   Capacity: 5.7 vCPU.
*   Demand: 2.07 vCPU.
*   **Result:**  **Optimal**.
    *   **High Availability:** Can lose 1 node and still run perfectly.
    *   **Headroom:** Plenty of space for **Prometheus/Grafana** (which are heavy) and Locust load spikes.


## 5. Architectural Decision: Why EKS? 

Why strictly **AWS EKS** and not others?

| Feature | Self-Managed (EC2/Kops) | Locally (Minikube) | AWS EKS (Managed) |
| :--- | :--- | :--- | :--- |
| **Control Plane** | You manage (Hard) | You manage (Easy) | **AWS manages (Zero Limit)** |
| **Scalability** | Manual / Custom Scripts | None (1PC) | **Auto-Scaling Groups (ASG)** |
| **Networking** | Complex Overlay | Simple Docker bridge | **AWS VPC Integration (CNI)** |
| **Realism** | Medium | Low | **High (Industry Standard)** |

**Verdict:** We chose EKS to demonstrate **Enterprise capabilities**:
*   Integration with real **AWS Load Balancers**.
*   Real **VPC Networking** validation.
*   Proof that we can handle cloud-native IAM Roles (IRSA).

---

## 6. Challenges & Solutions 

1.  **Frontend Throttling:**
    *   *Issue:* Locust tests (100 users) caused 500 errors.
    *   *Fix:* Identified `cpu: 100m` limit was too low. HPA would be the production fix; for now we scaled nodes to ensure headroom.
2.  **Hidden Metrics Costs:**
    *   *Issue:* Prometheus scraped metrics every 15s, bloating memory.
    *   *Fix:* Tuned retention and scrape interval, and confirmed `t3.small` could handle the memory pressure (2GB RAM was the constraint).

