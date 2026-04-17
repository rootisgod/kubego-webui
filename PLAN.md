# KubeGo — Technical Rewrite Plan (PassGo Web → KubeVirt orchestrator UI)

## Context

PassGo Web is a mature single-binary Go + Vue app that wraps the `multipass` CLI
to give a Proxmox-flavoured UI to a single developer host. The rewrite (KubeGo)
keeps the UX but retargets **KubeVirt on Kubernetes**, so the same UI can drive
multi-tenant VM orchestration and, optionally, provision nested Kubernetes
clusters via Cluster API KubeVirt (CAPK).

The economic driver is: KubeVirt lets commodity k8s clusters carry VMs
alongside containers, and a polished UI closes the usability gap against
Proxmox/vSphere for homelab and SMB buyers. CAPK layered on top turns the same
cluster into a "clusters-as-a-service" platform without paying for an
OpenShift Virt or Harvester seat.

The rewrite is **interface-shape-preserving**: PassGo's `pkg/multipass`
package already looks like a VM driver (Client + ~30 methods). We replace it
with `pkg/kubevirt` behind the same signatures, swap the CLI-shaped types for
CRD-shaped types where they leak, and most HTTP handlers and the whole
frontend port without meaningful change. The scope of rewrite is: the driver
package, a dozen handlers where semantics genuinely shift (suspend, clone,
mounts, transfer, groups, host resources, Ansible inventory), a new Cluster
view, and packaging (Helm chart + in-cluster deployment).

---

## 0. Things I think are wrong in the brief — read this first

I want to flag four places where the assumed mapping is too optimistic, before
the plan treats them as settled:

1. **Suspend has no real equivalent in KubeVirt.** `runStrategy` gives you
   `Always | Halted | Manual | RerunOnFailure`. None of these preserve guest
   RAM. `virtctl pause` freezes the QEMU process but does not survive
   virt-launcher pod restart, so it is not a durable suspend. Proposal: drop
   the suspend verb in the UI (rename to "Stop"), or keep the button and make
   it a confirm-dialog-wrapped stop. This **is a breaking change** to the REST
   API (`POST /vms/{name}/suspend`). Cheapest option: keep the route, make it
   an alias for stop, document it.

2. **`VirtualMachineClone` is alpha and feature-gated.** Do not build Clone on
   it. Build Clone on `VirtualMachineSnapshot` → `VirtualMachineRestore` into
   a new VM name. Requires CSI driver with `VolumeSnapshot` support (rules out
   `hostpath`, fine on rook-ceph / longhorn / CSI-backed managed k8s).

3. **`VirtualMachineInstancetype` / `VirtualMachinePreference` are still
   stabilising.** They are a neat fit for Launch Profiles but I would not hard
   depend on them in v1. Store profiles as normal app-level config (as
   PassGo does today) and *optionally* materialise them as instancetype CRs
   later. Same UX, less coupling to a moving API.

4. **Multipass host-mounts do not translate.** `multipass mount $HOME/src vm:/src`
   bind-mounts a host path into a guest. KubeVirt has no host bind-mount
   primitive — it has PVC hotplug (a *disk*, not a directory). These are
   different features. We should rename the UI from "Mounts" to "Disks" and
   drop the "open in Finder" action. This **is a breaking change** to
   `/api/v1/vms/{name}/mounts` semantics.

Everything else in the mapping you sent is broadly right.

---

## 1. Architecture

### Deployment model

Run KubeGo **as a pod inside the target cluster** by default, with an
alternative external-kubeconfig mode for dev. Preserve the single-binary /
embedded-frontend shape — it is one of PassGo's best properties — and ship a
Helm chart that is a thin wrapper over a Deployment + Service + SA + Role +
optional Ingress. The binary still runs standalone against `~/.kube/config`
for development.

```
                 ┌────────────────────────────────────────────────────────┐
                 │                 Kubernetes (host cluster)              │
                 │                                                        │
  browser ──TLS──▶ Ingress ─▶ Service ─▶ kubego Pod (Deployment, 1–N)     │
                 │                ├── Go HTTP server (net/http)           │
                 │                ├── embedded Vue SPA (go:embed)         │
                 │                ├── pkg/kubevirt (client-go +           │
                 │                │     kubevirt.io/client-go, in-cluster │
                 │                │     rest.Config)                      │
                 │                ├── WebSocket proxy (browser ⇄ virt-api │
                 │                │     /console, /vnc, /portforward)     │
                 │                ├── event log (JSONL PVC or             │
                 │                │     Kubernetes Events, open q.)       │
                 │                ├── scheduler (in-proc, as today)       │
                 │                └── webhook dispatcher                  │
                 │                                                        │
                 │         ServiceAccount: kubego                         │
                 │         RBAC: configurable scope (cluster-wide OR      │
                 │              namespaced — see open questions)         │
                 │                                                        │
                 │ ┌──────────────────┐   ┌──────────────────────────┐   │
                 │ │ kube-apiserver   │   │ virt-api (kubevirt)      │   │
                 │ │ + CRDs           │◀──▶ /console /vnc /portforward│   │
                 │ │ (VM, VMI, VMSnap │   └──────────────────────────┘   │
                 │ │  DataVolume, …)  │                                  │
                 │ └──────────────────┘                                  │
                 │                                                       │
                 │         Prometheus (kubevirt-prometheus-metrics)       │
                 │         CDI (DataVolume import)                        │
                 │         CAPK + Cluster API (workload clusters) ◀── v4 │
                 └────────────────────────────────────────────────────────┘
```

### How the Go backend talks to the cluster

- `k8s.io/client-go` for core + dynamic client.
- `kubevirt.io/client-go` for VirtualMachine/VMI/Snapshot/Restore/DataVolume CRs
  and for the virt-api subresource clients (`VirtualMachineInstance().SerialConsole()`,
  `.VNC()`, `.PortForward()`).
- `rest.InClusterConfig()` first; fall back to `clientcmd.BuildConfigFromFlags`
  when `KUBECONFIG` or `--kubeconfig` is set.
- Informers + listers for VM/VMI/Snapshot/PVC to avoid hammering the API
  server on every `ListVMs()`. This replaces PassGo's 3–5s polling loops on
  the client side — keep the polling in Vue (no behavioural change there),
  but answer each poll from a local cache.
- No custom CRDs in v1. All app state (groups, profiles, schedules, cloud-init
  templates, tokens, webhooks, events) lives in a single-replica PVC
  (preserving PassGo's config/JSON file model). v2 can migrate selected state
  to CRDs (ConfigMap-backed per-VM annotations, or a `KubegoProfile` CRD).

### Authentication boundary

KubeGo keeps its **own** session + bearer-token auth at the HTTP edge, the
same as PassGo. It does **not** delegate user identity to Kubernetes in v1.
Every request is executed by the pod's ServiceAccount. This is the cheap,
pragmatic choice and it matches how Harvester and Kubevirt-Manager started.

Mapping end-users to k8s identities (OIDC passthrough, impersonation headers,
per-namespace RBAC by user) is called out in open questions.

---

## 2. Package-by-package port

Verdicts: **keep** (no change), **refactor** (small edits), **replace**
(rewritten against KubeVirt), **delete**, **new**.

### cmd/server
- `main.go` — **refactor**. Add kubeconfig / in-cluster config wiring, remove
  Multipass install check, replace with a startup probe that verifies the
  KubeVirt CRD group is served (`kubevirt.io/v1`). Same embedded FS wiring.
- `cmd/server/cloud-init/*.yml` — **keep** as seed templates.

### internal/
- `internal/config` — **keep**. App config (groups, profiles, schedules,
  tokens, webhooks, cloud-init templates, defaults) is unchanged in shape.
  Storage still a PVC-mounted directory.
- `internal/api` — **mostly keep**. Handler files change surprisingly little
  because the driver signatures are preserved. See section 3 for the per-endpoint
  breakdown. Specific files:
  - `handlers_vms.go` — **refactor** (suspend semantics, resize semantics —
    memory resize on KubeVirt needs live-migration support or a stop/start).
  - `handlers_shell.go`, `pty_store.go`, `pty_store_unix.go`,
    `pty_store_windows.go`, `proc_unix.go`, `proc_windows.go` — **replace**
    with a much thinner WebSocket proxy. The PTY-on-the-server model is the
    wrong abstraction; virt-api already exposes a console WebSocket. Keep the
    64KB ring buffer + session registry + resize protocol, but the producer
    becomes `kubevirtClient.VirtualMachineInstance(ns).SerialConsole(name,
    &SerialConsoleOptions{})` which returns a `StreamInterface`. Drop all
    `creack/pty` and `conpty` code. Frontend xterm.js unchanged.
  - `handlers_snapshots.go` — **refactor**. Replace calls through to
    `VirtualMachineSnapshot` / `VirtualMachineRestore` CR CRUD.
  - `handlers_mounts.go` — **replace semantics**. Becomes "disks" / hotplug
    PVCs. See section 0 point 4.
  - `handlers_transfers.go` — **replace**. `multipass transfer` has no direct
    KubeVirt equivalent; implement via `virtctl scp`-style SSH through the
    guest (requires guest agent + SSH) or by mounting a shared PVC and a
    short-lived importer job. Recommend v1: SSH through guest agent for
    small files, document the PVC route for large ones. This is the ugliest
    port.
  - `handlers_cloudinit.go` — **keep** shape. Templates still stored as app
    state; on VM create they are written to a Secret and referenced via
    `cloudInitNoCloud.userDataSecretRef` (not inline, to dodge 1 MiB Secret
    limits cleanly and to allow shared templates across many VMs).
  - `handlers_ansible.go`, `handlers_ansible_run.go` — **refactor**.
    Inventory now generated from VMI list (+ `status.interfaces[].ipAddress`
    or from a guest-agent probe). Needs SSH reachability; call out CNI
    constraints in docs.
  - `handlers_groups.go` — **refactor** heavily (see open q. on namespace vs
    label).
  - `handlers_profiles.go` — **keep**. App-level profiles only in v1; no CRD
    coupling.
  - `handlers_schedule.go`, `scheduler.go` — **keep**. In-proc scheduler is
    still the right call; CronJob is the wrong primitive here (it would need
    to call *back* into our own API). Only the op targets change.
  - `handlers_webhooks.go`, `webhooks.go` — **keep**.
  - `eventlog.go` — **keep** for v1. Open question whether to *also* surface
    Kubernetes Events (`kubectl events`) so operators on kubectl see our
    activity. Low-effort addition in v2.
  - `middleware.go`, `handlers_tokens.go` — **keep**. Same session + SHA256
    bearer token model. Now additionally check a `readOnly` flag per token
    (new, see open questions on RBAC).
  - `handlers_chat.go`, `llm_agent.go`, `llm_tools.go` — **refactor**. All 30
    LLM tools need rewriting to hit `pkg/kubevirt` instead of
    `pkg/multipass`, but the signatures stay. A few tools are additive
    (namespace ops, cluster ops in v4).

### pkg/multipass → pkg/kubevirt (**replace wholesale**)

This is the core of the rewrite. Preserve the signature set so that
`internal/api` barely notices. Sketched interface (no code, just shape):

```
type Client interface {
    // lifecycle
    ListVMs(ctx, scope) ([]VMInfo, error)
    GetVMInfo(ctx, ref) (VMInfo, error)
    LaunchVM(ctx, spec LaunchSpec) error         // creates VM + optional DV + cloud-init Secret
    CloneVM(ctx, src, dst VMRef) error           // snapshot+restore under the hood, not VMClone CRD
    StartVM(ctx, ref) error                      // runStrategy=Always
    StopVM(ctx, ref) error                       // runStrategy=Halted
    SuspendVM(ctx, ref) error                    // alias for StopVM; or return 501 — decision needed
    DeleteVM(ctx, ref, purge bool) error         // Delete VM; purge=true also deletes PVCs
    RecoverVM(ctx, ref) error                    // only meaningful if using soft-delete; likely NOP
    SetVMCPUs(ctx, ref, cpus int) error          // requires Halted or live-migration
    SetVMMemory(ctx, ref, MB int) error
    SetVMDisk(ctx, ref, GB int) error            // PVC resize if storage class supports it
    ExecInVM(ctx, ref, cmd string) (string, error) // qemu-guest-agent exec

    // snapshots
    ListSnapshots(ctx, ref) (...)
    CreateSnapshot / RestoreSnapshot / DeleteSnapshot

    // disks (née mounts)
    ListDisks(ctx, ref) (...)
    AttachDisk / DetachDisk   // hot-plug PVCs

    // discovery
    ListNetworks(ctx) (...)   // NetworkAttachmentDefinitions (Multus) + pod network
    FindImages(ctx) (...)     // DataVolume source catalogue (app config)

    // subresources (new)
    SerialConsole(ctx, ref) (io.ReadWriteCloser, error)
    VNC(ctx, ref) (io.ReadWriteCloser, error)
    PortForward(ctx, ref, port int) (io.ReadWriteCloser, error)

    // host
    ClusterResources(ctx) (...) // replaces per-host resource query
}

type VMRef struct { Namespace, Name string }  // was just string in multipass
```

The single biggest signature change is that every identifier becomes a
`{namespace, name}` tuple. This ripples into every handler URL (`/vms/{name}`
→ `/vms/{ns}/{name}`, or keep `/vms/{name}` with a namespace header /
query) — open question, see §9.

`pkg/kubevirt` internal files:
- `client.go` — rest.Config wiring, informer factories.
- `vms.go`, `snapshots.go`, `disks.go`, `networks.go`, `images.go`,
  `cluster.go` (host resources), `subresources.go` (console/VNC/portforward),
  `exec_guestagent.go`.
- `types.go` — shared VMInfo, SnapshotInfo, DiskInfo, NetworkInfo shaped to
  look like PassGo's, but populated from CR status.
- No `host_darwin.go` / `host_linux.go` / `host_windows.go` — host detection
  becomes node detection; one file.

### frontend/
- Small diff. Details in §4.

### Delete outright
- `pkg/multipass/host_{darwin,linux,windows}.go` — host detection irrelevant.
- `pkg/multipass/ssh_key.go` — KubeVirt injects SSH keys via cloud-init, not
  via multipass-specific plumbing.
- All Windows-ConPTY code in internal/api — PTY is gone.
- Multipass install / CLI-presence checks in main.

---

## 3. API surface — per-endpoint verdict

Columns: endpoint · verdict · notes. "Breaking" = frontend + any external
integrations must change.

| Endpoint | Verdict | Notes |
|---|---|---|
| `POST /api/v1/auth/login`, `logout` | keep | |
| `GET/POST /api/v1/vms` | change (breaking, namespace in path or query) | see §9 |
| `GET /api/v1/vms/{ns}/{name}` | change (was `/vms/{name}`) | breaking |
| `POST /api/v1/vms/{ns}/{name}/start` | keep shape | runStrategy=Always |
| `.../stop` | keep shape | runStrategy=Halted |
| `.../suspend` | **remove or alias** | see §0 point 1; breaking if removed |
| `.../clone` | keep shape | snapshot+restore impl |
| `.../delete` | keep shape, new `purge` semantics | purge deletes PVCs |
| `.../recover` | **remove** | not meaningful; breaking |
| `.../resize` | keep shape | memory live-resize gated on live-migration support |
| `.../exec` | keep shape | via qemu-guest-agent |
| `.../cloudinit-status` | keep | read VMI annotation or guest-agent |
| `.../config` | keep | |
| `POST /api/v1/vms/start-all`, `/stop-all` | keep | scoped to current namespace |
| `POST/GET/DELETE /api/v1/vms/{ns}/{name}/shell[...]` | keep shape | backend now proxies virt-api SerialConsole |
| `WS /api/v1/vms/{ns}/{name}/shell/{sessionId}` | keep shape | producer changes, wire protocol and resize byte unchanged |
| `POST /api/v1/vms/{ns}/{name}/files/upload` / `download` / `list` / `mkdir` | change | via guest-agent or a PVC-mount sidecar; feature-flag in v1 |
| `GET/POST/DELETE .../snapshots[...]` | keep shape | |
| `GET/POST/DELETE .../mounts[...]` | **rename to /disks**, breaking | PVC hotplug semantics |
| `.../mounts/open` | **remove** | host-side action, meaningless in-cluster |
| `GET/POST/PUT/DELETE /api/v1/cloudinit/{name}` | keep | backed by Secret + app state |
| `GET /api/v1/ansible/status` | keep | |
| `GET /api/v1/ansible/inventory` | keep shape | generated from VMIs, SSH reachability caveats |
| `POST /api/v1/ansible/run` and friends | keep | |
| `GET/POST/PUT/DELETE /api/v1/playbooks[...]` | keep | |
| `GET/POST/PUT/DELETE /api/v1/groups[...]` | keep shape | semantics: namespace+label, breaking if client assumes flat list |
| `GET/POST/PUT/DELETE /api/v1/profiles[...]` | keep | app-level in v1 |
| `GET/POST/PUT/DELETE /api/v1/schedules[...]` | keep | |
| `GET /api/v1/networks` | keep shape | lists NADs (Multus) + "pod" |
| `GET /api/v1/images` | keep | now a DataVolume source catalogue |
| `GET /api/v1/host/resources` | **rename to /cluster/resources** | aggregated node metrics |
| `GET /api/v1/defaults`, `PUT` | keep | |
| `GET /api/v1/version` | keep | |
| `GET/POST/DELETE /api/v1/tokens[...]` | keep | |
| `GET/POST/PUT/DELETE /api/v1/webhooks[...]` | keep | |
| `POST /api/v1/webhooks/{id}/test` | keep | |
| `GET /api/v1/events` | keep | |
| `GET/PUT /api/v1/chat/config`, `models`, `POST /chat` | keep | |

**New endpoints:**
| Endpoint | Notes |
|---|---|
| `GET /api/v1/namespaces` | list namespaces accessible to SA |
| `POST /api/v1/namespaces` / `DELETE` | only if multi-namespace mode enabled; dangerous — confirm-gate |
| `GET /api/v1/nodes` | cluster node list + per-node metrics |
| `GET /api/v1/cluster/resources` | renamed from `/host/resources` |
| `GET /api/v1/instancetypes` (optional, v2) | enumerate VirtualMachineInstancetype if feature enabled |
| `GET /api/v1/storageclasses` | at VM create time — pick PVC backend |
| `GET/POST/DELETE /api/v1/clusters` (v4, CAPK) | list/create/destroy workload clusters |
| `GET /api/v1/clusters/{name}/kubeconfig` (v4) | download tenant kubeconfig |

**Breaking-change cost summary:** if you preserve `{name}` in paths and add
a `ns` query param with a default, most external API consumers survive. The
clean option (path-based `{ns}/{name}`) costs one `v2/` prefix and an
explicit deprecation of `v1/vms/{name}`. Recommend the clean option —
introducing `v2/`. Call this out for user decision in §9.

---

## 4. Frontend changes

Tight summary — most components are untouched because the state shape on the
wire is preserved.

**Untouched:**
- `xterm.js` terminal component (`ConsoleTerminal.vue`). Resize protocol byte
  `0x01 + u16 BE width + u16 BE height` is unchanged; WebSocket endpoint
  changes but the component doesn't care.
- CodeMirror 6 cloud-init editor.
- Ansible runner UI (SSE output).
- LLM chat UI.
- Event log viewer, webhook/token/profile/schedule settings panes.
- Toast/modal/shared widgets. Pinia stores shape.
- Auth pages, CSP/CORS headers.

**Changed:**
- `TreeSidebar.vue` — structure is now **Namespace → Group (label) → VM**
  (was Group → VM). If the v1 tenancy decision is "single namespace", the
  top level collapses and we match PassGo 1:1. Otherwise a new top-level
  group node is added and the store gains a `selectedNamespace`.
- `VmDetailPanel.vue` tabs: **rename Mounts → Disks**. "Open mount in Finder"
  action removed from the UI. Transfer tab gets a disabled-by-default
  warning about reachability.
- `VmConfigTab.vue` — resize UI warns when memory/CPU change requires
  stop/start (no live-migration) or a live-migration (if enabled).
- `VmSummaryTab.vue` — suspend button either removed or relabeled to Stop.
- New **ClusterView.vue** (v4, CAPK). Separate top-level tree node
  (`__clusters`) with list/create/delete and a kubeconfig download button.
- New **NodesView.vue** or merged into existing `HostResources.vue` — cluster
  node cards (was single-host resource card).
- `VmStore.js` (Pinia) — every VM gets a `namespace` field. List filtering
  by namespace is a new store getter.
- API client (`frontend/src/api/`) — regenerate if OpenAPI-driven; otherwise
  search/replace paths.

Estimated frontend LOC churn: <10% of the current frontend. The tree sidebar
and VmConfigTab take most of the edit.

---

## 5. KubeVirt-specific gotchas (what WILL bite us)

Briefly, with KubeVirt primer where useful:

- **/dev/kvm and nested virt.** KubeVirt runs qemu/KVM inside virt-launcher
  pods. The node must expose /dev/kvm. On GKE/AKS/EKS you need nested-virt
  capable instance types or bare-metal nodes. Software emulation (no KVM) is
  10–100× slower and should never be the default. Detect missing KVM at boot
  and refuse to create VMs with a clear error — don't silently emulate.

- **containerDisk vs DataVolume vs PVC.** *containerDisk* = OCI-image-backed
  read-only root disk, great for ephemeral stateless VMs, CANNOT persist
  changes. *DataVolume* = CDI-managed PVC with an importer (from HTTP/registry/
  PVC clone/blank). *PVC* = raw. v1 should default to **DataVolume from
  registry** for root disks; accept a BYO-PVC option for power users. Never
  default to containerDisk for a user-facing "launch a VM" flow — users will
  lose data and file a bug.

- **runStrategy semantics matter for status display.** A VM with
  `runStrategy=Halted` exists (spec visible) but has no VMI (no IP, no
  console). The UI must show both the *VM* (exists, stopped) and the *VMI*
  (may not exist) and reconcile: PassGo's single "VMInfo" conflates these
  and we must keep doing so, preferring VMI fields when VMI exists and
  falling back to VM fields otherwise.

- **Console subresource is virt-api, not kube-apiserver.** The call goes
  through kube-apiserver but is dispatched to virt-api over APIService
  aggregation. Tokens flow via the SA. Proxy it through our backend — do not
  expose virt-api to the browser. Re-use PassGo's ring buffer + reconnect
  design; the producer changes, the consumer doesn't.

- **CSI driver choice is load-bearing.** Snapshots, clones, resize, and
  RWX all depend on CSI capability. rook-ceph and longhorn give you
  everything; hostpath gives you nothing; managed-k8s defaults (pd.csi.gke,
  ebs.csi, disk.csi.azure) give you snapshot+resize but not RWX. Expose the
  chosen StorageClass in the Launch dialog and warn when snapshots/clones
  are unavailable.

- **Cloud-init is NoCloud-only.** No metadata server semantics, no other
  datasources. Put templates in a Secret with key `userdata` and reference
  via `userDataSecretRef`. Mind the 1 MiB Secret limit — split networkData
  into a separate secret only if needed.

- **`VirtualMachineClone` CRD is alpha** — already covered, don't use.

- **Memory/CPU hotplug requires live migration**, which requires shared
  storage and `evictionStrategy: LiveMigrate`. On hostpath-only clusters,
  resize = stop + edit + start.

- **Guest agent reliance.** ExecInVM and accurate IP reporting depend on
  qemu-guest-agent running in the guest. Bake it into every cloud-init
  template we ship. Surface "guest agent not responding" as a first-class
  status state.

- **Namespace-scoped CRDs.** VirtualMachine, VMI, Snapshot, Restore,
  DataVolume are all namespace-scoped. Listing "all VMs across all
  namespaces" requires cluster-wide list RBAC (`virtualmachines` at cluster
  scope). This interacts with §9 open question on tenancy.

- **virtctl version skew.** If we ever shell out to virtctl, pin it. Better:
  don't — use the Go subresource client.

- **Feature gates.** Hotplug volumes, instancetypes, VMClone, and others are
  feature-gated on older KubeVirt versions. At startup query the KubeVirt CR
  (`kubevirt.kubevirt.io`) `status.observedKubeVirtVersion` and the list of
  enabled featureGates, and disable UI affordances we cannot deliver.
  Don't let the user click a button that will 404 on the API.

---

## 6. Phased delivery plan

Each milestone is independently demoable and independently useful. "Cut from
v1" calls out the soft features.

**M0 — Skeleton (1–2 weeks).**
- Repo fork + rename. Delete multipass-specific packages. Stub `pkg/kubevirt`.
- In-cluster config + kubeconfig fallback. Startup probe for KubeVirt CRDs.
- Helm chart shell (Deployment, Service, SA, Role, Ingress optional).
- Frontend login + empty tree sidebar.
- Demo: pod starts in cluster, UI loads, empty state.

**M1 — Read-only VM list (1 week).**
- `ListVMs`, `GetVMInfo` via informers.
- Tree sidebar populated. VmSummaryTab shows real data.
- Single-namespace mode only.
- Demo: UI shows the VMs already in a test namespace.

**M2 — Lifecycle + console (2 weeks).**
- Start/Stop/Delete via runStrategy + CR mutation.
- Console: backend WebSocket proxy to virt-api `SerialConsole`. Keep ring
  buffer, session store, resize protocol. Frontend xterm.js unchanged.
- Demo: start/stop a VM, open terminal, reconnect after refresh.

**M3 — Create VMs (2 weeks).**
- Launch flow: DataVolume from registry, cloud-init template → Secret →
  `userDataSecretRef`, VM CR creation.
- Image catalogue (`FindImages`) backed by app config (curated list of
  public container-disk / qcow2 URLs).
- Cloud-init editor reused unchanged.
- Demo: create Ubuntu VM from template, cloud-init runs, SSH in via console.

**M4 — Snapshots + "clone" + disks (2 weeks).**
- `VirtualMachineSnapshot` / `Restore` CRUD.
- Clone = snapshot + restore to new VM name (document the CSI requirement).
- Hotplug PVC attach/detach ("Disks" tab, renamed from Mounts).
- Demo: snapshot, break the VM, restore; clone for a fresh test env.

**M5 — Resize, exec, guest-agent IP, bulk (1–2 weeks).**
- CPU/mem/disk resize with the "requires stop" vs "live" UX.
- Exec via qemu-guest-agent.
- Bulk start/stop. Scheduler reused.

**M6 — Multi-namespace tenancy (2 weeks, optional).**
- Namespace picker in UI, `{ns, name}` throughout.
- Groups = labels within a namespace (+ optional cross-namespace label).
- Cluster-wide list mode vs namespace-scoped mode toggle.
- **Decision point** — if the answer to §9 Q1 is "single namespace", skip this.

**M7 — Ansible, transfers, LLM (2 weeks).**
- Ansible inventory from VMIs, with a clear SSH-reachability doc.
- File transfer v1: SSH-through-guest-agent for small files.
- LLM tools rewritten to match. Same 30-tool surface.

**M8 — Metrics + events polish (1 week).**
- Cluster node cards (metrics.k8s.io for node-level; Prometheus for per-VM
  if configured; graceful degradation otherwise).
- Optional mirror of our events to Kubernetes Events.

**M9 — CAPK / Clusters (3–4 weeks, v2 milestone).**
- Depends on CAPK + Cluster API + CAPI-control-plane provider installed
  in-cluster.
- New Cluster view. Templates for k3s (simpler) and kubeadm.
- List, create, scale, delete workload clusters.
- Kubeconfig download.
- Networking opinionation: document the VXLAN-port workaround up front;
  pick a default CNI (calico) for tenants.

**Cuttable from v1 (ship later):**
- M6 multi-namespace (start single-namespace).
- M7 file transfer and LLM.
- M8 per-VM metrics (show placeholder until Prometheus is configured).
- M9 CAPK (always v2).
- VNC, USB redirect.
- Scheduled ops (nice-to-have, keep in code, low-priority UX).

---

## 7. Open questions (require your decisions)

1. **Tenancy model.** (a) *Single-namespace*: everyone shares one namespace,
   groups are labels, simple RBAC. (b) *Multi-namespace*: each "group" is a
   namespace, cross-namespace list requires cluster-wide RBAC. (c) *Hybrid*:
   groups are labels, namespace is a separate concept. My recommendation:
   **(c) hybrid**, starting in single-namespace mode, because namespaces
   are the right primitive for RBAC+quota but conflating them with user-
   facing "groups" is a worse UX than PassGo's flat grouping.

2. **Deployment model.** Keep the embedded-binary single-binary ethos (easy
   dev, easy external mode) **and** ship a Helm chart — or go Helm-first and
   drop the standalone-binary mode? Recommendation: **keep both**; embedded
   binary is 5 lines of `go:embed`, and it's a genuine differentiator vs
   KubeVirt-Manager / Kubevirt-UI.

3. **Auth model.** (a) Keep PassGo's session + bearer token; SA does all
   work. (b) OIDC passthrough + impersonation headers (`Impersonate-User`)
   so Kubernetes enforces RBAC per end-user. (c) Both, switchable. (a) is
   cheap and ships, (b) is production-grade, (c) is what this needs
   eventually. Recommendation: **ship (a) in v1, commit to (b) in v2**,
   and don't pretend otherwise.

4. **Storage default.** What StorageClass + CSI profile do we assume for
   "local/dev"? longhorn vs rook-ceph vs host-path. This affects snapshots
   and clones working out of the box. Recommendation: **require a
   StorageClass with VolumeSnapshot capability** and surface a clear error
   if none is present; do not try to paper over hostpath-only clusters.

5. **CNI / network defaults.** Pod network by default, Multus + NAD for
   power users. Do we ship a "bridge" NAD definition in the Helm chart, or
   leave it to the operator? Recommendation: **document only**; shipping a
   NAD in the chart invites surprises.

6. **API versioning.** Break `/api/v1/vms/{name}` to `/api/v2/vms/{ns}/{name}`
   cleanly, or keep v1 path and add `?namespace=` with a default? External
   API users (Ansible modules, scripts) are the constraint. Recommendation:
   **introduce /api/v2/** and freeze /api/v1 at the Multipass-shaped
   semantics (i.e. retired). Costs one stable-deprecation cycle.

7. **Pluggable driver.** Should KubeGo keep Multipass working behind the same
   interface, so the same UI drives both? It sounds elegant but the
   semantic impedance (mounts, groups, suspend, host resources) is large
   enough that you either pick the lowest common denominator (boring UI) or
   you feature-flag everything (messy code). Recommendation: **no** — fork
   KubeGo and let PassGo continue to exist for the single-host case.

8. **Scheduled operations: in-process vs CronJob.** Keep the in-proc
   scheduler (PassGo does) or map to Kubernetes CronJobs? Recommendation:
   **in-proc**, because CronJobs would have to call back into our own HTTP
   API, which is circular and awkward.

9. **Event log backing.** JSONL on a PVC (PassGo) vs Kubernetes Events vs a
   proper store (sqlite on PVC, or an external DB). Recommendation:
   **JSONL on PVC in v1**, mirror to Kubernetes Events in v2.

10. **Do we do CAPK at all, or only VM orchestration?** CAPK is the
    differentiator that makes this interesting. But it is pre-1.0 (v0.11.2
    as of March 2026) and networking is the hard part. Recommendation:
    **yes, but as M9 / v2**, and commit to it in the roadmap so the
    architecture doesn't close the door. No CAPK-specific code in v1.

---

## Critical files to change first

- `cmd/server/main.go` (config wiring + KubeVirt probe)
- `pkg/multipass/*` → `pkg/kubevirt/*` (full replace, signature-preserving)
- `internal/api/handlers_shell.go`, `pty_store*.go`, `proc_*.go`
  (replace PTY with virt-api SerialConsole proxy)
- `internal/api/handlers_vms.go` (suspend, resize, exec semantics)
- `internal/api/handlers_mounts.go` → `handlers_disks.go` (PVC hotplug)
- `internal/api/handlers_transfers.go` (SSH/guest-agent path)
- `internal/api/handlers_snapshots.go` (CR CRUD)
- `internal/api/llm_tools.go` (30 tools, interface-preserving refactor)
- `frontend/src/components/layout/TreeSidebar.vue`
- `frontend/src/stores/vmStore.js`
- `frontend/src/components/vm/VmDetailPanel.vue` (tab rename)
- `charts/kubego/` (new Helm chart)

## Verification plan

Tested end-to-end per milestone. Minimum environment: a local KinD cluster
with KubeVirt and CDI installed (documented as a dev bootstrap script).

- **Unit**: driver tests use a fake client (`client-go/testing/fake` +
  `kubevirt.io/client-go/kubecli/fake`) so the same `testdata` discipline
  PassGo has carries over. Handlers tested with `httptest.NewServer`.
- **Integration**: KinD + KubeVirt + rook-ceph (or longhorn) in CI. One
  golden-path test per milestone: "M3 = launch VM, wait Running, assert
  IP reported".
- **Manual E2E per milestone**: a written checklist (start/stop, console,
  snapshot, clone, resize, exec, bulk, scheduler, webhook delivery).
- **Backward-compat check**: point PassGo's existing OpenAPI consumer tests
  at KubeGo `/api/v2/` with a translation layer, confirm that the breaking
  changes are exactly the ones documented here (suspend removed, mounts
  renamed, recover removed, namespace in path).
- **CAPK (when we get there)**: `clusterctl` a test kubeadm cluster, confirm
  kubeconfig, deploy nginx, curl through a tenant Service.
