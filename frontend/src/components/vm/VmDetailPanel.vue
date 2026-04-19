<script setup>
import { ref, watch } from 'vue'
import { useVmStore } from '../../stores/vmStore.js'
import VmSummaryTab from './VmSummaryTab.vue'
import VmConsoleTab from './VmConsoleTab.vue'
import VncViewer from './VncViewer.vue'
import VmConnectTab from './VmConnectTab.vue'
import VmSnapshotsTab from './VmSnapshotsTab.vue'
import VmDisksTab from './VmDisksTab.vue'
import VmTransferTab from './VmTransferTab.vue'
import VmConfigTab from './VmConfigTab.vue'
import VmAnsibleTab from './VmAnsibleTab.vue'
import VmLogsTab from './VmLogsTab.vue'

const store = useVmStore()
const activeTab = ref('summary')
// Latches true the first time the user visits the console tab for the
// current VM. Keeps VmConsoleTab mounted after that (via v-show) so the
// WebSocket stays open and the xterm scrollback survives tab switches,
// without paying the mount/connect cost for VMs whose console is never
// opened.
const consoleEverOpened = ref(false)

const tabs = [
  { id: 'summary', label: 'Summary' },
  { id: 'console', label: 'Console' },
  { id: 'graphics', label: 'Graphics' },
  { id: 'connect', label: 'Connect' },
  { id: 'logs', label: 'Logs' },
  { id: 'snapshots', label: 'Snapshots' },
  { id: 'disks', label: 'Disks' },
  { id: 'files', label: 'Files' },
  { id: 'config', label: 'Config' },
  { id: 'ansible', label: 'Ansible' },
]

// Reset tab + console-open latch when the selected VM changes.
watch(() => store.selectedNode, () => {
  activeTab.value = 'summary'
  consoleEverOpened.value = false
})

watch(activeTab, (tab) => {
  if (tab === 'console') consoleEverOpened.value = true
})
</script>

<template>
  <div class="flex flex-col h-full" v-if="store.selectedVm">
    <!-- Tab bar -->
    <div class="flex border-b border-[var(--border)] bg-[var(--bg-secondary)] px-4">
      <button
        v-for="tab in tabs"
        :key="tab.id"
        class="px-4 py-2.5 text-sm transition-colors relative"
        :class="activeTab === tab.id
          ? 'text-[var(--accent)]'
          : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'"
        @click="activeTab = tab.id"
      >
        {{ tab.label }}
        <div
          v-if="activeTab === tab.id"
          class="absolute bottom-0 left-0 right-0 h-0.5 bg-[var(--accent)] tab-indicator"
        />
      </button>
    </div>

    <!-- Tab content -->
    <div class="flex-1 overflow-auto">
      <!-- Ansible tab rendered outside Transition (CodeMirror conflict) -->
      <VmAnsibleTab v-if="activeTab === 'ansible'" :vm-name="store.selectedNode" :key="'ansible-' + store.selectedNode" />
      <!-- Console rendered outside Transition and kept mounted (v-show) so
           the WebSocket and xterm scrollback survive tab switches. -->
      <VmConsoleTab
        v-if="consoleEverOpened"
        v-show="activeTab === 'console'"
        :key="'console-' + store.selectedNode"
      />
      <Transition v-if="activeTab !== 'ansible' && activeTab !== 'console'" name="fade" mode="out-in">
        <VmSummaryTab v-if="activeTab === 'summary'" :key="'summary-' + store.selectedNode" />
        <VncViewer v-else-if="activeTab === 'graphics'" :active="true" :key="'vnc-' + store.selectedNode" />
        <VmConnectTab v-else-if="activeTab === 'connect'" :key="'connect-' + store.selectedNode" />
        <VmLogsTab v-else-if="activeTab === 'logs'" :key="'logs-' + store.selectedNode" />
        <VmSnapshotsTab v-else-if="activeTab === 'snapshots'" :key="'snap-' + store.selectedNode" />
        <VmDisksTab v-else-if="activeTab === 'disks'" :key="'disks-' + store.selectedNode" />
        <VmTransferTab v-else-if="activeTab === 'files'" :key="'files-' + store.selectedNode" />
        <VmConfigTab v-else-if="activeTab === 'config'" :key="'config-' + store.selectedNode" />
      </Transition>
    </div>
  </div>

  <div v-else class="flex items-center justify-center h-full text-[var(--text-secondary)]">
    Select a VM from the tree
  </div>
</template>
