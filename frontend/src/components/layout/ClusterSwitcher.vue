<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useVmStore } from '../../stores/vmStore.js'
import { useToastStore } from '../../stores/toastStore.js'
import * as api from '../../api/client.js'
import { ChevronDown, Plus, Trash2, Check, Server } from 'lucide-vue-next'
import ConfirmModal from '../modals/ConfirmModal.vue'
import KindCreateModal from '../modals/KindCreateModal.vue'
import KindProgressModal from '../modals/KindProgressModal.vue'

const store = useVmStore()
const toasts = useToastStore()

const open = ref(false)
const rootRef = ref(null)
const createPrompt = ref(false)
const confirmDelete = ref(null) // { name }
const progress = ref(null)      // { title, status, lines, errorMessage }

const activeLabel = computed(() => store.activeContext || 'no cluster')
const canManageKind = computed(() => store.kindAvailable && !store.inClusterMode)

function close() { open.value = false }

function onDocClick(e) {
  if (!rootRef.value) return
  if (!rootRef.value.contains(e.target)) close()
}

onMounted(() => document.addEventListener('click', onDocClick))
onBeforeUnmount(() => document.removeEventListener('click', onDocClick))

async function switchTo(ctx) {
  close()
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
  close()
  createPrompt.value = true
}

async function doCreateKind(name) {
  createPrompt.value = false
  progress.value = {
    title: `Creating KinD cluster "${name}"...`,
    status: 'running',
    lines: [],
    errorMessage: '',
  }
  try {
    await api.createKindCluster(name, (ev) => {
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

function askDeleteKind(ctx) {
  close()
  // The kind cluster name is the context minus the "kind-" prefix.
  const name = ctx.startsWith('kind-') ? ctx.slice(5) : ctx
  confirmDelete.value = { context: ctx, name }
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
</script>

<template>
  <div ref="rootRef" class="relative">
    <button
      class="w-full flex items-center gap-2 px-2 py-1.5 rounded hover:bg-[var(--bg-hover)] transition-colors text-left"
      @click.stop="open = !open"
    >
      <Server class="w-4 h-4 text-[var(--accent)] flex-shrink-0" />
      <div class="flex-1 min-w-0">
        <div class="text-[10px] uppercase tracking-wide text-[var(--muted)]">Cluster</div>
        <div class="text-xs truncate">{{ activeLabel }}</div>
      </div>
      <ChevronDown class="w-3.5 h-3.5 flex-shrink-0 text-[var(--muted)]" :class="open ? 'rotate-180' : ''" />
    </button>

    <div
      v-if="open"
      class="absolute left-0 right-0 top-full mt-1 z-30 bg-[var(--bg-surface)] border border-[var(--border)] rounded shadow-lg max-h-96 overflow-y-auto"
    >
      <div v-if="store.clusters.length === 0" class="px-3 py-2 text-xs text-[var(--muted)] italic">
        No contexts found
      </div>
      <div
        v-for="c in store.clusters"
        :key="c.context"
        class="flex items-center gap-2 px-2 py-1.5 hover:bg-[var(--bg-hover)] cursor-pointer text-xs"
        @click="switchTo(c.context)"
      >
        <Check v-if="c.current" class="w-3.5 h-3.5 text-[var(--success)] flex-shrink-0" />
        <span v-else class="w-3.5 h-3.5 flex-shrink-0" />
        <span class="truncate flex-1">{{ c.context }}</span>
        <button
          v-if="c.is_kind && canManageKind"
          class="w-5 h-5 flex items-center justify-center rounded hover:bg-[var(--danger)]/30 text-[var(--muted)] hover:text-[var(--danger)] transition-colors"
          title="Delete KinD cluster"
          @click.stop="askDeleteKind(c.context)"
        >
          <Trash2 class="w-3 h-3" />
        </button>
      </div>
      <div
        v-if="canManageKind"
        class="border-t border-[var(--border)] flex items-center gap-2 px-2 py-1.5 text-xs cursor-pointer hover:bg-[var(--bg-hover)] text-[var(--accent)]"
        @click="askCreateKind"
      >
        <Plus class="w-3.5 h-3.5" />
        <span>New KinD cluster...</span>
      </div>
      <div
        v-else-if="!store.inClusterMode"
        class="border-t border-[var(--border)] px-2 py-1.5 text-[10px] text-[var(--muted)]"
      >
        kind CLI not on server PATH
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
  </div>
</template>
