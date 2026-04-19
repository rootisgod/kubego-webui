import { defineStore } from 'pinia'
import { listVMs, listLaunches, listGroups, listProfiles, dismissLaunch, getClusterResources, getClusterInfo, listClusters } from '../api/client.js'
import { recordMetrics } from '../composables/useMetricsHistory.js'

export const useVmStore = defineStore('vms', {
  state: () => ({
    authenticated: false,
    vms: [],
    launches: [],  // in-progress or recently-failed launches
    selectedNode: null,  // null = host, string = VM name
    selectedVms: [],     // multi-select: array of VM names
    lastRefresh: null,
    loading: false,
    error: null,
    hostname: 'cluster',
    clusterResources: null,  // { total_cpus, load_avg_1, ..., total_memory_mb, used_memory_mb, total_disk_mb, used_disk_mb }
    clusterInfo: null,  // { name, context, flavor, api_server, kubernetes_version, nodes[], kubevirt{}, cdi{}, virtualisation{} }
    // Groups
    groups: [],           // ordered list of group names
    vmGroups: {},         // {vmName: groupName}
    expandedGroups: {},   // {groupName: bool} local UI state
    // Profiles
    profiles: [],
    // Clusters (kubeconfig contexts)
    clusters: [],          // [{ context, current, server, is_kind, in_cluster }]
    activeContext: '',     // name of the active context
    inClusterMode: false,  // true when server is running in-cluster (disables kind ops)
    kindAvailable: false,  // true when `kind` is on PATH on the server
    // OS filter for the sidebar list. "all" | "linux" | "windows".
    // Empty/missing OS is treated as Linux — older VMs predate the label.
    osFilter: 'all',
  }),

  getters: {
    selectedVm: (state) => state.vms.find(vm => vm.name === state.selectedNode),
    runningCount: (state) => state.vms.filter(vm => vm.state === 'Running').length,
    stoppedCount: (state) => state.vms.filter(vm => vm.state === 'Stopped').length,
    totalCount: (state) => state.vms.length,
    // Only show launches for VMs not yet in the real VM list
    activeLaunches: (state) => {
      const vmNames = new Set(state.vms.map(vm => vm.name))
      return state.launches.filter(l => l.status === 'launching' && !vmNames.has(l.name))
    },
    launchingCount() { return this.activeLaunches.length },
    failedLaunches: (state) => state.launches.filter(l => l.status === 'failed'),
    selectedVmObjects: (state) => state.vms.filter(vm => state.selectedVms.includes(vm.name)),
    // visibleVms applies the OS filter. The list, group expansion, and
    // bulk "select all" all read through this so the UI stays consistent.
    // Selection state itself is kept against the full vms list — toggling
    // the filter doesn't drop already-selected VMs from the selection.
    visibleVms(state) {
      if (state.osFilter === 'all') return state.vms
      return state.vms.filter(vm => {
        const os = (vm.os || 'linux').toLowerCase()
        return state.osFilter === 'windows' ? os === 'windows' : os !== 'windows'
      })
    },
    ungroupedVms() {
      return this.visibleVms.filter(vm => !this.vmGroups[vm.name])
    },
    activeCluster: (state) => state.clusters.find(c => c.context === state.activeContext) || null,
    activeClusterColor() { return this.activeCluster?.color || '' },
    activeClusterTag() { return this.activeCluster?.tag || '' },
  },

  actions: {
    groupedVms(groupName) {
      return this.visibleVms.filter(vm => this.vmGroups[vm.name] === groupName)
    },

    setOsFilter(value) {
      this.osFilter = value === 'linux' || value === 'windows' ? value : 'all'
    },

    groupSummary(groupName) {
      const vms = this.groupedVms(groupName)
      return {
        running: vms.filter(v => v.state === 'Running').length,
        stopped: vms.filter(v => v.state === 'Stopped').length,
        total: vms.length,
      }
    },

    async fetchVMs() {
      try {
        this.loading = true
        this.error = null
        // A broken or empty active cluster makes listVMs 500. Catch it
        // here instead of rejecting the whole Promise.all, so fetchGroups/
        // fetchProfiles/fetchClusters below still run and the cluster
        // switcher stays usable.
        const [data, launchData, clusterData, clusterMeta] = await Promise.all([
          listVMs().catch(e => { this.error = e.message; return [] }),
          listLaunches().catch(() => []),
          getClusterResources().catch(() => null),
          getClusterInfo().catch(() => null),
        ])
        if (clusterMeta) {
          this.clusterInfo = clusterMeta
          this.hostname = clusterMeta.name || 'cluster'
        }
        const launches = Array.isArray(launchData) ? launchData : []
        const launchingNames = new Set(launches.filter(l => l.status === 'launching').map(l => l.name))
        const vms = Array.isArray(data) ? data : []
        // Tag VMs that are still being created with a "Creating" state
        for (const vm of vms) {
          if (launchingNames.has(vm.name) && (!vm.state || vm.state === 'Unknown')) {
            vm.state = 'Creating'
          }
        }
        this.vms = vms
        this.launches = launches
        this.lastRefresh = new Date()

        // Record metrics for running VMs
        for (const vm of vms) {
          if (vm.state === 'Running' && vm.load) {
            const loadParts = vm.load.split(' ').map(Number)
            const cpuLoad = loadParts.length >= 1 ? loadParts[0] : 0
            const memPct = vm.memory_total_raw ? (vm.memory_usage_raw / vm.memory_total_raw) * 100 : 0
            const diskPct = vm.disk_total_raw ? (vm.disk_usage_raw / vm.disk_total_raw) * 100 : 0
            recordMetrics(vm.name, { cpu: cpuLoad, memory: memPct, disk: diskPct })
          }
        }

        // Refresh groups, profiles, and clusters alongside VMs
        await Promise.all([this.fetchGroups(), this.fetchProfiles(), this.fetchClusters()])

        // Record cluster resource metrics
        if (clusterData) {
          this.clusterResources = clusterData
          const memPct = clusterData.total_memory_mb ? (clusterData.used_memory_mb / clusterData.total_memory_mb) * 100 : 0
          const diskPct = clusterData.total_disk_mb ? (clusterData.used_disk_mb / clusterData.total_disk_mb) * 100 : 0
          recordMetrics('__cluster__', { cpu: clusterData.load_avg_1 || 0, memory: memPct, disk: diskPct })
        }
      } catch (err) {
        // 401 is handled centrally via the 'passgo:unauthorized' event in App.vue.
        // Treat it as a normal error here — the listener flips authenticated.
        this.error = err.message
      } finally {
        this.loading = false
      }
    },

    async fetchClusters() {
      try {
        const data = await listClusters()
        this.clusters = data.contexts || []
        this.activeContext = data.active || ''
        this.inClusterMode = !!data.in_cluster
        this.kindAvailable = !!data.kind_available
      } catch {
        // Non-critical — keep whatever we had
      }
    },

    // Called after select/create/delete. Clears cluster-scoped state and
    // refetches so the UI reflects the new active context rather than
    // flashing stale VMs from the previous cluster.
    async onClusterChanged() {
      this.vms = []
      this.launches = []
      this.clusterInfo = null
      this.clusterResources = null
      this.groups = []
      this.vmGroups = {}
      this.selectedNode = null
      this.selectedVms = []
      await this.fetchClusters()
      await this.fetchVMs()
    },

    async fetchGroups() {
      try {
        const data = await listGroups()
        this.groups = data.groups || []
        this.vmGroups = data.vmGroups || {}
      } catch {
        // Non-critical — keep existing state
      }
    },

    async fetchProfiles() {
      try {
        const data = await listProfiles()
        this.profiles = Array.isArray(data) ? data : []
      } catch {
        // Non-critical — keep existing state
      }
    },

    async dismissFailedLaunch(name) {
      try {
        await dismissLaunch(name)
        this.launches = this.launches.filter(l => l.name !== name)
      } catch { /* ignore */ }
    },

    selectNode(name) {
      this.selectedNode = name
    },

    toggleVmSelection(name) {
      const idx = this.selectedVms.indexOf(name)
      if (idx >= 0) {
        this.selectedVms.splice(idx, 1)
      } else {
        this.selectedVms.push(name)
      }
    },

    selectAllVms() {
      // Select all currently-visible VMs — respects the OS filter so users
      // don't accidentally bulk-act on hidden VMs.
      this.selectedVms = this.visibleVms.map(vm => vm.name)
    },

    clearSelection() {
      this.selectedVms = []
    },

    toggleGroupExpanded(name) {
      this.expandedGroups[name] = !this.expandedGroups[name]
    },

    expandAllGroups() {
      for (const g of this.groups) {
        this.expandedGroups[g] = true
      }
    },

    collapseAllGroups() {
      for (const g of this.groups) {
        this.expandedGroups[g] = false
      }
    },
  },
})
