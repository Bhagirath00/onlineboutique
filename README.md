<p align="center">
  <img src="src/frontend/static/icons/Hipster_HeroLogo.svg" width="400" alt="Online Boutique" />
</p>

<p align="center">
  <img alt="AWS" src="https://img.shields.io/badge/AWS-Cloud-232F3E?logo=amazonaws&logoColor=white" />
  <img alt="Amazon EKS" src="https://img.shields.io/badge/EKS-Kubernetes-FF9900?logo=amazoneks&logoColor=white" />
  <img alt="Kubernetes" src="https://img.shields.io/badge/Kubernetes-Orchestration-326CE5?logo=kubernetes&logoColor=white" />
  <img alt="Terraform" src="https://img.shields.io/badge/Terraform-IaC-844FBA?logo=terraform&logoColor=white" />
</p>

**Online Boutique** is a cloud-native microservices demo application (a fork of GoogleCloudPlatform/microservices-demo).

**Upstream repository:** https://github.com/GoogleCloudPlatform/microservices-demo

## About this fork

- **Original project:** Online Boutique originally comes from Google Cloud’s `GoogleCloudPlatform/microservices-demo`. This repository is a **clone/fork** used for learning and practice.
- **Why I chose it (Kubernetes research):** I chose this project to learn how Kubernetes behaves in real scenarios:
  - Kubernetes continuously reconciles the desired state. If a Pod is killed/crashes or a node becomes unhealthy, workloads are **restarted and/or rescheduled** to keep the app available.
  - Under user spikes, the platform can remain stable by running enough healthy replicas and distributing them across nodes. (Autoscaling such as HPA/Cluster Autoscaler can be layered on top depending on the cluster setup.)
- **DevOps practice in this repo:**
  - **Load testing:** includes a Locust-based load generator service to simulate sudden spikes.
  - **Monitoring:** Prometheus + Grafana steps are documented in `commands.md`.
  - **CI/CD:** used as a place to practice GitHub Actions-style CI/CD workflows.

**This workspace is organized as a DevOps project:**

- **Kubernetes manifests:** `release/` (deployable as-is)
- **Infrastructure (IaC):** `terraform/` (provisions AWS EKS)
- **Microservices source code:** `src/`

**AWS services you’ll commonly use (docs):**

- **Amazon EKS:** https://docs.aws.amazon.com/eks/
- **AWS Load Balancer Controller:** https://kubernetes-sigs.github.io/aws-load-balancer-controller/
- **IAM Roles for Service Accounts (IRSA):** https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html
- **CloudWatch Container Insights (optional):** https://docs.aws.amazon.com/AmazonCloudWatch/latest/monitoring/ContainerInsights.html
- **AWS X-Ray (optional):** https://docs.aws.amazon.com/xray/

## Project goals (this repo)

- Deploy Online Boutique locally (Minikube) or on AWS (EKS)
- Practice IaC using Terraform and Kubernetes manifests
- Keep credentials out of git (use `.env` + `.gitignore`)

## Screenshots

| Home Page                                                                                                               | Checkout Screen                                                                                                          |
| ----------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| [![Screenshot of store homepage](./docs/img/online-boutique-frontend-1.png)](./docs/img/online-boutique-frontend-1.png) | [![Screenshot of checkout screen](./docs/img/online-boutique-frontend-2.png)](./docs/img/online-boutique-frontend-2.png) |

## Quickstart (Local Kubernetes - Minikube)

1. Start Minikube:

```powershell
minikube start --driver=docker
kubectl get nodes
```

2. Deploy the app (use deployable manifests under `/release`):

```powershell
kubectl apply -f release/kubernetes-manifests.yaml
kubectl get pods
```

3. Open the frontend:

```powershell
minikube service frontend-external
```

4. Cleanup:

```powershell
kubectl delete -f release/kubernetes-manifests.yaml
minikube delete
```

## Quickstart (AWS EKS - Terraform)

This repo contains Terraform to provision an EKS cluster in `us-east-1` by default (see `terraform/variables.tf`).

1. Prereqs:

- **AWS CLI configured:** `aws sts get-caller-identity` must work
- **Tools installed:** `terraform`, `kubectl`

2. Provision the EKS cluster:

```powershell
Set-Location terraform
terraform init
terraform plan -out tfplan
terraform apply tfplan
```

3. Configure kubectl:

```powershell
aws eks --region us-east-1 update-kubeconfig --name online-boutique-cluster
kubectl get nodes
```

4. Deploy Online Boutique:

```powershell
Set-Location ..
kubectl apply -f release/kubernetes-manifests.yaml
kubectl get pods
```

5. Get the frontend endpoint:

```powershell
$lb = kubectl get svc frontend-external -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'
$lb
```

6. Cleanup (important to avoid AWS charges):

```powershell
kubectl delete -f release/kubernetes-manifests.yaml
Set-Location terraform
terraform destroy
```

## Notes

- **Runbook:** for full command reference (Minikube, EKS, monitoring), see [commands.md](commands.md).
- **Manifests:** YAMLs in `kubernetes-manifests/` are intended for `skaffold`; use `release/kubernetes-manifests.yaml` for direct `kubectl apply`.
- **Secrets:** do not commit credentials. Use `.env` locally and keep it ignored.
- **Terraform state:** `terraform.tfstate*` should not be committed. If it was ever committed/pushed, rotate any exposed credentials.

## AWS references (optional)

- EKS getting started: https://docs.aws.amazon.com/eks/latest/userguide/getting-started.html
- Exposing Services on EKS (LoadBalancer/Ingress): https://docs.aws.amazon.com/eks/latest/userguide/load-balancing.html
- Kubernetes autoscaling on EKS: https://docs.aws.amazon.com/eks/latest/userguide/autoscaling.html
- Observability on EKS (CloudWatch): https://docs.aws.amazon.com/eks/latest/userguide/cloudwatch.html

## Architecture

**Online Boutique** is composed of 11 microservices written in different
languages that talk to each other over gRPC. See the [Development Principles](/docs/development-principles.md) doc for more information.

**1) Microservices architecture (original)**

This shows the core 11-service application design.

[![Architecture of microservices](./docs/img/architecture-diagram.png)](./docs/img/architecture-diagram.png)

**2) DevOps / AWS EKS architecture (this repo)**

This shows how the same services fit into an AWS EKS + DevOps workflow (CI/CD, load testing, monitoring).

[![Architecture of microservices (DevOps / AWS EKS)](./docs/img/architecture-diagram.svg)](./docs/img/architecture-diagram.svg)

Find **Protocol Buffers Descriptions** at the [`./pb` directory](./pb).

| Service                                              | Language      | Description                                                                                                                       |
| ---------------------------------------------------- | ------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| [frontend](./src/frontend)                           | Go            | Exposes an HTTP server to serve the website. Does not require signup/login and generates session IDs for all users automatically. |
| [cartservice](./src/cartservice)                     | C#            | Stores the items in the user's shopping cart in Redis and retrieves it.                                                           |
| [productcatalogservice](./src/productcatalogservice) | Go            | Provides the list of products from a JSON file and ability to search products and get individual products.                        |
| [currencyservice](./src/currencyservice)             | Node.js       | Converts one money amount to another currency. Uses real values fetched from European Central Bank. It's the highest QPS service. |
| [paymentservice](./src/paymentservice)               | Node.js       | Charges the given credit card info (mock) with the given amount and returns a transaction ID.                                     |
| [shippingservice](./src/shippingservice)             | Go            | Gives shipping cost estimates based on the shopping cart. Ships items to the given address (mock)                                 |
| [emailservice](./src/emailservice)                   | Python        | Sends users an order confirmation email (mock).                                                                                   |
| [checkoutservice](./src/checkoutservice)             | Go            | Retrieves user cart, prepares order and orchestrates the payment, shipping and the email notification.                            |
| [recommendationservice](./src/recommendationservice) | Python        | Recommends other products based on what's given in the cart.                                                                      |
| [adservice](./src/adservice)                         | Java          | Provides text ads based on given context words.                                                                                   |
| [loadgenerator](./src/loadgenerator)                 | Python/Locust | Continuously sends requests imitating realistic user shopping flows to the frontend.                                              |

## Features

- **[Kubernetes](https://kubernetes.io):** The app runs on any Kubernetes cluster (e.g., Minikube locally or AWS EKS in this repo).
- **[gRPC](https://grpc.io):** Microservices use a high volume of gRPC calls to
  communicate to each other.
- **Service Mesh (optional):** The upstream demo supports Istio; this repo focuses on core Kubernetes deployment.
- **[OpenCensus](https://opencensus.io/) Tracing:** Most services are
  instrumented using OpenCensus trace interceptors for gRPC/HTTP.
- **Monitoring (optional):** `commands.md` includes Prometheus + Grafana installation via Helm.
- **[Skaffold](https://skaffold.dev) (optional):** Useful for local dev workflows; this repo uses `/release` manifests for direct `kubectl apply`.
- **Synthetic Load Generation:** The application demo comes with a background
  job that creates realistic usage patterns on the website using
  [Locust](https://locust.io/) load generator.

## Local Development

If you would like to contribute features or fixes to this app, see the [Development Guide](/docs/development-guide.md) on how to build this demo locally.

---

This is a learning/devops project fork and is not an official Google project.
