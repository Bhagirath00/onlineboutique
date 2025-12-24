# Online Boutique - Final Commands (DevOps / Demo)

This file is a practical command reference for running the **Online Boutique** demo locally or on AWS EKS.

## 0) Safety / hygiene (do this once)

### Keep credentials out of git

- Put secrets in `.env` (already created in repo root).
- Ensure these never get committed (already covered by the root `.gitignore`):
  - `.env`, `.env.*`
  - `**/*.tfstate*`, `**/.terraform/`, `**/*.tfvars*`, `**/*tfplan*`

If you ever pushed Terraform state to a remote repo, rotate any credentials used during that deployment. 

## 1) Prerequisites (quick checks)

### Windows PowerShell

```powershell
docker version
kubectl version --client
terraform version
helm version
aws --version
minikube version
```

### Bash

```bash
docker version
kubectl version --client
terraform version
helm version
aws --version
minikube version
```

## 2) Local demo (Minikube)

### Start Minikube

```powershell
minikube start --driver=docker
kubectl get nodes
```

### Deploy the app

```powershell
kubectl apply -f release/kubernetes-manifests.yaml
kubectl get pods
kubectl get svc
```

### Open the frontend

```powershell
minikube service frontend-external
```

### Troubleshooting

```powershell
kubectl get events --sort-by=.metadata.creationTimestamp
kubectl describe pod -l app=frontend
kubectl logs -f deployment/frontend
```

### Cleanup local

```powershell
kubectl delete -f release/kubernetes-manifests.yaml
minikube stop
minikube delete
```

## 3) AWS EKS (Terraform)

### Authenticate

```powershell
aws sts get-caller-identity
```

### Plan + apply

```powershell
Set-Location terraform
terraform init
terraform plan -out tfplan
terraform apply tfplan
```

### Configure kubectl for the cluster

```powershell
aws eks --region us-east-1 update-kubeconfig --name online-boutique-cluster
kubectl get nodes
```

## 4) Deploy to EKS + debug

### Deploy

```powershell
Set-Location ..
kubectl apply -f release/kubernetes-manifests.yaml
kubectl get pods
kubectl get svc
```

### Get frontend URL (LoadBalancer)

```powershell
$lb = kubectl get svc frontend-external -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'
$lb
```

### Debug pods/services

```powershell
kubectl get pods -o wide
kubectl logs -f deployment/emailservice
kubectl describe pod -l app=emailservice
kubectl get events --sort-by=.metadata.creationTimestamp
```

## 5) Monitoring (Prometheus + Grafana via Helm)

### Install

```powershell
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo add grafana https://grafana.github.io/helm-charts
helm repo update

kubectl create namespace monitoring

helm install prometheus prometheus-community/prometheus --namespace monitoring `
  --set server.persistentVolume.enabled=false `
  --set alertmanager.persistentVolume.enabled=false

# IMPORTANT: pick a real password and keep it in your local .env (do not commit it).
helm install grafana grafana/grafana --namespace monitoring --set adminPassword=<SET_A_PASSWORD>
```

### Access

```powershell
kubectl port-forward -n monitoring svc/grafana 3000:80
kubectl port-forward -n monitoring svc/prometheus-server 9091:80
```

### Quick access

- Grafana: http://localhost:3000 (user: `admin`, password: what you set at install time)
- Prometheus: http://localhost:9091

### Grafana dashboard IDs (from imp.md)

Import in Grafana: ☰ Menu → Dashboards → New → Import → Enter ID

| ID    | Dashboard Name                  | Description                         |
| ----- | ------------------------------- | ----------------------------------- |
| 315   | Kubernetes Cluster Monitoring   | Overall cluster health, CPU, memory |
| 6417  | Kubernetes Pods                 | Detailed pod-level metrics          |
| 3119  | Kubernetes Cluster              | Alternative cluster monitoring view |
| 1860  | Node Exporter Full              | Detailed EC2/node statistics        |
| 7249  | Kubernetes Cluster (Prometheus) | Another cluster perspective         |
| 6879  | Analysis by Namespace           | Breakdown by Kubernetes namespace   |
| 8588  | Kubernetes Deployment           | Deployment-specific metrics         |
| 11454 | Kubernetes Pods Overview        | Simple pod overview                 |

Browse more: https://grafana.com/grafana/dashboards/

### Prometheus queries (from imp.md)

Run these at `http://localhost:9091` in the Expression box.

#### Basic

| Query                   | Description                          |
| ----------------------- | ------------------------------------ |
| `up`                    | Which targets are up (1) or down (0) |
| `count(kube_pod_info)`  | Total number of pods                 |
| `kube_pod_status_phase` | Pod status (Running/Pending/Failed)  |

#### Memory

| Query                                        | Description           |
| -------------------------------------------- | --------------------- |
| `container_memory_usage_bytes`               | Memory per container  |
| `sum(container_memory_usage_bytes) by (pod)` | Memory grouped by pod |
| `container_memory_usage_bytes / 1024 / 1024` | Memory in MB          |

#### CPU

| Query                                                       | Description         |
| ----------------------------------------------------------- | ------------------- |
| `container_cpu_usage_seconds_total`                         | Raw CPU usage       |
| `rate(container_cpu_usage_seconds_total[5m])`               | CPU rate over 5 min |
| `sum(rate(container_cpu_usage_seconds_total[5m])) by (pod)` | CPU by pod          |

#### Network

| Query                                             | Description            |
| ------------------------------------------------- | ---------------------- |
| `container_network_receive_bytes_total`           | Network bytes received |
| `container_network_transmit_bytes_total`          | Network bytes sent     |
| `rate(container_network_receive_bytes_total[5m])` | Receive rate           |

#### Pod status

| Query                                                    | Description              |
| -------------------------------------------------------- | ------------------------ |
| `kube_pod_container_status_restarts_total`               | Container restarts count |
| `sum(kube_pod_container_status_restarts_total) by (pod)` | Restarts by pod          |
| `kube_pod_container_status_waiting_reason`               | Why pods are waiting     |

## 6) Load testing (Locust)

```powershell
python -m pip install --upgrade pip
pip install locust

$lb = kubectl get svc frontend-external -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'
locust -f src\loadgenerator\locustfile.py --host "http://$lb"
```

If you use a different locustfile, replace the `-f` path.

## 7) “Self-healing” check

```powershell
kubectl get pods -w
kubectl delete pod -l app=emailservice
kubectl get events --sort-by=.metadata.creationTimestamp
```

## 8) Cleanup (important)

### Remove app + monitoring

```powershell
kubectl delete -f release/kubernetes-manifests.yaml
helm uninstall prometheus -n monitoring
helm uninstall grafana -n monitoring
kubectl delete namespace monitoring
```

### Destroy AWS infrastructure

```powershell
Set-Location terraform
terraform destroy -auto-approve
```

## Appendix: Quick “status snapshot” (PowerShell)

```powershell
Write-Host "=== NODES ==="; kubectl get nodes
Write-Host "=== PODS ==="; kubectl get pods
Write-Host "=== SERVICES ==="; kubectl get svc
Write-Host "=== MONITORING PODS ==="; kubectl get pods -n monitoring
```

## Appendix: Verification commands (from imp.md)

```powershell
# Pods
kubectl get pods
kubectl get pods -A
kubectl get pods -n monitoring
kubectl describe pod <pod-name>

# Nodes
kubectl get nodes
kubectl get nodes -o wide
kubectl describe nodes
kubectl top nodes

# Services
kubectl get svc
kubectl get svc -n monitoring
kubectl get svc frontend-external

# LoadBalancer hostname
$lb = kubectl get svc frontend-external -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'
$lb

# Deployments
kubectl get deployments
kubectl get deployments -n monitoring
kubectl rollout status deployment/<deployment-name>

# Images currently running
kubectl get pods -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.containers[*].image}{"\n"}{end}'
kubectl describe pods | findstr "Image:"

# EKS status (if using AWS)
aws eks describe-cluster --name online-boutique-cluster --query "cluster.status"

# Node group helpers
aws eks list-nodegroups --cluster-name online-boutique-cluster
aws eks describe-nodegroup --cluster-name online-boutique-cluster --nodegroup-name <NODEGROUP_NAME> --query "nodegroup.status"
aws eks describe-nodegroup --cluster-name online-boutique-cluster --nodegroup-name <NODEGROUP_NAME> --query "nodegroup.scalingConfig"
```
