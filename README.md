# k8s-healthcheck

A small Go application that runs **inside** a Kubernetes cluster, surveys the
cluster's health, and writes a single PDF report. It is designed to work on
**any K8s distribution** (kubeadm, k3s, RKE2, EKS, GKE, AKS, OpenShift…) by
talking to the standard Kubernetes API and degrading gracefully when an
optional data source (metrics-server, /etc/kubernetes/pki, ComponentStatus)
is missing.

## What it collects

Inspired by the [HackMD K8s 健檢](https://hackmd.io/@wu-andy/S14JZOgvxe) checklist plus extra coverage:

| Section | Items |
| --- | --- |
| Cluster overview | Server version, platform, node / namespace / pod counts, distribution tag |
| Nodes | Roles, status, kubelet/runtime/OS/kernel, internal IP, age, taints, pods-on-node, **all node Conditions** (Ready / Memory / Disk / PID / NetworkUnavailable) |
| Node resource usage | CPU/memory used vs capacity (% and absolute) via `metrics.k8s.io`, pod count vs pod capacity |
| Pod summary | Total / Running / Pending / Succeeded / Failed / Unknown |
| Problem pods | Pods not Ready, CrashLoopBackOff, ImagePullBackOff, ≥5 restarts, Pending — with reason and last container message |
| Top consumers | Top 10 pods by CPU and Top 10 by memory |
| Workloads | Deployments / DaemonSets / StatefulSets / ReplicaSets / Jobs / CronJobs totals + an "Unhealthy" table with desired/ready and reason |
| Storage | PV phase counts, PVC phase counts, StorageClasses (incl. default), pending PVCs |
| Control-plane health | `/healthz`, `/livez`, `/readyz` (verbose — surfaces failing checks like etcd, scheduler, controller-manager). Falls back to legacy `componentstatuses` when present |
| Certificates | Walks `/etc/kubernetes/pki` (mounted via hostPath) and reports each cert's expiry / days remaining / OK / WARN / EXPIRING SOON / EXPIRED. Skipped on distros without that directory |
| Events | Last 50 non-Normal events across all namespaces with reason, object, namespace, count |
| Errors | Any non-fatal collector errors, so partial sections are auditable |

## Quick start

```bash
# 1. build & push the image (replace with your own registry)
docker build -t ghcr.io/you/k8s-healthcheck:latest .
docker push ghcr.io/you/k8s-healthcheck:latest

# 2. point the manifests at your image
sed -i 's|ghcr.io/brobridge/k8s-healthcheck:latest|ghcr.io/you/k8s-healthcheck:latest|' deploy/*.yaml

# 3. run the pod and pull the PDF out in one shot
./scripts/run-and-fetch.sh ./k8s-health-report.pdf
```

The helper script applies `deploy/rbac.yaml` + `deploy/pod.yaml`, waits for
the collector to finish writing the PDF, copies it locally, and deletes the
pod. The RBAC namespace is left intact for re-runs.

### Manual flow

```bash
kubectl apply -f deploy/rbac.yaml
kubectl apply -f deploy/pod.yaml

# wait for the report to be produced
kubectl -n k8s-healthcheck logs -f k8s-healthcheck

# copy the PDF out
PDF=$(kubectl -n k8s-healthcheck exec k8s-healthcheck -- ls /reports | head -1)
kubectl -n k8s-healthcheck cp "k8s-healthcheck:/reports/${PDF}" "./${PDF}"

# clean up
kubectl -n k8s-healthcheck delete pod k8s-healthcheck
```

### As a Job (no `kubectl cp`)

`deploy/job.yaml` is provided for CI / one-off runs where you have a real
PVC (or another out-of-band way of retrieving the file). The Job has
`ttlSecondsAfterFinished: 3600` so it self-cleans an hour after completion.

## Why it works on any distro

* **Pure Kubernetes API** — no `kubectl`, `etcdctl`, or shelling-out. The
  binary uses client-go, so anything reachable from a Pod's
  ServiceAccount works the same on every distribution.
* **Optional inputs**:
  * metrics-server: missing -> resource-usage section says so, rest of report still rendered.
  * `/etc/kubernetes/pki`: missing on managed K8s -> cert section skipped.
  * `componentstatuses`: deprecated and disabled on many distros -> falls back to `/healthz` verbose check.
* **Tolerates every taint** so the pod can run on control-plane-only or
  GPU-tainted clusters.
* **Distroless `nonroot` base image** with read-only rootfs and dropped
  capabilities — passes restricted PSA out of the box.
* **No external font files** — uses gofpdf's built-in Helvetica; the docker
  image is ~25 MB and contains only the static binary.

## Flags

| Flag | Default | Purpose |
| --- | --- | --- |
| `--out` | `/reports` | Directory to write the PDF into |
| `--timeout` | `5m` | Hard deadline for the whole collection |
| `--kubeconfig` | _(empty)_ | Path to a kubeconfig if running outside the cluster |
| `--pki-dir` | `/host/etc/kubernetes/pki` | Where to look for kubeadm certs (mount via hostPath) |
| `--sleep-after` | `0` | After writing the PDF, hold the pod alive for this long (so `kubectl cp` can run) |

## Local development

```bash
go mod tidy
go build ./...
# point at any kubeconfig you can read
go run . --kubeconfig=$HOME/.kube/config --out=./out
```

## Layout

```
.
├── main.go                       # CLI entry, flags, lifecycle
├── internal/
│   ├── collector/                # one file per data source
│   │   ├── collector.go          # orchestrator + client wiring
│   │   ├── cluster.go            # version, counts, distro detection
│   │   ├── nodes.go              # node table + conditions
│   │   ├── metrics.go            # metrics-server: nodes & pods
│   │   ├── pods.go               # pod summary + problem detection
│   │   ├── workloads.go          # Deploys/DS/STS/RS/Jobs/CronJobs
│   │   ├── storage.go            # PV / PVC / StorageClasses
│   │   ├── events.go             # warning events (last 50)
│   │   ├── components.go         # legacy componentstatuses
│   │   ├── api_health.go         # /healthz /livez /readyz?verbose
│   │   └── certs.go              # x509 expiry from PKI dir
│   ├── model/model.go            # data types shared by collectors + report
│   └── report/pdf.go             # gofpdf rendering, sectioned tables
├── Dockerfile                    # 2-stage, distroless final
├── deploy/
│   ├── rbac.yaml                 # Namespace + SA + ClusterRole(Binding)
│   ├── pod.yaml                  # default deployment (sleep-then-cp pattern)
│   └── job.yaml                  # alternative Job-based deployment
├── scripts/run-and-fetch.sh      # apply -> wait -> kubectl cp -> cleanup
└── README.md
```
