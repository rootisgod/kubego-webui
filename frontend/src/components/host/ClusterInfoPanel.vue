<script setup>
import { computed } from 'vue'
import { useVmStore } from '../../stores/vmStore.js'
import { Box, Server, Cpu, Zap, AlertTriangle, CircleCheck } from 'lucide-vue-next'

const store = useVmStore()
const info = computed(() => store.clusterInfo)

const virtBadge = computed(() => {
  const v = info.value?.virtualisation
  if (!v) return null
  switch (v.mode) {
    case 'kvm':
      return { label: 'KVM (hardware)', classes: 'bg-green-900/30 text-[var(--success)] border-green-800', icon: Zap }
    case 'emulation':
      return { label: 'Software emulation', classes: 'bg-amber-900/30 text-[var(--warning)] border-amber-800', icon: AlertTriangle }
    case 'mixed':
      return { label: 'Mixed', classes: 'bg-amber-900/30 text-[var(--warning)] border-amber-800', icon: AlertTriangle }
    default:
      return { label: 'Unknown', classes: 'bg-gray-800/30 text-[var(--muted)] border-gray-700', icon: AlertTriangle }
  }
})

const flavorLabel = computed(() => {
  const f = info.value?.flavor
  if (f === 'kind') return 'KinD'
  if (f === 'k3s') return 'k3s'
  return 'Generic'
})

function fmtMB(mb) {
  if (!mb) return '—'
  if (mb >= 1024) return (mb / 1024).toFixed(1) + ' GiB'
  return mb + ' MiB'
}
</script>

<template>
  <div v-if="info" class="bg-[var(--bg-surface)] rounded-lg border border-[var(--border)] p-4 mb-6">
    <!-- Header -->
    <div class="flex items-center justify-between mb-4">
      <div class="flex items-center gap-3">
        <Box class="w-6 h-6 text-[var(--accent)]" />
        <div>
          <div class="text-lg font-semibold">{{ info.name }}</div>
          <div class="text-xs text-[var(--text-secondary)] font-mono">
            {{ flavorLabel }} · {{ info.context || 'no context' }}
          </div>
        </div>
      </div>
      <span
        v-if="virtBadge"
        class="px-2.5 py-1 rounded text-xs border flex items-center gap-1.5"
        :class="virtBadge.classes"
      >
        <component :is="virtBadge.icon" class="w-3.5 h-3.5" />
        {{ virtBadge.label }}
      </span>
    </div>

    <!-- Summary row -->
    <p v-if="info.virtualisation?.summary" class="text-xs text-[var(--text-secondary)] mb-4">
      {{ info.virtualisation.summary }}
    </p>

    <!-- Metadata grid -->
    <div class="grid grid-cols-2 md:grid-cols-4 gap-3 text-xs mb-4">
      <div>
        <div class="text-[var(--muted)] uppercase tracking-wider text-[10px] mb-0.5">Kubernetes</div>
        <div class="font-mono">{{ info.kubernetes_version || '—' }}</div>
      </div>
      <div>
        <div class="text-[var(--muted)] uppercase tracking-wider text-[10px] mb-0.5">KubeVirt</div>
        <div class="font-mono">
          <span v-if="info.kubevirt?.installed">{{ info.kubevirt.version || info.kubevirt.phase || '—' }}</span>
          <span v-else class="text-[var(--danger)]">not installed</span>
        </div>
      </div>
      <div>
        <div class="text-[var(--muted)] uppercase tracking-wider text-[10px] mb-0.5">CDI</div>
        <div class="font-mono">
          <span v-if="info.cdi?.installed">{{ info.cdi.version || info.cdi.phase || '—' }}</span>
          <span v-else class="text-[var(--muted)]">not installed</span>
        </div>
      </div>
      <div>
        <div class="text-[var(--muted)] uppercase tracking-wider text-[10px] mb-0.5">API Server</div>
        <div class="font-mono truncate" :title="info.api_server">{{ info.api_server }}</div>
      </div>
    </div>

    <!-- Nodes table -->
    <div v-if="info.nodes?.length" class="border border-[var(--border)] rounded overflow-hidden">
      <table class="w-full text-xs">
        <thead class="bg-[var(--bg-secondary)] text-[var(--text-secondary)]">
          <tr>
            <th class="text-left px-3 py-2 font-medium">Node</th>
            <th class="text-left px-3 py-2 font-medium">Role</th>
            <th class="text-left px-3 py-2 font-medium">Ready</th>
            <th class="text-left px-3 py-2 font-medium">KVM</th>
            <th class="text-left px-3 py-2 font-medium">CPU</th>
            <th class="text-left px-3 py-2 font-medium">Memory</th>
            <th class="text-left px-3 py-2 font-medium">Disk</th>
            <th class="text-left px-3 py-2 font-medium">OS</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="n in info.nodes" :key="n.name" class="border-t border-[var(--border)]">
            <td class="px-3 py-1.5 font-mono">{{ n.name }}</td>
            <td class="px-3 py-1.5 text-[var(--text-secondary)]">{{ n.role }}</td>
            <td class="px-3 py-1.5">
              <CircleCheck v-if="n.ready" class="w-3.5 h-3.5 text-[var(--success)]" />
              <AlertTriangle v-else class="w-3.5 h-3.5 text-[var(--danger)]" />
            </td>
            <td class="px-3 py-1.5">
              <span v-if="n.kvm_capacity > 0" class="text-[var(--success)]">yes</span>
              <span v-else class="text-[var(--warning)]">no</span>
            </td>
            <td class="px-3 py-1.5 font-mono">{{ n.cpus }}</td>
            <td class="px-3 py-1.5 font-mono">{{ fmtMB(n.memory_mb) }}</td>
            <td class="px-3 py-1.5 font-mono">{{ fmtMB(n.disk_mb) }}</td>
            <td class="px-3 py-1.5 text-[var(--text-secondary)] truncate max-w-[200px]" :title="n.os_image + ' / ' + n.kernel_version">
              {{ n.os_image }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
