<script setup>
import { ref, computed } from 'vue'
import { useVmStore } from '../../stores/vmStore.js'
import { useToastStore } from '../../stores/toastStore.js'
import * as api from '../../api/client.js'
import { Plus, Trash2, Settings as SettingsIcon } from 'lucide-vue-next'
import ConfirmModal from '../modals/ConfirmModal.vue'
import KindCreateModal from '../modals/KindCreateModal.vue'
import KindProgressModal from '../modals/KindProgressModal.vue'
import ClusterEditModal from '../modals/ClusterEditModal.vue'

const store = useVmStore()
const toasts = useToastStore()

const createPrompt = ref(false)
const confirmDelete = ref(null) // { context, name }
const progress = ref(null)      // { title, status, lines, errorMessage }
const editing = ref(null)       // cluster object being edited

const canManageKind = computed(() => store.kindAvailable && !store.inClusterMode)

// Strip the `kind-` prefix for display — context names get noisy fast
// when half your list looks like `kind-dev / kind-staging / kind-prod`.
// Hover/aria still carries the full context name.
function displayName(c) {
  return c.context?.startsWith('kind-') ? c.context.slice(5) : c.context
}

async function switchTo(ctx) {
  if (ctx === store.activeContext) return
  try {
    await api.selectCluster(ctx)
    toasts.success(`Switched to ${ctx}`)
    await store.onClusterChanged()
  } catch (e) {
    toasts.error(e.message)
  }
}

function askCreateKind() {
  createPrompt.value = true
}

async function doCreateKind(opts) {
  createPrompt.value = false
  const name = opts.name
  progress.value = {
    title: `Creating KinD cluster "${name}"...`,
    status: 'running',
    lines: [],
    errorMessage: '',
  }
  try {
    await api.createKindCluster(opts, (ev) => {
      if (ev.type === 'output') progress.value.lines.push(ev.line)
    })
    progress.value.status = 'done'
    toasts.success(`Cluster "kind-${name}" created and activated`)
    await store.onClusterChanged()
  } catch (e) {
    progress.value.status = 'error'
    progress.value.errorMessage = e.message
  }
}

function askDeleteKind(c) {
  const name = c.context.startsWith('kind-') ? c.context.slice(5) : c.context
  confirmDelete.value = { context: c.context, name }
}

async function doDeleteKind() {
  const { context, name } = confirmDelete.value
  confirmDelete.value = null
  progress.value = {
    title: `Deleting KinD cluster "${context}"...`,
    status: 'running',
    lines: [],
    errorMessage: '',
  }
  try {
    await api.deleteKindCluster(name, (ev) => {
      if (ev.type === 'output') progress.value.lines.push(ev.line)
    })
    progress.value.status = 'done'
    toasts.success(`Cluster "${context}" deleted`)
    await store.onClusterChanged()
  } catch (e) {
    progress.value.status = 'error'
    progress.value.errorMessage = e.message
  }
}

function openEdit(c) {
  editing.value = { context: c.context, tag: c.tag || '', color: c.color || '' }
}

async function saveEdit({ tag, color }) {
  const ctx = editing.value.context
  editing.value = null
  try {
    await api.setClusterMetadata(ctx, { tag, color })
    toasts.success(`Updated "${ctx}"`)
    await store.fetchClusters()
  } catch (e) {
    toasts.error(e.message)
  }
}
</script>

<template>
  <div class="px-1 pb-1">
    <div class="text-[10px] uppercase tracking-wide text-[var(--muted)] px-1 pb-1 flex items-center justify-between">
      <span>Clusters</span>
      <button
        v-if="canManageKind"
        class="p-0.5 rounded hover:bg-[var(--bg-hover)] text-[var(--muted)] hover:text-[var(--accent)] transition-colors"
        title="New KinD cluster"
        @click="askCreateKind"
      >
        <Plus class="w-3 h-3" />
      </button>
    </div>

    <!-- Empty state -->
    <div
      v-if="store.clusters.length === 0"
      class="px-1.5 py-2 text-xs text-[var(--muted)] italic"
    >
      No clusters. Click + to create one.
    </div>

    <!-- Tabstrip: vertical rather than horizontal because a 240px
         sidebar cramps 3 horizontal chips; the active-tab colour
         stripe gives the same "always visible which env I'm in"
         signal. -->
    <div v-else class="flex flex-col gap-0.5">
      <div
        v-for="c in store.clusters"
        :key="c.context"
        class="group relative flex items-center gap-1.5 px-1.5 py-1 rounded cursor-pointer transition-colors text-xs"
        :class="c.current
          ? 'bg-[var(--bg-hover)] text-[var(--text-primary)]'
          : 'hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]'"
        :style="c.current && c.color ? {
          borderLeft: `3px solid ${c.color}`,
          paddingLeft: '5px',
          backgroundColor: `${c.color}22`,
        } : (c.current ? { borderLeft: '3px solid var(--accent)', paddingLeft: '5px' } : {})"
        :title="c.context"
        @click="switchTo(c.context)"
      >
        <span
          class="w-2 h-2 rounded-full flex-shrink-0"
          :style="{ background: c.color || 'var(--muted)' }"
        />
        <span class="truncate flex-1">{{ displayName(c) }}</span>
        <span
          v-if="c.tag"
          class="px-1 py-0 text-[9px] uppercase rounded flex-shrink-0"
          :style="c.color
            ? { background: `${c.color}33`, color: c.color, border: `1px solid ${c.color}66` }
            : { background: 'var(--bg-surface)', color: 'var(--muted)' }"
        >{{ c.tag }}</span>
        <div class="opacity-0 group-hover:opacity-100 flex items-center gap-0.5 transition-opacity">
          <button
            class="w-4 h-4 flex items-center justify-center rounded hover:bg-[var(--accent)]/20 text-[var(--muted)] hover:text-[var(--accent)]"
            title="Tag and colour"
            @click.stop="openEdit(c)"
          >
            <SettingsIcon class="w-3 h-3" />
          </button>
          <button
            v-if="c.is_kind && canManageKind"
            class="w-4 h-4 flex items-center justify-center rounded hover:bg-[var(--danger)]/30 text-[var(--muted)] hover:text-[var(--danger)]"
            title="Delete KinD cluster"
            @click.stop="askDeleteKind(c)"
          >
            <Trash2 class="w-3 h-3" />
          </button>
        </div>
      </div>
    </div>

    <KindCreateModal
      v-if="createPrompt"
      @confirm="doCreateKind"
      @cancel="createPrompt = false"
    />
    <ConfirmModal
      v-if="confirmDelete"
      :message="`Delete KinD cluster '${confirmDelete.context}'? This tears down the containers and removes the context from your kubeconfig.`"
      @confirm="doDeleteKind"
      @cancel="confirmDelete = null"
    />
    <KindProgressModal
      v-if="progress"
      :title="progress.title"
      :status="progress.status"
      :lines="progress.lines"
      :error-message="progress.errorMessage"
      @close="progress = null"
    />
    <ClusterEditModal
      v-if="editing"
      :context="editing.context"
      :tag="editing.tag"
      :color="editing.color"
      @save="saveEdit"
      @cancel="editing = null"
    />
  </div>
</template>
