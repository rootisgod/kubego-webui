# KubeGo

A browser UI and REST API for [KubeVirt](https://kubevirt.io/) — drives VMs as Kubernetes resources so a single commodity cluster can carry VMs alongside containers. Forked from [PassGo Web](https://github.com/rootisgod/passgo-webui) and progressively retargeted away from Canonical Multipass.

> **Status: M0 skeleton (pre-alpha).** The binary builds, boots against a cluster, and probes for KubeVirt, but every VM operation returns `ErrNotImplemented`. M1+ wires each method against the real KubeVirt API. See [PLAN.md](PLAN.md) for the full milestone breakdown.

## What is it aimed at?

Any Kubernetes cluster that can run KubeVirt — homelab, on-prem, managed (EKS/AKS/GKE with nested-virt nodes), or a dev KinD cluster. There is no KinD-specific code in the binary; KinD is only the dev/test convenience, and the `scripts/` directory codifies that setup. The deployment target is in-cluster as a pod (Helm chart lands in M0 Slice 3); running standalone against `~/.kube/config` is the supported dev mode.

## End-to-end walkthrough

Three stages: build the binary, point it at a cluster, confirm it's wired. You can do all three against an existing cluster if you have one; otherwise set up a local KinD cluster first.

### 1. Prerequisites

- Go 1.26.1
- `kubectl`, `curl`
- Either: an existing Kubernetes cluster you can reach, **or** [`kind`](https://kind.sigs.k8s.io/docs/user/quick-start/) installed
- Optional: [`task`](https://taskfile.dev/) — the Taskfile is a thin shortcut over the same shell commands and scripts below. All Taskfile tasks map to plain `go`/`bash`/`kubectl` invocations, so Task is never required.

### 2a. Build and smoke-test without a cluster

The binary will boot against any kubeconfig that parses; the KubeVirt probe logs a warning when the target is unreachable or KubeVirt is missing, but the process keeps running. This is enough to validate your build:

```bash
# Build
go build -o kubego ./cmd/server
# or: task build-backend

# End-to-end smoke test: builds, boots on :18080 using $KUBECONFIG (or
# ~/.kube/config), hits /version, logs in, confirms VM calls return the
# stub error, then shuts the binary down. Safe to run against any
# kubeconfig — real, fake, or unreachable.
./scripts/smoke-test.sh
# or: task smoke
```

Expected output ends with `Smoke test passed.` and a captured log line resembling:

```
level=WARN msg="kubevirt probe failed" err="...dial tcp ...: connect: connection refused"
# or, against a real cluster without KubeVirt:
level=WARN msg="kubevirt not installed on target cluster — VM operations will return 501 until KubeVirt is installed"
# or, against a cluster that has it:
level=INFO msg="kubevirt detected" group=kubevirt.io version=v1 ...
```

### 2b. Build a dev KinD cluster and install KubeVirt

The included `scripts/kind-up.sh` creates a `kubego-dev` KinD cluster, installs the KubeVirt operator + CR, patches it into software-emulation mode (KinD nodes have no `/dev/kvm`), installs the Containerized Data Importer (CDI), and waits for both to become Available. The script is idempotent — re-running against an existing cluster just re-applies the manifests.

```bash
./scripts/kind-up.sh
# or: task kind:up
```

First run takes 5–10 minutes (KubeVirt virt-operator, virt-api, virt-controller, virt-handler DaemonSet, CDI operator, CDI controllers all need to come up). Subsequent runs are seconds.

Override the defaults if you want:

```bash
KUBEGO_KIND_CLUSTER=my-cluster \
KUBEVIRT_VERSION=v1.2.0 \
CDI_VERSION=v1.59.0 \
./scripts/kind-up.sh
```

Tear it down with `./scripts/kind-down.sh` (or `task kind:down`).

> Software emulation is 10–100× slower than KVM. Fine for wiring and integration tests; **do not** run real workloads on this. See PLAN.md §5 for the gory details.

### 3. Run the binary against the cluster

```bash
./kubego --kubeconfig "$HOME/.kube/config" --port 8080
# or: task run
```

Visit `http://localhost:8080`. The embedded SPA is the placeholder until M0 Slice 3 re-wires it — useful endpoints right now are served via `curl`:

```bash
# Public
curl -s localhost:8080/api/v1/version

# Auth then authenticated requests
curl -sc /tmp/kubego.jar -X POST localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}'

# VM calls return the M0 stub error (HTTP 500 with ErrNotImplemented)
curl -sb /tmp/kubego.jar localhost:8080/api/v1/vms
```

Default login is `admin` / `admin`; a config file is created at `~/.passgo-web/config.json` on first run. (The path still carries the upstream name; rename lands in a later milestone.)

### 4. Tear down

```bash
# Stop the binary: Ctrl-C, or if you backgrounded it, kill its PID.
./scripts/kind-down.sh
# or: task kind:down
```

## What works today (M0)

- `go build ./...` and `go vet ./...` clean on Go 1.26.1.
- Kubernetes config discovery: in-cluster first, then `--kubeconfig` / `$KUBECONFIG` / `~/.kube/config`.
- Startup discovery probe for `kubevirt.io/v1`; logs, not crashes, when absent.
- Session + bearer-token auth, rate limiting, event log, config load/save, REST endpoints for every resource the driver exposes.
- Filesystem-backed helpers for cloud-init templates and Ansible playbooks survive the rewrite.

## What does not work yet

| Area | Status | Milestone |
|------|--------|-----------|
| VM list / info | Stubbed (`ErrNotImplemented`) | M1 |
| VM lifecycle (start/stop/delete) | Stubbed | M2 |
| Serial console | Routes removed | M2 (virt-api `SerialConsole` proxy) |
| VM creation | Stubbed | M3 |
| Snapshots / clone / disks | Stubbed | M4 |
| Resize / exec / bulk | Stubbed | M5 |
| Multi-namespace tenancy | Single-namespace only | M6 |
| Ansible inventory, file transfer | Stubbed | M7 |
| Metrics / Kubernetes Events | Stubbed | M8 |
| CAPK (workload clusters) | Not in scope | M9 |
| Helm chart | Missing | M0 Slice 3 |
| Frontend (Vue app) | Still calls PassGo routes | M0 Slice 3 |

The Vue app in `frontend/` builds but renders broken against the current backend — it asks for removed routes (`/mounts`, `/host/resources`, `/recover`). A Slice 3 pass removes those call sites and renames Mounts → Disks.

## Development

### Useful Taskfile targets

```
task build-backend       Build kubego without rebuilding the frontend
task run                 Build + run against ~/.kube/config
task dev-backend         go run ./cmd/server (no binary produced)
task smoke               End-to-end smoke test
task kind:up             Create kubego-dev KinD cluster + KubeVirt + CDI
task kind:down           Delete kubego-dev KinD cluster
task kind:reset          Delete and recreate
task test                go test ./... -v
task clean               Remove all build artifacts
```

All Taskfile entries are shell commands; `cat Taskfile.yml` if you want the plain invocation.

### Layout

```
cmd/server/              Go entrypoint, embeds frontend/dist and cloud-init templates
internal/api/            HTTP handlers and middleware
internal/config/         App config load/save, password hashing
pkg/kubevirt/            Driver interface (all VM operations live behind here)
  client.go              NewClient, rest.Config wiring
  unimplemented.go       M0 stub — returns ErrNotImplemented
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

## Roadmap

| Milestone | Scope |
|-----------|-------|
| M0 (current) | Driver interface + cluster wiring + CRD probe. Helm + frontend still to go (Slice 3). |
| M1 | Read-only VM list via informers. |
| M2 | Lifecycle + serial console via virt-api. |
| M3 | VM creation with DataVolume + cloud-init Secret. |
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
