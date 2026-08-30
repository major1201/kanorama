# kanorama

Kanorama (Kubernetes + Panorama) is a read-only CLI that builds a cluster-wide
report from a Kubernetes cluster, helping you understand its composition from
multiple angles: version, authentication, nodes, kubelet configuration,
networking, ingresses, namespaces, workloads, storage, Helm releases, CRDs,
webhooks, certificates, and events.

It talks directly to the Kubernetes API with your kubeconfig (or in-cluster
service account), so it requires no agents, no metrics server, and no
installation inside the cluster.

## Features

- **Read-only** — only list/get and self-subject review API calls are performed; nothing in the cluster is mutated.
- **Modular report** — every aspect is a separate module that can be enabled,
  disabled, or run on its own.
- **Zero extra dependencies in the cluster** — works with any standard
  Kubernetes cluster and your existing kubeconfig.
- **Shared API cache** — the same node/pod lists are reused across modules to
  avoid duplicate API calls.
- **HTML export** — generate a self-contained HTML report with one tab per
  module.

## Requirements

- Go 1.27+ (to build from source)
- A kubeconfig with access to the target cluster, or an in-cluster service
  account

## Installation

```bash
go install github.com/major1201/kanorama@latest
```

Or build from source:

```bash
git clone https://github.com/major1201/kanorama.git
cd kanorama
go build -o kanorama .
```

## Usage

```bash
kanorama [flags]
```

### Flags

| Flag | Shorthand | Description |
| --- | --- | --- |
| `--modules` | `-m` | Comma-separated list of module IDs to run. This is an explicit allowlist that replaces the default-enabled set. |
| `--enable-modules` | `-E` | Comma-separated list of module IDs to enable in addition to the default-enabled modules. |
| `--disable-modules` | `-D` | Comma-separated list of module IDs to disable. Wins over `--enable-modules`. |
| `--list-modules` | `-L` | List all modules and whether they are enabled by default. |
| `--kubeconfig` | | Path to a kubeconfig file. Defaults to `KUBECONFIG` or `~/.kube/config`. |
| `--context` | | Name of the kubeconfig context to use. |
| `--html` | | Write the report to an HTML file with one tab per module instead of printing to stdout. |

Without `--kubeconfig`/`--context`, kanorama prefers in-cluster configuration
when running inside a pod, and falls back to the default kubeconfig otherwise.

### Examples

```bash
# Full report (all default-enabled modules)
kanorama

# Only the pods and nodes modules
kanorama -m pods,nodes

# Everything except storage and helm
kanorama -D storage,helm

# Start from version, then add auth on top
kanorama -m version -E auth

# List available modules
kanorama -L

# Generate an HTML report
kanorama --html report.html

# Target a specific kubeconfig context
kanorama --kubeconfig ~/.kube/config --context prod -m pods
```

Example output:

```
======================= Version =======================
API Server Version: v1.32.3-vke.8
======================= Network =======================
CNI:
  - Cilium
```

## Modules

| ID | Module | Description |
| --- | --- | --- |
| `version` | Version | Prints the Kubernetes API server version. |
| `auth` | Auth | Equivalent to `kubectl auth whoami`, plus a summary of effective permissions across all namespaces. |
| `nodes` | Nodes | Node counts (total / unschedulable / not-ready / control-plane), kubelet / container-runtime / kernel version distribution, capacity & allocatable resources, and well-known labels. Virtual-kubelet nodes are excluded. |
| `kubelet` | Kubelet | Prints the cluster's KubeletConfiguration from the `kubelet-config` ConfigMap in `kube-system` (kubeadm and kubeadm-based distros). |
| `network` | Network | Detects the CNI(s) in use via node annotations, DaemonSet names, and container images, and reports the DNS domain, pod/service CIDRs from `kubeadm-config`, and the actual cluster DNS provider (traced from port-53 services to their backing pods). |
| `ingresses` | Ingresses | Lists all Ingresses with the same columns as `kubectl get ingress -A`: namespace, name, class, hosts, address, ports, and age. |
| `namespaces` | Namespaces | Lists Namespaces with status, pod counts, aggregated CPU/memory requests & limits, and ResourceQuota details. |
| `pods` | Pods | Pod status summary, cluster request/limit usage versus allocatable, and Top 5 kinds & namespaces by pod count, CPU, memory, and extended resources (e.g. GPUs). |
| `daemonset` | DaemonSet | Lists every DaemonSet with namespace, name, desired/current/ready counts, node selector, and resource requests/limits. |
| `storage` | Storage | Lists StorageClasses with PVC/PV usage per class, plus Top 5 classes/kinds/namespaces by storage usage. |
| `helm` | Helm | Lists Helm releases (similar to `helm ls -A`) by decoding Helm 3 release Secrets. |
| `crd` | CRD | Lists CustomResourceDefinitions with short names, API version, scope, creation time, and instance count. |
| `webhooks` | Webhooks | Lists Validating and Mutating webhook configurations with webhook counts, intercepted resources, and failure/match policies. |
| `certificates` | Certificates | Checks TLS Secrets subject, expiry, key validity, and certificate/key match. |
| `events` | Events | Event totals by type, Top 10 reasons and involved kinds, and recent warnings. Disabled by default. |

Use `kanorama -L` (or `kanorama --help`) to see the current module list and
their default-enabled status.

## How module selection works

- By default, all modules with `EnableByDefault() == true` run.
- `--modules` replaces the base set with an explicit allowlist.
- `--enable-modules` adds modules on top of the base set.
- `--disable-modules` removes modules last, so it wins over `--enable-modules`.

Module IDs are the lowercase names shown by `kanorama -L` (for example
`pods`, `storage`). Unknown IDs are reported as errors.

## HTML report

`--html report.html` produces a single self-contained HTML file (no external
assets). Each module gets a tab; click a tab to switch modules. The output
inside each tab preserves the monospace-aligned tables of the terminal output.

## Development

```bash
go build ./...
go vet ./...
go test ./...
gofmt -l .
```

## License

[Apache License 2.0](LICENSE)
