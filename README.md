# KubeGo

A browser UI and REST API for [KubeVirt](https://kubevirt.io/) — drives VMs as Kubernetes resources so a single commodity cluster can carry VMs alongside containers. Forked from [PassGo Web](https://github.com/rootisgod/passgo-webui) and progressively retargeted away from Canonical Multipass.

> **Status: M1–M3 slice A (pre-alpha).** The binary builds, boots against a cluster, and can create / list / start / stop / delete an Ubuntu 24.04 VM end-to-end via `quay.io/containerdisks/ubuntu:24.04` (a containerDisk — ephemeral, no PVC yet). Snapshots, disks, resize, console, exec, transfers, Ansible and the LLM tools still return `ErrNotImplemented`; frontend is still PassGo-shaped. See [PLAN.md](PLAN.md) for the full milestone breakdown.

## Bare-minimum quickstart on a new machine

This is the shortest path from a clean laptop to "binary running against a local KinD cluster with KubeVirt installed". Four steps; the third one takes 5–10 minutes on first run and the rest are seconds.

### 1. Install the six dependencies

You need `go`, `docker` (Docker Desktop or engine, so KinD has a container runtime to run on), `kind`, `kubectl`, `curl`, and `git`. Nothing else.

**macOS (Homebrew):**

```bash
brew install go kind kubectl
# Docker Desktop: https://www.docker.com/products/docker-desktop/
# (curl and git ship with macOS already.)
```

**Ubuntu/Debian:**

```bash
sudo apt update && sudo apt install -y golang-go kubectl curl git docker.io
# Install kind (no apt package yet):
curl -Lo /tmp/kind https://kind.sigs.k8s.io/dl/latest/kind-linux-amd64
chmod +x /tmp/kind && sudo mv /tmp/kind /usr/local/bin/kind
# Let your user run docker without sudo (takes effect on next login):
sudo usermod -aG docker "$USER"
```

Verify: `go version`, `docker info`, `kind version`, `kubectl version --client`. The repo's Go build requires **Go 1.26.1** — if `apt` gives you an older version, install from [go.dev/dl](https://go.dev/dl/) instead.

### 2. Clone the repo

```bash
git clone https://github.com/rootisgod/kubego-webui.git
cd kubego-webui
```

### 3. Create a KinD cluster with KubeVirt installed

```bash
./scripts/kind-up.sh
```

This creates a `kubego-dev` cluster, installs the KubeVirt operator + CR, patches it into software-emulation mode (KinD nodes have no `/dev/kvm`), installs CDI, and waits for both to become `Available`. Idempotent — re-run it any time. First run takes 5–10 minutes; subsequent runs are seconds.

### 4. Build the binary and run it

```bash
task run
```

This builds `./kubego` and runs it against `~/.kube/config` on port 8081. Leave it running; open `http://localhost:8081` (login `admin` / `admin`). The UI is still the placeholder — the real verification is in the logs:

```
level=INFO msg="kubevirt driver ready" source=kubeconfig:... namespace=default server=...
level=INFO msg="kubevirt detected" group=kubevirt.io version=v1 ...
```

Or hit the API directly:

```bash
curl -s localhost:8081/api/v1/version
```

That's the full bare-minimum path. VM-operation endpoints return an `ErrNotImplemented` error — that is the M0 state; M1+ wires them against real VM CRs. See the [Roadmap](#roadmap) below.

### Tear down

```bash
# Stop the binary: Ctrl-C in its terminal.
task kind:down
```

## What is it aimed at?

Any Kubernetes cluster that can run KubeVirt — homelab, on-prem, managed (EKS/AKS/GKE with nested-virt nodes), or a dev KinD cluster. There is no KinD-specific code in the binary; KinD is only the dev/test convenience, and the `scripts/` directory codifies that setup. The deployment target is in-cluster as a pod (Helm chart lands in M0 Slice 3); running standalone against `~/.kube/config` is the supported dev mode.

## Variants of the quickstart

### Validate the build without any cluster

If you just want to confirm the binary is wired correctly before touching KinD, the smoke test works against any kubeconfig — including an unreachable one. The KubeVirt probe logs a warning; the HTTP server comes up anyway.

```bash
./scripts/smoke-test.sh
# or: task smoke
```

Expected output ends with `Smoke test passed.` and the captured startup log contains one of:

```
level=WARN  msg="kubevirt probe failed" err="...dial tcp ...: connect: connection refused"
level=WARN  msg="kubevirt not installed on target cluster — VM operations will return 501 until KubeVirt is installed"
level=INFO  msg="kubevirt detected" group=kubevirt.io version=v1 ...
```

### Point at an existing cluster instead of KinD

Skip step 3. Step 4 works unchanged — the binary only cares that `--kubeconfig` resolves to a reachable API server. Production-grade clusters with real nodes don't need the `useEmulation` patch.

### Override KinD defaults

```bash
KUBEGO_KIND_CLUSTER=my-cluster \
KUBEVIRT_VERSION=v1.2.0 \
CDI_VERSION=v1.59.0 \
./scripts/kind-up.sh
```

### Using Task

[`task`](https://taskfile.dev/) is optional — every Taskfile entry maps 1:1 to a shell command you've already seen above. `task kind:up`, `task run`, `task smoke`, `task kind:down` cover the quickstart path.

> KinD runs KubeVirt in **software emulation** (10–100× slower than KVM, because KinD nodes have no `/dev/kvm`). Fine for wiring and integration tests; don't run real workloads. See PLAN.md §5.

## What works today

- `go build ./...` and `go vet ./...` clean on Go 1.26.1.
- Kubernetes config discovery: in-cluster first, then `--kubeconfig` / `$KUBECONFIG` / `~/.kube/config`.
- Startup discovery probe for `kubevirt.io/v1`; logs, not crashes, when absent.
- Session + bearer-token auth, rate limiting, event log, config load/save, REST endpoints for every resource the driver exposes.
- Filesystem-backed helpers for cloud-init templates and Ansible playbooks survive the rewrite.
- **First-VM backend slice:** `POST /api/v1/vms` creates a `VirtualMachine` CR plus a cloud-init `Secret` (owner-ref'd to the VM so it GCs on delete). `GET /api/v1/vms[/{name}]` reads state from `status.printableStatus`, falling back to `spec.runStrategy` pre-observation. `start` / `stop` patch `runStrategy`; `DELETE` removes the CR. containerDisk-only for now — disks are ephemeral.
- **Multi-cluster switcher:** `GET /api/v1/clusters` enumerates kubeconfig contexts; `POST /api/v1/clusters/select` flips the active one. `POST /api/v1/clusters/kind` and `DELETE /api/v1/clusters/kind/{name}` shell out to `kind` with SSE progress, auto-selecting the new context on create and falling back to a surviving context on delete. Sidebar header exposes the switcher; in-cluster mode hides KinD ops.

## What does not work yet

| Area | Status | Milestone |
|------|--------|-----------|
| Persistent root disks (DataVolume + PVC) | Stubbed — containerDisk only | M3 slice D |
| Serial console | Routes removed | M2 (virt-api `SerialConsole` proxy) |
| Snapshots / clone / disks | Stubbed | M4 |
| Resize / exec / bulk | Stubbed | M5 |
| Multi-namespace tenancy | Single-namespace only | M6 |
| Ansible inventory, file transfer | Stubbed | M7 |
| Metrics / Kubernetes Events | Stubbed | M8 |
| CAPK (workload clusters) | Not in scope | M9 |
| Helm chart | Missing | M0 Slice 3 |
| Frontend (Vue app) | Still calls PassGo routes | next slice |

The Vue app in `frontend/` builds but renders broken against the current backend — it asks for removed routes (`/mounts`, `/host/resources`, `/recover`) and its VM-create dialog hasn't been wired to the new endpoint. Next slice unbreaks list + create + lifecycle there and renames Mounts → Disks.

## Development

### Useful Taskfile targets

```
task build-backend       Build kubego without rebuilding the frontend
task run                 Build + run against ~/.kube/config
task dev-backend         go run ./cmd/server (no binary produced)
task smoke               End-to-end smoke test
task kind:install        Install the kind CLI
task kind:up             Create kubego-dev KinD cluster + KubeVirt + CDI
task kind:down           Delete kubego-dev KinD cluster
task kind:reset          Delete and recreate
task install-kubectl     Install the kubectl CLI
task install-k9s         Install the k9s CLI
task test                go test ./... -v
task clean               Remove all build artifacts
```

All Taskfile entries are shell commands; `cat Taskfile.yml` if you want the plain invocation. The `install-*` and `kind:install` tasks each call a script in `scripts/` directly — you can run those without Task installed.

### Layout

```
cmd/server/              Go entrypoint, embeds frontend/dist and cloud-init templates
internal/api/            HTTP handlers and middleware
internal/config/         App config load/save, password hashing
pkg/kubevirt/            Driver interface (all VM operations live behind here)
  client.go              NewClient, rest.Config wiring
  vms.go                 VM lifecycle via dynamic client (Launch/List/Get/Start/Stop/Delete)
  unimplemented.go       Stub for methods not yet ported; VM methods now live in vms.go
  types.go               Wire types (VMInfo, DiskInfo, ClusterResources, ...)
  constants.go           Validation helpers, defaults
  cloudinit_files.go     Template read/write/scan (filesystem, not KubeVirt-coupled)
  playbooks_files.go     Ansible playbook read/write (same)
frontend/                Vue 3 app (still PassGo-shaped; ports in M0 Slice 3)
scripts/
  kind-up.sh             Create + configure kubego-dev KinD cluster
  kind-down.sh           Delete kubego-dev KinD cluster
  smoke-test.sh          End-to-end binary smoke test
PLAN.md                  The rewrite roadmap and design decisions
```

### Driver interface

`pkg/kubevirt.Client` is the single seam between HTTP handlers and the KubeVirt API. Every milestone M1–M9 fills in method bodies without changing handler code. The interface preserves PassGo's signatures where sensible (single-namespace string identifiers in M0) and introduces `VMRef{Namespace, Name}` in M6 when multi-namespace mode lands.

To implement a method: replace the `ErrNotImplemented` return in `pkg/kubevirt/unimplemented.go` (or split the method into a dedicated file, e.g. `vms.go`, and have the struct delegate there). All handler code continues to work.

## API

All endpoints are under `/api/v1/`. Auth via session cookie or `Authorization: Bearer <token>`. The [PLAN.md §3](PLAN.md) table lists every endpoint with its KubeGo verdict. Notable PassGo → KubeGo changes:

- `/host/resources` → `/cluster/resources`.
- `/vms/{name}/mounts*` → `/vms/{name}/disks*` (PVC hot-plug, not host bind-mounts).
- `/vms/{name}/recover`, `/vms/purge`, `/host/files`, `/host/home`, `/mounts/open` — **removed**.
- `/vms/{name}/suspend` — kept as alias for stop.
- Shell routes dropped for M0; re-added in M2 against virt-api.
- `/clusters*` — new; kubeconfig context list + select, KinD create/delete with SSE progress streams.

## Roadmap

| Milestone | Scope |
|-----------|-------|
| M0 | Driver interface + cluster wiring + CRD probe. |
| M1–M3 slice A (current) | VM create/list/start/stop/delete against containerDisk. Backend only. |
| M1–M3 slice B (next) | Frontend port: wire VM list + create dialog + lifecycle against the new backend. |
| M1–M3 slice D | Swap containerDisk for DataVolume + PVC so disks persist. |
| M2 console | Lifecycle + serial console via virt-api. |
| M4 | Snapshots, clone (snapshot+restore), PVC hot-plug disks. |
| M5 | Resize, guest-agent exec, bulk. |
| M6 | Multi-namespace tenancy (optional). |
| M7 | Ansible inventory from VMIs, file transfer via guest agent. |
| M8 | Cluster node metrics, optional Kubernetes Events mirror. |
| M9 | CAPK workload clusters. |

## Tech stack

- **Backend:** Go 1.26, `k8s.io/client-go`, single binary with embedded frontend via `go:embed`.
- **Driver:** `pkg/kubevirt` — interface-first, stubbed today; typed KubeVirt client lands in M1.
- **Frontend (pending port):** Vue 3 + Vite + Tailwind + Pinia, xterm.js, CodeMirror 6.

## License

MIT (inherited from PassGo Web).
