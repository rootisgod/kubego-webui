<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useToastStore } from '../../stores/toastStore.js'
import * as api from '../../api/client.js'
import { Disc, Upload, Trash2, RefreshCw, Loader2, CheckCircle2, AlertCircle, HelpCircle, Link2 } from 'lucide-vue-next'

const toasts = useToastStore()

const uploads = ref([])
const loading = ref(false)

// Upload-in-progress state (one at a time — a second upload while the
// first is running would conflict at the CDI layer anyway).
const uploading = ref(false)
const uploadName = ref('')
const uploadKind = ref('iso')
const uploadSizeGB = ref(10)
const uploadProgress = ref({ loaded: 0, total: 0 })
const fileInput = ref(null)
const dragActive = ref(false)

// "file" = browser → CDI uploadproxy (streamed via this server). "url"
// = CDI's own importer pod pulls the URL — no bytes go through us.
const sourceMode = ref('file')
const importURL = ref('')
const importing = ref(false)

let pollTimer = null

async function refresh() {
  loading.value = true
  try {
    const data = await api.listImageUploads()
    uploads.value = Array.isArray(data) ? data : []
  } catch (e) {
    toasts.error('Failed to load images: ' + e.message)
  } finally {
    loading.value = false
  }
}

function pickFile() {
  fileInput.value?.click()
}

function onFileChosen(ev) {
  const file = ev.target.files?.[0]
  if (file) startUpload(file)
  ev.target.value = ''
}

function onDrop(ev) {
  dragActive.value = false
  const file = ev.dataTransfer?.files?.[0]
  if (file) startUpload(file)
}

function looksLikeISO(name) {
  return /\.iso$/i.test(name)
}

async function startUpload(file) {
  if (uploading.value) {
    toasts.error('An upload is already running — wait for it to finish')
    return
  }
  const displayName = uploadName.value || file.name
  const kind = uploadKind.value || (looksLikeISO(file.name) ? 'iso' : 'disk')

  // Auto-size the DV to the next-GB-up from the file size plus a 1 GB
  // cushion. CDI needs headroom for its overhead layer, and the UI no
  // longer asks the user — they don't care what PVC size backs the ISO.
  const fileGB = Math.ceil(file.size / 1024 / 1024 / 1024)
  const sizeGB = Math.max(fileGB + 1, 1)

  uploading.value = true
  uploadProgress.value = { loaded: 0, total: file.size }

  try {
    const created = await api.createImageUpload({
      name: displayName,
      kind,
      size_gb: sizeGB,
    })
    await api.uploadImageData(created.pvc_name, file, (loaded, total) => {
      uploadProgress.value = { loaded, total }
    })
    toasts.success(`Uploaded ${displayName}`)
    await refresh()
  } catch (e) {
    toasts.error('Upload failed: ' + e.message)
  } finally {
    uploading.value = false
    uploadName.value = ''
  }
}

function looksLikeISOURL(url) {
  try {
    const u = new URL(url)
    return /\.iso(\?|#|$)/i.test(u.pathname)
  } catch {
    return false
  }
}

async function startImport() {
  const url = importURL.value.trim()
  if (!url) {
    toasts.error('Enter a URL to import')
    return
  }
  if (!/^https?:\/\//i.test(url)) {
    toasts.error('URL must start with http:// or https://')
    return
  }
  let displayName = uploadName.value.trim()
  if (!displayName) {
    try {
      const u = new URL(url)
      const last = u.pathname.split('/').filter(Boolean).pop() || u.hostname
      displayName = decodeURIComponent(last)
    } catch {
      displayName = url
    }
  }
  const kind = uploadKind.value || (looksLikeISOURL(url) ? 'iso' : 'disk')
  const sizeGB = Math.max(uploadSizeGB.value, 1)

  importing.value = true
  try {
    await api.createImageImport({
      name: displayName,
      kind,
      size_gb: sizeGB,
      url,
    })
    toasts.success(`Import started for ${displayName} — CDI is pulling in the background`)
    importURL.value = ''
    uploadName.value = ''
    await refresh()
  } catch (e) {
    toasts.error('Import failed: ' + e.message)
  } finally {
    importing.value = false
  }
}

async function deleteUpload(pvcName) {
  if (!confirm(`Delete image "${pvcName}"? This frees the PVC.`)) return
  try {
    await api.deleteImageUpload(pvcName)
    toasts.success('Image deleted')
    await refresh()
  } catch (e) {
    toasts.error(e.message)
  }
}

function statusIcon(phase) {
  if (phase === 'Succeeded') return CheckCircle2
  if (phase === 'Failed') return AlertCircle
  if (phase === 'UploadReady' || phase === 'UploadScheduled') return HelpCircle
  return Loader2
}

function humanSize(bytes) {
  if (!bytes && bytes !== 0) return ''
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let n = bytes
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${n.toFixed(n >= 10 || i === 0 ? 0 : 1)} ${units[i]}`
}

onMounted(() => {
  refresh()
  // Poll the phase of in-flight uploads (CDI flips to Succeeded after
  // the proxy finalises the write — can lag a second or two behind the
  // XHR completion).
  pollTimer = setInterval(refresh, 5000)
})
onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<template>
  <div class="h-full flex flex-col p-6 overflow-auto">
    <div class="flex items-center justify-between mb-4">
      <div class="flex items-center gap-2">
        <Disc class="w-5 h-5 text-[var(--accent)]" />
        <h2 class="text-lg font-semibold">Images</h2>
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

    <p class="text-xs text-[var(--text-secondary)] mb-4">
      Upload any ISO or qcow2 / raw disk image — Linux installers, Windows ISOs, virtio-win, cloud images. Boot a VM from one of these from the VM create panels.
    </p>

    <!-- Source mode tabs -->
    <div class="flex gap-1 mb-2">
      <button
        @click="sourceMode = 'file'"
        :class="sourceMode === 'file'
          ? 'bg-[var(--bg-surface)] border-[var(--accent)] text-[var(--text-primary)]'
          : 'bg-transparent border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]'"
        class="flex items-center gap-1.5 px-3 py-1 text-xs rounded-t border border-b-0 transition-colors"
      >
        <Upload class="w-3.5 h-3.5" />
        Upload file
      </button>
      <button
        @click="sourceMode = 'url'"
        :class="sourceMode === 'url'
          ? 'bg-[var(--bg-surface)] border-[var(--accent)] text-[var(--text-primary)]'
          : 'bg-transparent border-[var(--border)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]'"
        class="flex items-center gap-1.5 px-3 py-1 text-xs rounded-t border border-b-0 transition-colors"
      >
        <Link2 class="w-3.5 h-3.5" />
        Import from URL
      </button>
    </div>

    <!-- Upload form -->
    <div
      v-if="sourceMode === 'file'"
      class="p-4 rounded border-2 border-dashed transition-colors"
      :class="dragActive ? 'border-[var(--accent)] bg-[var(--accent)]/10' : 'border-[var(--border)] bg-[var(--bg-surface)]'"
      @dragover.prevent="dragActive = true"
      @dragleave="dragActive = false"
      @drop.prevent="onDrop"
    >
      <div class="grid grid-cols-[2fr_1fr_auto] gap-3 items-end">
        <div>
          <label class="block text-[11px] text-[var(--text-secondary)] mb-1">Display name (defaults to filename)</label>
          <input
            v-model="uploadName"
            type="text"
            placeholder="Ubuntu 24.04 server"
            :disabled="uploading"
            class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-2 py-1.5 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)] disabled:opacity-50"
          />
        </div>
        <div>
          <label class="block text-[11px] text-[var(--text-secondary)] mb-1">Kind</label>
          <select
            v-model="uploadKind"
            :disabled="uploading"
            class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-2 py-1.5 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)] disabled:opacity-50"
          >
            <option value="iso">ISO (bootable)</option>
            <option value="disk">Disk (qcow2 / raw)</option>
          </select>
        </div>
        <button
          @click="pickFile"
          :disabled="uploading"
          class="flex items-center gap-1.5 px-3 py-1.5 text-sm rounded bg-[var(--accent)] text-white hover:bg-blue-600 transition-colors disabled:opacity-40"
        >
          <Loader2 v-if="uploading" class="w-3.5 h-3.5 animate-spin" />
          <Upload v-else class="w-3.5 h-3.5" />
          {{ uploading ? 'Uploading…' : 'Choose file' }}
        </button>
      </div>
      <input ref="fileInput" type="file" class="hidden" @change="onFileChosen" />
      <p class="text-[11px] text-[var(--text-secondary)] mt-2">
        …or drag-and-drop a file anywhere in this box. PVC size is auto-derived from the file.
      </p>

      <!-- Progress bar -->
      <div v-if="uploading" class="mt-3">
        <div class="h-2 bg-[var(--bg-primary)] rounded overflow-hidden">
          <div
            class="h-full bg-[var(--accent)] transition-all"
            :style="{ width: uploadProgress.total ? `${(uploadProgress.loaded / uploadProgress.total * 100).toFixed(1)}%` : '0%' }"
          />
        </div>
        <p class="text-[11px] text-[var(--text-secondary)] mt-1">
          {{ humanSize(uploadProgress.loaded) }} / {{ humanSize(uploadProgress.total) }}
          ({{ uploadProgress.total ? ((uploadProgress.loaded / uploadProgress.total * 100).toFixed(1)) : 0 }}%)
        </p>
      </div>
    </div>

    <!-- URL import form -->
    <div
      v-else
      class="p-4 rounded border border-[var(--border)] bg-[var(--bg-surface)]"
    >
      <div class="grid grid-cols-[2fr_1fr_1fr_auto] gap-3 items-end">
        <div>
          <label class="block text-[11px] text-[var(--text-secondary)] mb-1">Source URL (http / https)</label>
          <input
            v-model="importURL"
            type="url"
            placeholder="https://releases.ubuntu.com/24.04/ubuntu-24.04-live-server-amd64.iso"
            :disabled="importing"
            class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-2 py-1.5 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)] disabled:opacity-50"
          />
        </div>
        <div>
          <label class="block text-[11px] text-[var(--text-secondary)] mb-1">Kind</label>
          <select
            v-model="uploadKind"
            :disabled="importing"
            class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-2 py-1.5 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)] disabled:opacity-50"
          >
            <option value="iso">ISO (bootable)</option>
            <option value="disk">Disk (qcow2 / raw)</option>
          </select>
        </div>
        <div>
          <label class="block text-[11px] text-[var(--text-secondary)] mb-1">PVC size (GiB)</label>
          <input
            v-model.number="uploadSizeGB"
            type="number"
            min="1"
            max="1024"
            :disabled="importing"
            class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-2 py-1.5 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)] disabled:opacity-50"
          />
        </div>
        <button
          @click="startImport"
          :disabled="importing || !importURL"
          class="flex items-center gap-1.5 px-3 py-1.5 text-sm rounded bg-[var(--accent)] text-white hover:bg-blue-600 transition-colors disabled:opacity-40"
        >
          <Loader2 v-if="importing" class="w-3.5 h-3.5 animate-spin" />
          <Link2 v-else class="w-3.5 h-3.5" />
          {{ importing ? 'Starting…' : 'Import' }}
        </button>
      </div>
      <div class="mt-2">
        <label class="block text-[11px] text-[var(--text-secondary)] mb-1">Display name (optional — derived from URL filename)</label>
        <input
          v-model="uploadName"
          type="text"
          placeholder="Ubuntu 24.04 server"
          :disabled="importing"
          class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-2 py-1.5 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)] disabled:opacity-50"
        />
      </div>
      <p class="text-[11px] text-[var(--text-secondary)] mt-2">
        CDI's importer pod fetches the URL directly — no bytes flow through your browser. Size the PVC generously: the import fails if the payload doesn't fit.
      </p>
    </div>

    <!-- Images list -->
    <div class="mt-6 space-y-2">
      <div
        v-for="u in uploads"
        :key="u.pvc_name"
        class="p-3 rounded bg-[var(--bg-surface)] border border-[var(--border)]"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="flex items-center gap-2 min-w-0">
            <component
              :is="statusIcon(u.phase)"
              class="w-4 h-4 flex-shrink-0"
              :class="{
                'text-[var(--success)]': u.phase === 'Succeeded',
                'text-red-400': u.phase === 'Failed',
                'text-[var(--accent)] animate-spin': !['Succeeded', 'Failed', 'UploadReady', 'UploadScheduled'].includes(u.phase),
                'text-[var(--text-secondary)]': ['UploadReady', 'UploadScheduled'].includes(u.phase),
              }"
            />
            <div class="min-w-0">
              <div class="text-sm font-medium text-[var(--text-primary)] truncate">{{ u.name }}</div>
              <div class="text-xs text-[var(--text-secondary)]">
                {{ u.kind }} · {{ u.size }} · {{ u.phase || 'Unknown' }}
              </div>
              <div class="text-[10px] text-[var(--muted)] font-mono truncate">PVC: {{ u.pvc_name }}</div>
            </div>
          </div>
          <button
            @click="deleteUpload(u.pvc_name)"
            class="p-1.5 rounded text-[var(--text-secondary)] hover:text-red-400 hover:bg-[var(--bg-hover)] transition-colors"
            title="Delete image"
          >
            <Trash2 class="w-3.5 h-3.5" />
          </button>
        </div>
      </div>
      <div v-if="uploads.length === 0 && !loading" class="text-sm text-center text-[var(--text-secondary)] py-8">
        No images uploaded yet.
      </div>
    </div>
  </div>
</template>
