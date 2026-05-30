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

const images = computed(() => data.value?.images || [])
const filteredImages = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return images.value
  return images.value.filter(img =>
    img.reference.toLowerCase().includes(q) ||
    (img.id || '').toLowerCase().includes(q)
  )
})

async function loadCache() {
  loading.value = true
  error.value = ''
  try {
    data.value = await api.listKindImageCache()
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function loadImage(image) {
  loadingImage.value = image.reference
  progressLines.value = []
  try {
    await api.loadKindDockerImage(image.reference, (ev) => {
      if (ev.type === 'output') progressLines.value.push(ev.line)
    })
    toasts.success(`Loaded ${image.reference}`)
    await loadCache()
  } catch (e) {
    toasts.error(e.message)
    progressLines.value.push(`ERROR: ${e.message}`)
  } finally {
    loadingImage.value = ''
  }
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

      <div v-if="progressLines.length" class="mb-4 rounded border border-[var(--border)] bg-[var(--bg-primary)] p-3 max-h-44 overflow-auto">
        <pre class="text-xs whitespace-pre-wrap text-[var(--text-secondary)]">{{ progressLines.join('\n') }}</pre>
      </div>

      <div class="bg-[var(--bg-surface)] rounded-lg border border-[var(--border)] overflow-hidden">
        <table class="w-full text-sm">
          <thead class="text-xs text-[var(--muted)] bg-[var(--bg-primary)]">
            <tr>
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
              <td colspan="4" class="px-4 py-8 text-center text-sm text-[var(--muted)]">No Docker images match the filter.</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </div>
</template>
