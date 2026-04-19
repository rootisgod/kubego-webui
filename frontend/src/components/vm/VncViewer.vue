<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useVmStore } from '../../stores/vmStore.js'
import { useToastStore } from '../../stores/toastStore.js'
import { startVM } from '../../api/client.js'
import { PowerOff, Play, Monitor, RefreshCw, WifiOff } from 'lucide-vue-next'
import RFB from '@novnc/novnc/lib/rfb.js'

const props = defineProps({
  active: { type: Boolean, default: true },
})

const store = useVmStore()
const toasts = useToastStore()
const vm = computed(() => store.selectedVm)
const isRunning = computed(() => vm.value?.state === 'Running')
const isDeleted = computed(() => vm.value?.state === 'Deleted')
const starting = ref(false)

const container = ref(null)
const status = ref('idle') // 'idle' | 'connecting' | 'connected' | 'disconnected' | 'error'
const errorMsg = ref('')

let rfb = null

function wsURL() {
  // Same-origin WS URL. Session cookie is sent automatically, so auth
  // middleware happily upgrades us.
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}/api/v1/vms/${store.selectedNode}/vnc`
}

function connect() {
  if (!container.value || !isRunning.value) return
  if (rfb) {
    try { rfb.disconnect() } catch { /* ignore */ }
    rfb = null
  }
  errorMsg.value = ''
  status.value = 'connecting'
  try {
    rfb = new RFB(container.value, wsURL(), {
      // KubeVirt's VNC endpoint doesn't require a password (the apiserver
      // authenticates the connection). noVNC nevertheless expects the
      // credentials slot to exist.
      credentials: { password: '' },
      wsProtocols: ['binary'],
    })
    rfb.viewOnly = false
    rfb.scaleViewport = true   // fit framebuffer into the tab
    rfb.resizeSession = false  // don't ask the VM to match browser size
    rfb.background = '#0b0b0b'

    rfb.addEventListener('connect', () => { status.value = 'connected' })
    rfb.addEventListener('disconnect', (e) => {
      status.value = e.detail && e.detail.clean ? 'disconnected' : 'error'
      if (!e.detail?.clean) {
        errorMsg.value = 'Disconnected unexpectedly — the VM may have stopped.'
      }
    })
    rfb.addEventListener('securityfailure', (e) => {
      status.value = 'error'
      errorMsg.value = `Security failure: ${e.detail?.reason || 'unknown'}`
    })
    rfb.addEventListener('credentialsrequired', () => {
      status.value = 'error'
      errorMsg.value = 'The VM requested a VNC password; KubeGo does not configure one.'
    })
  } catch (e) {
    status.value = 'error'
    errorMsg.value = e.message || 'failed to start RFB client'
  }
}

function disconnect() {
  if (rfb) {
    try { rfb.disconnect() } catch { /* ignore */ }
    rfb = null
  }
  status.value = 'idle'
}

async function powerOn() {
  starting.value = true
  try {
    await startVM(store.selectedNode)
    toasts.success(`${store.selectedNode} starting...`)
    store.fetchVMs()
  } catch (e) {
    toasts.error(e.message)
  } finally {
    starting.value = false
  }
}

function reconnect() { connect() }

// Reconnect when the tab becomes active or the VM starts running.
watch(() => [props.active, isRunning.value, store.selectedNode], ([active, running, node]) => {
  if (active && running && node) {
    if (!rfb) connect()
  } else {
    disconnect()
  }
})

onMounted(() => {
  if (props.active && isRunning.value) connect()
})

onBeforeUnmount(() => {
  disconnect()
})
</script>

<template>
  <!-- VM deleted -->
  <div v-if="isDeleted" class="flex flex-col items-center justify-center h-full gap-4 text-[var(--text-secondary)]">
    <PowerOff class="w-12 h-12 text-[var(--muted)]" />
    <p class="text-lg">VM Deleted</p>
    <p class="text-sm">Recover this VM to access the graphical console</p>
  </div>

  <!-- VM not running -->
  <div v-else-if="!isRunning" class="flex flex-col items-center justify-center h-full gap-4 text-[var(--text-secondary)]">
    <PowerOff class="w-12 h-12 text-[var(--muted)]" />
    <p class="text-lg">Powered Off</p>
    <p class="text-sm">Start the VM to access the graphical console</p>
    <button
      @click="powerOn"
      :disabled="starting"
      class="flex items-center gap-2 mt-2 px-4 py-2 text-sm rounded bg-green-900/30 hover:bg-[var(--success)] text-green-300 hover:text-white transition-colors disabled:opacity-40"
    >
      <Play class="w-4 h-4" />
      {{ starting ? 'Starting...' : 'Start VM' }}
    </button>
  </div>

  <!-- VM running — show noVNC canvas -->
  <div v-else class="flex flex-col h-full bg-black">
    <!-- Status bar -->
    <div class="flex items-center justify-between gap-2 px-3 py-1.5 bg-[var(--bg-surface)] border-b border-[var(--border)] text-xs">
      <div class="flex items-center gap-2 text-[var(--text-secondary)]">
        <Monitor class="w-3.5 h-3.5" />
        <span v-if="status === 'connecting'">Connecting…</span>
        <span v-else-if="status === 'connected'" class="text-[var(--success)]">Connected</span>
        <span v-else-if="status === 'disconnected'">Disconnected</span>
        <span v-else-if="status === 'error'" class="text-red-400">{{ errorMsg || 'Error' }}</span>
        <span v-else>Idle</span>
      </div>
      <div class="flex items-center gap-1.5">
        <button
          v-if="status !== 'connecting' && status !== 'connected'"
          @click="reconnect"
          class="flex items-center gap-1 px-2 py-0.5 rounded bg-[var(--bg-hover)] hover:bg-[var(--border)] text-[var(--text-secondary)] transition-colors"
        >
          <RefreshCw class="w-3 h-3" />
          Reconnect
        </button>
        <button
          v-else-if="status === 'connected'"
          @click="disconnect"
          class="flex items-center gap-1 px-2 py-0.5 rounded bg-[var(--bg-hover)] hover:bg-[var(--border)] text-[var(--text-secondary)] transition-colors"
        >
          <WifiOff class="w-3 h-3" />
          Disconnect
        </button>
      </div>
    </div>
    <!-- Canvas host. noVNC owns the DOM inside. -->
    <div ref="container" class="flex-1 relative overflow-hidden">
      <div
        v-if="status === 'connecting'"
        class="absolute inset-0 flex items-center justify-center text-[var(--text-secondary)] text-sm pointer-events-none"
      >
        Connecting to the VNC console…
      </div>
    </div>
  </div>
</template>
