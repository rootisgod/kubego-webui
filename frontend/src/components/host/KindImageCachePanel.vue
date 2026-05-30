<script setup>
import { computed, onMounted, ref } from 'vue'
import { RefreshCw, Loader2, Download, CheckCircle2, AlertTriangle, Search } from 'lucide-vue-next'
import * as api from '../../api/client.js'
import { useToastStore } from '../../stores/toastStore.js'

const toasts = useToastStore()
const loading = ref(false)
const loadingImage = ref('')
const error = ref('')
const query = ref('')
const data = ref(null)
const progressLines = ref([])
const selected = ref(new Set())
const batch = ref(null)

const images = computed(() => data.value?.images || [])
const filteredImages = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return images.value
  return images.value.filter(img =>
    img.reference.toLowerCase().includes(q) ||
    (img.id || '').toLowerCase().includes(q)
  )
})
const loadableImages = computed(() => filteredImages.value.filter(img => !img.loaded))
const selectedImages = computed(() => images.value.filter(img => selected.value.has(img.reference) && !img.loaded))
const allLoadableSelected = computed(() => loadableImages.value.length > 0 && loadableImages.value.every(img => selected.value.has(img.reference)))
const selectedCount = computed(() => selectedImages.value.length)
const batchPercent = computed(() => {
  if (!batch.value?.total) return 0
  return Math.round((batch.value.done / batch.value.total) * 100)
})

async function loadCache() {
  loading.value = true
  error.value = ''
  try {
    data.value = await api.listKindImageCache()
    const refs = new Set(data.value.images?.map(img => img.reference) || [])
    selected.value = new Set([...selected.value].filter(ref => refs.has(ref)))
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

function toggleImage(image) {
  const next = new Set(selected.value)
  if (next.has(image.reference)) next.delete(image.reference)
  else next.add(image.reference)
  selected.value = next
}

function toggleAllFiltered() {
  const next = new Set(selected.value)
  if (allLoadableSelected.value) {
    for (const img of loadableImages.value) next.delete(img.reference)
  } else {
    for (const img of loadableImages.value) next.add(img.reference)
  }
  selected.value = next
}

async function loadOne(image, refresh = true) {
  loadingImage.value = image.reference
  progressLines.value = []
  try {
    await api.loadKindDockerImage(image.reference, (ev) => {
      if (ev.type === 'output') progressLines.value.push(ev.line)
    })
    if (refresh) {
      toasts.success(`Loaded ${image.reference}`)
      await loadCache()
    }
  } catch (e) {
    if (refresh) toasts.error(e.message)
    progressLines.value.push(`ERROR: ${e.message}`)
    throw e
  } finally {
    loadingImage.value = ''
  }
}

async function loadImage(image) {
  await loadOne(image)
}

async function loadSelected() {
  const queue = [...selectedImages.value]
  if (queue.length === 0) return
  batch.value = { total: queue.length, done: 0, current: '', errors: [] }
  progressLines.value = []
  try {
    for (const image of queue) {
      batch.value.current = image.reference
      try {
        await loadOne(image, false)
      } catch (e) {
        batch.value.errors.push(`${image.reference}: ${e.message}`)
      } finally {
        batch.value.done += 1
      }
    }
    batch.value.current = 'Complete'
    if (batch.value.errors.length) {
      toasts.error(`${batch.value.errors.length} of ${batch.value.total} image loads failed`)
    } else {
      toasts.success(`Loaded ${batch.value.total} image${batch.value.total !== 1 ? 's' : ''}`)
    }
    selected.value = new Set()
    await loadCache()
  } finally {
    loadingImage.value = ''
  }
}

function closeBatch() {
  if (batch.value && batch.value.done >= batch.value.total) batch.value = null
}

onMounted(loadCache)
</script>

<template>
  <div class="p-6">
    <div class="flex items-center justify-between gap-4 mb-6">
      <div>
        <h2 class="text-xl font-semibold">KinD Image Cache</h2>
        <p class="text-xs text-[var(--muted)] mt-1">
          Load host Docker images into the active KinD cluster's container runtime.
        </p>
      </div>
      <button
        class="flex items-center gap-1.5 px-3 py-1.5 text-sm rounded bg-[var(--bg-hover)] hover:bg-[var(--border)] transition-colors"
        :disabled="loading"
        @click="loadCache"
      >
        <RefreshCw class="w-3.5 h-3.5" :class="{ 'animate-spin': loading }" />
        Refresh
      </button>
    </div>

    <div v-if="error" class="mb-4 px-4 py-3 rounded bg-red-900/20 border border-red-800/30 text-sm text-[var(--danger)]">
      {{ error }}
    </div>

    <div v-if="loading && !data" class="flex items-center gap-2 text-[var(--text-secondary)]">
      <Loader2 class="w-4 h-4 animate-spin" />
      Reading Docker and KinD image caches...
    </div>

    <template v-if="data">
      <div class="mb-4 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div class="text-sm text-[var(--text-secondary)]">
          Active cluster: <code class="text-[var(--text-primary)]">{{ data.context }}</code>
          <span class="mx-2 text-[var(--muted)]">|</span>
          Nodes: <span class="text-[var(--text-primary)]">{{ data.nodes?.length || 0 }}</span>
        </div>
        <label class="relative block sm:w-80">
          <Search class="absolute left-2.5 top-2.5 w-3.5 h-3.5 text-[var(--muted)]" />
          <input
            v-model="query"
            type="search"
            placeholder="Filter images"
            class="w-full pl-8 pr-3 py-2 text-sm rounded bg-[var(--bg-surface)] border border-[var(--border)] focus:border-[var(--accent)] focus:outline-none"
          />
        </label>
      </div>

      <div class="mb-4 flex items-center justify-between gap-3 rounded border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-2">
        <label class="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            class="rounded border-[var(--border)]"
            :checked="allLoadableSelected"
            :disabled="loadableImages.length === 0 || !!loadingImage"
            @change="toggleAllFiltered"
          />
          Select all visible missing images
        </label>
        <button
          class="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm rounded bg-[var(--accent)] hover:bg-blue-600 transition-colors disabled:opacity-40"
          :disabled="selectedCount === 0 || !!loadingImage"
          @click="loadSelected"
        >
          <Download class="w-3.5 h-3.5" />
          Load selected ({{ selectedCount }})
        </button>
      </div>

      <div v-if="progressLines.length" class="mb-4 rounded border border-[var(--border)] bg-[var(--bg-primary)] p-3 max-h-44 overflow-auto">
        <pre class="text-xs whitespace-pre-wrap text-[var(--text-secondary)]">{{ progressLines.join('\n') }}</pre>
      </div>

      <div class="bg-[var(--bg-surface)] rounded-lg border border-[var(--border)] overflow-hidden">
        <table class="w-full text-sm">
          <thead class="text-xs text-[var(--muted)] bg-[var(--bg-primary)]">
            <tr>
              <th class="text-left px-4 py-2 font-normal w-10"></th>
              <th class="text-left px-4 py-2 font-normal">Docker image</th>
              <th class="text-left px-4 py-2 font-normal w-32">Size</th>
              <th class="text-left px-4 py-2 font-normal w-40">KinD status</th>
              <th class="text-right px-4 py-2 font-normal w-32">Action</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="img in filteredImages"
              :key="img.reference"
              class="border-t border-[var(--border)]"
            >
              <td class="px-4 py-2 align-top">
                <input
                  type="checkbox"
                  class="rounded border-[var(--border)]"
                  :checked="selected.has(img.reference)"
                  :disabled="img.loaded || !!loadingImage"
                  @change="toggleImage(img)"
                />
              </td>
              <td class="px-4 py-2">
                <div class="font-mono text-xs text-[var(--text-primary)] break-all">{{ img.reference }}</div>
                <div class="text-[11px] text-[var(--muted)] mt-1">{{ img.id }} · {{ img.created_since }}</div>
              </td>
              <td class="px-4 py-2 font-mono text-xs text-[var(--text-secondary)]">{{ img.size || '-' }}</td>
              <td class="px-4 py-2">
                <span v-if="img.loaded" class="inline-flex items-center gap-1 text-xs text-[var(--success)]">
                  <CheckCircle2 class="w-3.5 h-3.5" /> Loaded
                </span>
                <span v-else class="inline-flex items-center gap-1 text-xs text-[var(--warning)]">
                  <AlertTriangle class="w-3.5 h-3.5" /> Missing {{ img.missing_nodes?.length || 0 }}/{{ data.nodes?.length || 0 }}
                </span>
                <div v-if="!img.loaded && img.missing_nodes?.length" class="text-[11px] text-[var(--muted)] mt-1 truncate" :title="img.missing_nodes.join(', ')">
                  {{ img.missing_nodes.join(', ') }}
                </div>
              </td>
              <td class="px-4 py-2 text-right">
                <button
                  class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded bg-[var(--accent)] hover:bg-blue-600 transition-colors disabled:opacity-40"
                  :disabled="!!loadingImage || img.loaded"
                  @click="loadImage(img)"
                >
                  <Loader2 v-if="loadingImage === img.reference" class="w-3.5 h-3.5 animate-spin" />
                  <Download v-else class="w-3.5 h-3.5" />
                  {{ img.loaded ? 'Loaded' : 'Load' }}
                </button>
              </td>
            </tr>
            <tr v-if="filteredImages.length === 0">
              <td colspan="5" class="px-4 py-8 text-center text-sm text-[var(--muted)]">No Docker images match the filter.</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <Teleport to="body">
      <div v-if="batch" class="fixed inset-0 z-50 flex items-center justify-center">
        <div class="absolute inset-0 bg-black/60 backdrop-blur-sm" />
        <div class="relative w-full max-w-xl mx-4 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-5 shadow-2xl">
          <div class="flex items-start justify-between gap-4 mb-4">
            <div>
              <h3 class="text-sm font-medium">Loading images into KinD</h3>
              <p class="text-xs text-[var(--muted)] mt-1">{{ batch.done }} of {{ batch.total }} complete</p>
            </div>
            <button
              class="px-3 py-1.5 text-xs rounded bg-[var(--bg-hover)] hover:bg-[var(--border)] transition-colors disabled:opacity-40"
              :disabled="batch.done < batch.total"
              @click="closeBatch"
            >Close</button>
          </div>

          <div class="h-2 rounded bg-[var(--bg-primary)] overflow-hidden mb-3">
            <div class="h-full bg-[var(--accent)] transition-all" :style="{ width: `${batchPercent}%` }" />
          </div>
          <div class="flex items-center gap-2 text-xs text-[var(--text-secondary)] mb-4">
            <Loader2 v-if="batch.done < batch.total" class="w-3.5 h-3.5 animate-spin" />
            <CheckCircle2 v-else class="w-3.5 h-3.5 text-[var(--success)]" />
            <span class="truncate">{{ batch.current || 'Complete' }}</span>
          </div>

          <div v-if="progressLines.length" class="rounded border border-[var(--border)] bg-[var(--bg-primary)] p-3 max-h-52 overflow-auto">
            <pre class="text-xs whitespace-pre-wrap text-[var(--text-secondary)]">{{ progressLines.join('\n') }}</pre>
          </div>
          <div v-if="batch.errors.length" class="mt-3 text-xs text-[var(--danger)]">
            {{ batch.errors.length }} image{{ batch.errors.length !== 1 ? 's' : '' }} failed. See output above.
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
