<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { useVmStore } from '../../stores/vmStore.js'
import { useToastStore } from '../../stores/toastStore.js'
import {
  hostCheck, createK9sSession, listK9sSessions, deleteK9sSession, installK9s,
} from '../../api/client.js'
import K9sTerminal from './K9sTerminal.vue'
import KindProgressModal from '../modals/KindProgressModal.vue'
import { Download, Plus, X, Loader2, Terminal as TerminalIcon, AlertTriangle } from 'lucide-vue-next'

const store = useVmStore()
const toasts = useToastStore()

const loading = ref(true)
const k9sAvailable = ref(false)
const installProgress = ref(null) // { title, status, lines, errorMessage }
const tabs = ref([]) // [{ sessionId, label }]
const activeSessionId = ref(null)
let tabCounter = 0

// Disable the panel entirely in in-cluster mode — k9s would inherit
// cluster-admin via the service account, which is too broad.
const inClusterMode = computed(() => store.inClusterMode)
const noActiveCluster = computed(() => !store.activeContext)

// Panel is "active" when the user has selected the k9s sidebar entry.
// The component stays mounted either way (see App.vue v-show) so tabs
// and their WebSockets survive navigation — isActive just tells us
// whether to focus/auto-spawn.
const isActive = computed(() => store.selectedNode === '__k9s__')

async function checkAvailability() {
  loading.value = true
  try {
    const data = await hostCheck()
    const k9sTool = data.tools.find(t => t.name === 'k9s')
    k9sAvailable.value = !!(k9sTool && k9sTool.available)
  } catch (e) {
    toasts.error(`host check failed: ${e.message}`)
  } finally {
    loading.value = false
  }
}

async function addTab() {
  try {
    const { sessionId } = await createK9sSession()
    tabs.value.push({ sessionId, label: `k9s ${++tabCounter}` })
    activeSessionId.value = sessionId
  } catch (e) {
    toasts.error(`Failed to start k9s: ${e.message}`)
  }
}

async function closeTab(sessionId) {
  try {
    await deleteK9sSession(sessionId)
  } catch {
    // Already gone — remove locally anyway.
  }
  const idx = tabs.value.findIndex(t => t.sessionId === sessionId)
  if (idx >= 0) tabs.value.splice(idx, 1)
  if (activeSessionId.value === sessionId) {
    activeSessionId.value = tabs.value[0]?.sessionId || null
  }
}

function switchTab(sessionId) {
  activeSessionId.value = sessionId
}

async function doInstall() {
  installProgress.value = {
    title: 'Installing k9s…',
    status: 'running',
    lines: [],
    errorMessage: '',
  }
  try {
    await installK9s((ev) => {
      if (ev.type === 'output') installProgress.value.lines.push(ev.line)
    })
    installProgress.value.status = 'done'
    toasts.success('k9s installed')
    await checkAvailability()
  } catch (e) {
    installProgress.value.status = 'error'
    installProgress.value.errorMessage = e.message
  }
}

// Single guard for concurrent spawn attempts — both the cluster-switch
// handler and the auto-spawn watch can race on the same empty-tabs
// state.
let spawning = false
async function tryAutoSpawn() {
  if (spawning) return
  if (!isActive.value || loading.value) return
  if (tabs.value.length > 0) return
  if (!k9sAvailable.value || inClusterMode.value || noActiveCluster.value) return
  spawning = true
  try {
    await addTab()
  } finally {
    spawning = false
  }
}

// Auto-spawn triggers: user navigates to k9s, availability check
// resolves, or k9s gets installed. Deliberately NOT watching
// tabs.length — a user who clicks the X on their last tab should not
// have one respawn at them.
watch([isActive, loading, k9sAvailable], () => {
  tryAutoSpawn()
}, { immediate: true })

// Close all sessions when the active cluster changes — k9s was spawned
// against the previous context and any further navigation would be
// against the wrong cluster. The sidebar makes this cluster-scoping
// visible (k9s sits under the switcher, alongside the VM tree), so
// "switch cluster = fresh k9s" is expected. If the user is still
// looking at the panel, auto-spawn a replacement for the new cluster
// instead of dropping them at the empty state.
watch(() => store.activeContext, async (_, prev) => {
  if (prev === undefined) return // initial load
  for (const t of [...tabs.value]) {
    await closeTab(t.sessionId)
  }
  if (isActive.value) await tryAutoSpawn()
})

onMounted(checkAvailability)
</script>

<template>
  <div class="h-full flex flex-col">
    <!-- Disabled state: in-cluster mode -->
    <div
      v-if="inClusterMode"
      class="p-6 flex flex-col items-center justify-center h-full gap-3 text-[var(--text-secondary)]"
    >
      <AlertTriangle class="w-12 h-12 text-[var(--warning)]" />
      <p class="text-lg">k9s disabled in in-cluster mode</p>
      <p class="text-sm max-w-md text-center">
        In-cluster mode would give k9s the pod's cluster-admin service account. That's a broader
        blast radius than KubeGo's own VM CRUD, so the panel is disabled by default.
      </p>
    </div>

    <!-- Loading -->
    <div v-else-if="loading" class="p-6 flex items-center gap-2 text-[var(--text-secondary)]">
      <Loader2 class="w-4 h-4 animate-spin" />
      Checking k9s availability…
    </div>

    <!-- Not installed -->
    <div
      v-else-if="!k9sAvailable"
      class="p-6 flex flex-col items-center justify-center h-full gap-4 text-[var(--text-secondary)]"
    >
      <TerminalIcon class="w-12 h-12 text-[var(--muted)]" />
      <p class="text-lg">k9s is not installed on the server</p>
      <p class="text-sm max-w-md text-center">
        k9s is an Apache-2.0 licensed terminal UI for Kubernetes. Click below to install the latest
        release from the upstream GitHub; requires write access to
        <code class="font-mono">/usr/local/bin</code> (the server must be running as root, or
        <code class="font-mono">sudo</code> must be available).
      </p>
      <button
        class="flex items-center gap-2 mt-2 px-4 py-2 text-sm rounded bg-green-900/30 hover:bg-[var(--success)] text-green-300 hover:text-white transition-colors"
        @click="doInstall"
      >
        <Download class="w-4 h-4" />
        Install k9s
      </button>
    </div>

    <!-- No active cluster -->
    <div
      v-else-if="noActiveCluster"
      class="p-6 flex flex-col items-center justify-center h-full gap-3 text-[var(--text-secondary)]"
    >
      <AlertTriangle class="w-12 h-12 text-[var(--warning)]" />
      <p class="text-lg">No active cluster</p>
      <p class="text-sm">Create or select a cluster from the sidebar switcher to launch k9s.</p>
    </div>

    <!-- Ready — tabbed k9s sessions -->
    <div v-else class="flex flex-col h-full">
      <!-- Tab bar -->
      <div class="flex items-center bg-[var(--bg-surface)] border-b border-[var(--border)] overflow-x-auto">
        <button
          v-for="tab in tabs"
          :key="tab.sessionId"
          class="group flex items-center gap-1.5 px-3 py-1.5 text-xs border-r border-[var(--border)] whitespace-nowrap transition-colors"
          :class="tab.sessionId === activeSessionId
            ? 'bg-[var(--bg-primary)] text-[var(--text-primary)]'
            : 'bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'"
          @click="switchTab(tab.sessionId)"
        >
          <span>{{ tab.label }}</span>
          <X
            class="w-3 h-3 opacity-0 group-hover:opacity-60 hover:!opacity-100 transition-opacity cursor-pointer"
            @click.stop="closeTab(tab.sessionId)"
          />
        </button>
        <button
          class="flex items-center px-2 py-1.5 text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-hover)] transition-colors"
          title="New k9s session"
          @click="addTab"
        >
          <Plus class="w-3.5 h-3.5" />
        </button>
        <div class="flex-1" />
        <div class="px-3 py-1 text-xs text-[var(--muted)] flex-shrink-0">
          {{ store.activeContext }}
        </div>
      </div>

      <!-- Empty state -->
      <div
        v-if="tabs.length === 0"
        class="flex-1 flex flex-col items-center justify-center gap-3 text-[var(--text-secondary)]"
      >
        <TerminalIcon class="w-12 h-12 text-[var(--muted)]" />
        <p class="text-sm">No active k9s sessions.</p>
        <button
          class="flex items-center gap-2 px-4 py-2 text-sm rounded bg-[var(--bg-hover)] hover:bg-[var(--accent)] transition-colors"
          @click="addTab"
        >
          <Plus class="w-4 h-4" />
          Start k9s
        </button>
      </div>

      <!-- Terminals (all mounted, only active visible) -->
      <div v-else class="flex-1 relative">
        <K9sTerminal
          v-for="tab in tabs"
          :key="tab.sessionId"
          :sessionId="tab.sessionId"
          :active="tab.sessionId === activeSessionId"
          class="absolute inset-0"
          :class="{ 'invisible': tab.sessionId !== activeSessionId }"
        />
      </div>
    </div>

    <KindProgressModal
      v-if="installProgress"
      :title="installProgress.title"
      :status="installProgress.status"
      :lines="installProgress.lines"
      :error-message="installProgress.errorMessage"
      @close="installProgress = null"
    />
  </div>
</template>
