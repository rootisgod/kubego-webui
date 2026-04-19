<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useVmStore } from '../../stores/vmStore.js'
import { useToastStore } from '../../stores/toastStore.js'
import * as api from '../../api/client.js'
import { Copy, ExternalLink, Plus, Trash2, RefreshCw, Terminal, Globe, Cable, Loader2, Package, Download } from 'lucide-vue-next'
import KindProgressModal from '../modals/KindProgressModal.vue'

const store = useVmStore()
const toasts = useToastStore()
const vm = computed(() => store.selectedVm)
const isRunning = computed(() => vm.value?.state === 'Running')

const forwards = ref([])
const loading = ref(false)
const creating = ref(false)

// Ingress-controller state + managed exposures for this VM.
const controller = ref({ installed: false, ready: false, replicas: 0, host_ip: '' })
const ingresses = ref([])
const controllerLoading = ref(false)
const exposing = ref(false)
const exposePort = ref('80')

// Install modal state (reuses KindProgressModal).
const installModalOpen = ref(false)
const installStatus = ref('running') // running|done|error
const installLines = ref([])
const installError = ref('')

// Custom-port form state
const customPort = ref('')
const customLabel = ref('')
const showCustom = ref(false)

let pollTimer = null

async function refresh() {
  if (!store.selectedNode) return
  loading.value = true
  try {
    const [pf, ing, ctrl] = await Promise.all([
      api.listPortForwards(store.selectedNode).catch(() => []),
      api.listVMIngresses(store.selectedNode).catch(() => []),
      api.ingressStatus().catch(() => ({ installed: false, ready: false })),
    ])
    forwards.value = Array.isArray(pf) ? pf : []
    ingresses.value = Array.isArray(ing) ? ing : []
    controller.value = ctrl || { installed: false, ready: false }
  } catch (e) {
    toasts.error('Failed to load connect state: ' + e.message)
  } finally {
    loading.value = false
  }
}

async function runInstall() {
  installModalOpen.value = true
  installStatus.value = 'running'
  installLines.value = []
  installError.value = ''
  try {
    await api.installIngress((ev) => {
      if (ev.type === 'output' && ev.line !== undefined) {
        installLines.value.push(ev.line)
      }
    })
    installStatus.value = 'done'
    refresh()
  } catch (e) {
    installStatus.value = 'error'
    installError.value = e.message || 'install failed'
  }
}

async function exposePortOnIngress() {
  const port = parseInt(exposePort.value, 10)
  if (!port || port <= 0 || port > 65535) {
    toasts.error('Enter a port between 1 and 65535')
    return
  }
  exposing.value = true
  try {
    const info = await api.createVMIngress(store.selectedNode, port)
    toasts.success(`Exposed on ${info.hostname}`)
    await refresh()
  } catch (e) {
    toasts.error(e.message)
  } finally {
    exposing.value = false
  }
}

async function deleteExposure(id) {
  try {
    await api.deleteVMIngress(store.selectedNode, id)
    toasts.success('Exposure removed')
    await refresh()
  } catch (e) {
    toasts.error(e.message)
  }
}

async function createForward(remotePort, protocol, label) {
  creating.value = true
  try {
    const entry = await api.createPortForward(store.selectedNode, {
      remote_port: remotePort,
      protocol,
      label: label || '',
    })
    toasts.success(`Forwarding :${remotePort} -> localhost:${entry.local_port}`)
    await refresh()
  } catch (e) {
    toasts.error(e.message)
  } finally {
    creating.value = false
  }
}

async function createCustom() {
  const port = parseInt(customPort.value, 10)
  if (!port || port <= 0 || port > 65535) {
    toasts.error('Enter a port between 1 and 65535')
    return
  }
  await createForward(port, 'tcp', customLabel.value)
  customPort.value = ''
  customLabel.value = ''
  showCustom.value = false
}

async function closeForward(id) {
  try {
    await api.deletePortForward(store.selectedNode, id)
    toasts.success('Port-forward closed')
    await refresh()
  } catch (e) {
    toasts.error(e.message)
  }
}

function copyToClipboard(text) {
  navigator.clipboard.writeText(text).then(
    () => toasts.success('Copied'),
    () => toasts.error('Copy failed'),
  )
}

function sshCommand(f) {
  const user = vm.value?.release?.startsWith('debian') ? 'debian'
    : vm.value?.release?.startsWith('fedora') ? 'fedora'
    : vm.value?.release?.startsWith('centos') ? 'centos'
    : vm.value?.release?.startsWith('rocky') ? 'cloud-user'
    : 'ubuntu'
  // The server's injected key lives next to config.json on disk; we can't
  // introspect that path from the browser so we show a generic reference
  // that the user resolves locally. KubeGo's default path is
  // ~/.config/kubego/id_kubego_ed25519 on Linux.
  return `ssh -o StrictHostKeyChecking=accept-new -i <kubego-id-key> -p ${f.local_port} ${user}@localhost`
}

function proxyUrl(f) {
  return `${window.location.origin}/proxy/${f.vm}/${f.remote_port}/`
}

function protocolIcon(p) {
  if (p === 'ssh') return Terminal
  if (p === 'http') return Globe
  return Cable
}

onMounted(() => {
  refresh()
  // Light poll to pick up idle-reaped entries
  pollTimer = setInterval(refresh, 15000)
})

onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<template>
  <div class="p-4 space-y-4">
    <div class="flex items-center justify-between">
      <div>
        <h3 class="text-sm font-semibold text-[var(--text-primary)]">Connect</h3>
        <p class="text-xs text-[var(--text-secondary)] mt-0.5">
          Port-forward tunnels from KubeGo to this VM. Idle forwards are closed after 30 minutes.
        </p>
      </div>
      <button
        @click="refresh"
        :disabled="loading"
        class="flex items-center gap-1.5 px-2 py-1 text-xs rounded bg-[var(--bg-hover)] hover:bg-[var(--border)] transition-colors disabled:opacity-40"
      >
        <RefreshCw class="w-3.5 h-3.5" :class="loading ? 'animate-spin' : ''" />
        Refresh
      </button>
    </div>

    <div v-if="!isRunning" class="p-3 rounded bg-[var(--bg-surface)] border border-[var(--border)] text-xs text-[var(--text-secondary)]">
      The VM must be running to open a port-forward.
    </div>

    <!-- Quick actions -->
    <div v-if="isRunning" class="grid grid-cols-3 gap-2">
      <button
        @click="createForward(22, 'ssh', 'SSH')"
        :disabled="creating"
        class="flex items-center justify-center gap-2 px-3 py-2.5 text-sm rounded bg-[var(--bg-surface)] border border-[var(--border)] hover:border-[var(--accent)] transition-colors disabled:opacity-40"
      >
        <Terminal class="w-4 h-4" />
        Forward SSH (:22)
      </button>
      <button
        @click="createForward(80, 'http', 'HTTP')"
        :disabled="creating"
        class="flex items-center justify-center gap-2 px-3 py-2.5 text-sm rounded bg-[var(--bg-surface)] border border-[var(--border)] hover:border-[var(--accent)] transition-colors disabled:opacity-40"
      >
        <Globe class="w-4 h-4" />
        Forward HTTP (:80)
      </button>
      <button
        @click="showCustom = !showCustom"
        :disabled="creating"
        class="flex items-center justify-center gap-2 px-3 py-2.5 text-sm rounded bg-[var(--bg-surface)] border border-[var(--border)] hover:border-[var(--accent)] transition-colors disabled:opacity-40"
      >
        <Plus class="w-4 h-4" />
        Custom port
      </button>
    </div>

    <!-- Custom-port form -->
    <div v-if="showCustom" class="p-3 rounded bg-[var(--bg-surface)] border border-[var(--border)] space-y-2">
      <div class="grid grid-cols-[1fr_2fr_auto] gap-2">
        <input
          v-model="customPort"
          type="number"
          min="1"
          max="65535"
          placeholder="Port (e.g. 3000)"
          class="bg-[var(--bg-primary)] border border-[var(--border)] rounded px-2 py-1.5 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
        />
        <input
          v-model="customLabel"
          type="text"
          placeholder="Label (optional)"
          class="bg-[var(--bg-primary)] border border-[var(--border)] rounded px-2 py-1.5 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
        />
        <button
          @click="createCustom"
          :disabled="creating || !customPort"
          class="flex items-center gap-1.5 px-3 py-1.5 text-sm rounded bg-[var(--accent)] hover:bg-blue-600 transition-colors disabled:opacity-40 text-white"
        >
          <Loader2 v-if="creating" class="w-3.5 h-3.5 animate-spin" />
          Open
        </button>
      </div>
    </div>

    <!-- Active forwards -->
    <div v-if="forwards.length === 0 && !loading && isRunning" class="p-4 text-center text-sm text-[var(--text-secondary)]">
      No active forwards. Pick a quick action above or open a custom port.
    </div>

    <div v-else-if="forwards.length > 0" class="space-y-2">
      <div
        v-for="f in forwards"
        :key="f.id"
        class="p-3 rounded bg-[var(--bg-surface)] border border-[var(--border)]"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="flex items-center gap-2">
            <component :is="protocolIcon(f.protocol)" class="w-4 h-4 text-[var(--accent)]" />
            <div>
              <div class="text-sm font-medium text-[var(--text-primary)]">
                {{ f.label || `Port ${f.remote_port}` }}
              </div>
              <div class="text-xs text-[var(--text-secondary)]">
                VM :{{ f.remote_port }} &rarr; localhost:{{ f.local_port }}
              </div>
            </div>
          </div>
          <button
            @click="closeForward(f.id)"
            class="p-1.5 rounded text-[var(--text-secondary)] hover:text-red-400 hover:bg-[var(--bg-hover)] transition-colors"
            title="Close forward"
          >
            <Trash2 class="w-3.5 h-3.5" />
          </button>
        </div>

        <!-- SSH hint -->
        <div v-if="f.protocol === 'ssh'" class="mt-2 flex items-center gap-1.5">
          <code class="flex-1 px-2 py-1.5 rounded bg-[var(--bg-primary)] text-xs font-mono text-[var(--text-primary)] overflow-x-auto whitespace-nowrap">
            {{ sshCommand(f) }}
          </code>
          <button
            @click="copyToClipboard(sshCommand(f))"
            class="p-1.5 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
            title="Copy"
          >
            <Copy class="w-3.5 h-3.5" />
          </button>
        </div>

        <!-- HTTP: show proxy URL -->
        <div v-else-if="f.protocol === 'http'" class="mt-2 space-y-1.5">
          <div class="flex items-center gap-1.5">
            <code class="flex-1 px-2 py-1.5 rounded bg-[var(--bg-primary)] text-xs font-mono text-[var(--text-primary)] overflow-x-auto whitespace-nowrap">
              {{ proxyUrl(f) }}
            </code>
            <a
              :href="proxyUrl(f)"
              target="_blank"
              rel="noopener"
              class="p-1.5 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
              title="Open in new tab"
            >
              <ExternalLink class="w-3.5 h-3.5" />
            </a>
            <button
              @click="copyToClipboard(proxyUrl(f))"
              class="p-1.5 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
              title="Copy"
            >
              <Copy class="w-3.5 h-3.5" />
            </button>
          </div>
          <p class="text-[11px] text-[var(--text-secondary)]">
            Path-prefix proxy: apps that redirect to absolute paths ("/login") may escape the proxy. For a clean root-path URL, use Tier 1-B ingress.
          </p>
        </div>

        <!-- Raw TCP -->
        <div v-else class="mt-2 flex items-center gap-1.5">
          <code class="flex-1 px-2 py-1.5 rounded bg-[var(--bg-primary)] text-xs font-mono text-[var(--text-primary)] overflow-x-auto whitespace-nowrap">
            localhost:{{ f.local_port }}
          </code>
          <button
            @click="copyToClipboard(`localhost:${f.local_port}`)"
            class="p-1.5 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
            title="Copy"
          >
            <Copy class="w-3.5 h-3.5" />
          </button>
        </div>
      </div>
    </div>

    <!-- Ingress section: publish a VM port through ingress-nginx. Cluster-wide
         and persistent, unlike port-forwards. Requires the controller to be
         installed — offer a one-click install if it isn't. -->
    <div class="pt-4 border-t border-[var(--border)]">
      <div class="flex items-center gap-2 mb-2">
        <Package class="w-4 h-4 text-[var(--accent)]" />
        <h3 class="text-sm font-semibold text-[var(--text-primary)]">Ingress exposures</h3>
      </div>
      <p class="text-xs text-[var(--text-secondary)] mb-3">
        Publish a VM port through ingress-nginx with a nip.io hostname. Persists across reloads.
      </p>

      <div v-if="!controller.installed" class="p-3 rounded bg-[var(--bg-surface)] border border-[var(--border)]">
        <div class="flex items-center justify-between gap-3">
          <div class="text-xs text-[var(--text-secondary)]">
            ingress-nginx is not installed in this cluster.
          </div>
          <button
            @click="runInstall"
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-[var(--accent)] text-white hover:bg-blue-600 transition-colors"
          >
            <Download class="w-3.5 h-3.5" />
            Install (KinD preset)
          </button>
        </div>
      </div>

      <div v-else-if="!controller.ready" class="p-3 rounded bg-[var(--bg-surface)] border border-[var(--border)] text-xs text-[var(--text-secondary)]">
        ingress-nginx is installed but not ready yet. Try again in a moment.
      </div>

      <div v-else class="space-y-3">
        <div v-if="isRunning" class="p-3 rounded bg-[var(--bg-surface)] border border-[var(--border)]">
          <div class="grid grid-cols-[1fr_auto] gap-2 items-end">
            <div>
              <label class="block text-[11px] text-[var(--text-secondary)] mb-1">VM port to expose</label>
              <input
                v-model="exposePort"
                type="number"
                min="1"
                max="65535"
                placeholder="80"
                class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-2 py-1.5 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
              />
            </div>
            <button
              @click="exposePortOnIngress"
              :disabled="exposing || !exposePort"
              class="flex items-center gap-1.5 px-3 py-1.5 text-sm rounded bg-[var(--accent)] text-white hover:bg-blue-600 transition-colors disabled:opacity-40"
            >
              <Loader2 v-if="exposing" class="w-3.5 h-3.5 animate-spin" />
              <Plus v-else class="w-3.5 h-3.5" />
              Expose
            </button>
          </div>
          <p class="text-[11px] text-[var(--text-secondary)] mt-2">
            Hostname pattern: <code>&lt;vm&gt;-&lt;port&gt;.{{ controller.host_ip || '&lt;node-ip&gt;' }}.nip.io</code>
          </p>
        </div>

        <div v-if="ingresses.length === 0 && isRunning" class="px-3 text-xs text-[var(--text-secondary)]">
          No exposures yet. Pick a port above.
        </div>

        <div
          v-for="ing in ingresses"
          :key="ing.id"
          class="p-3 rounded bg-[var(--bg-surface)] border border-[var(--border)]"
        >
          <div class="flex items-start justify-between gap-3">
            <div class="flex items-center gap-2">
              <Globe class="w-4 h-4 text-[var(--accent)]" />
              <div>
                <div class="text-sm font-medium text-[var(--text-primary)]">Port {{ ing.remote_port }}</div>
                <div class="text-xs text-[var(--text-secondary)]">{{ ing.hostname }}</div>
              </div>
            </div>
            <button
              @click="deleteExposure(ing.id)"
              class="p-1.5 rounded text-[var(--text-secondary)] hover:text-red-400 hover:bg-[var(--bg-hover)] transition-colors"
              title="Remove exposure"
            >
              <Trash2 class="w-3.5 h-3.5" />
            </button>
          </div>
          <div class="mt-2 flex items-center gap-1.5">
            <code class="flex-1 px-2 py-1.5 rounded bg-[var(--bg-primary)] text-xs font-mono text-[var(--text-primary)] overflow-x-auto whitespace-nowrap">
              {{ ing.url }}
            </code>
            <a
              :href="ing.url"
              target="_blank"
              rel="noopener"
              class="p-1.5 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
              title="Open in new tab"
            >
              <ExternalLink class="w-3.5 h-3.5" />
            </a>
            <button
              @click="copyToClipboard(ing.url)"
              class="p-1.5 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]"
              title="Copy"
            >
              <Copy class="w-3.5 h-3.5" />
            </button>
          </div>
        </div>
      </div>
    </div>

    <KindProgressModal
      v-if="installModalOpen"
      title="Installing ingress-nginx (KinD preset)"
      :status="installStatus"
      :lines="installLines"
      :error-message="installError"
      @close="installModalOpen = false"
    />
  </div>
</template>
