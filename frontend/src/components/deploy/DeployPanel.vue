<script setup>
import { computed, ref, watch } from 'vue'
import YAML from 'js-yaml'
import * as api from '../../api/client.js'
import { useToastStore } from '../../stores/toastStore.js'
import { useVmStore } from '../../stores/vmStore.js'
import { Rocket, Wand2, FileCode, Loader2, CheckCircle2, Copy, Search, Download, AlertTriangle } from 'lucide-vue-next'

const toasts = useToastStore()
const store = useVmStore()

const mode = ref('form')
const applying = ref(false)
const scanningImages = ref(false)
const preloadingImages = ref(false)
const applied = ref([])
const scannedImages = ref([])
const imageProgressLines = ref([])
const preloadBeforeApply = ref(true)
const yamlError = ref('')
const form = ref({
  name: 'web',
  namespace: '',
  image: 'nginx:latest',
  replicas: 1,
  port: 80,
  service: true,
})

const quickYaml = computed(() => {
  const name = cleanName(form.value.name) || 'web'
  const labels = { app: name }
  const docs = [{
    apiVersion: 'apps/v1',
    kind: 'Deployment',
    metadata: metadata(name),
    spec: {
      replicas: Number(form.value.replicas) || 1,
      selector: { matchLabels: labels },
      template: {
        metadata: { labels },
        spec: {
          containers: [{
            name,
            image: form.value.image || 'nginx:latest',
            ports: [{ containerPort: Number(form.value.port) || 80 }],
          }],
        },
      },
    },
  }]

  if (form.value.service) {
    docs.push({
      apiVersion: 'v1',
      kind: 'Service',
      metadata: metadata(name),
      spec: {
        selector: labels,
        ports: [{ port: Number(form.value.port) || 80, targetPort: Number(form.value.port) || 80 }],
      },
    })
  }

  return docs.map(doc => YAML.dump(doc, { noRefs: true, lineWidth: 120 })).join('---\n')
})

const rawYaml = ref(quickYaml.value)
const activeYaml = computed(() => mode.value === 'form' ? quickYaml.value : rawYaml.value)
const canPreloadImages = computed(() => store.activeContext?.startsWith('kind-') && scannedImages.value.length > 0 && !preloadingImages.value && !applying.value)

watch(mode, (next) => {
  if (next === 'yaml' && !rawYaml.value.trim()) rawYaml.value = quickYaml.value
})

watch(activeYaml, () => {
  scannedImages.value = []
  imageProgressLines.value = []
})

function metadata(name) {
  const meta = { name }
  const ns = form.value.namespace.trim()
  if (ns) meta.namespace = ns
  return meta
}

function cleanName(value) {
  return String(value || '').trim().toLowerCase().replace(/[^a-z0-9-]/g, '-').replace(/^-+|-+$/g, '')
}

function validateYaml() {
  yamlError.value = ''
  try {
    const docs = YAML.loadAll(activeYaml.value).filter(Boolean)
    if (!docs.length) yamlError.value = 'YAML must contain at least one resource.'
    for (const doc of docs) {
      if (!doc.apiVersion || !doc.kind || !doc.metadata?.name) {
        yamlError.value = 'Each resource needs apiVersion, kind, and metadata.name.'
        break
      }
    }
  } catch (e) {
    yamlError.value = e.message
  }
  return !yamlError.value
}

async function apply() {
  if (!validateYaml()) return
  applying.value = true
  applied.value = []
  try {
    if (preloadBeforeApply.value && store.activeContext?.startsWith('kind-')) {
      const images = await scanImagesFromManifest(false)
      if (images.length) {
        const failures = await preloadImageRefs(images)
        if (failures.length) {
          throw new Error('image preload failed; manifest was not applied')
        }
      }
    }
    const res = await api.applyManifest(activeYaml.value)
    applied.value = res.resources || []
    toasts.success(`Applied ${applied.value.length} resource${applied.value.length !== 1 ? 's' : ''}`)
    store.fetchVMs()
  } catch (e) {
    toasts.error(e.message)
  } finally {
    applying.value = false
  }
}

async function scanImages() {
  if (!validateYaml()) return
  scanningImages.value = true
  scannedImages.value = []
  imageProgressLines.value = []
  try {
    const images = await scanImagesFromManifest(true)
    if (images.length) {
      toasts.success(`Found ${images.length} image${images.length !== 1 ? 's' : ''}`)
    } else {
      toasts.info('No container images found')
    }
  } catch (e) {
    toasts.error(e.message)
  } finally {
    scanningImages.value = false
  }
}

async function scanImagesFromManifest(clearProgress) {
  if (clearProgress) imageProgressLines.value = []
  const res = await api.scanManifestImages(activeYaml.value)
  scannedImages.value = Array.isArray(res.images) ? res.images : []
  return scannedImages.value
}

async function preloadImages() {
  if (!scannedImages.value.length) return
  const failures = await preloadImageRefs(scannedImages.value)
  if (failures.length) {
    toasts.error(`${failures.length} image preload${failures.length !== 1 ? 's' : ''} failed`)
  } else {
    toasts.success(`Preloaded ${scannedImages.value.length} image${scannedImages.value.length !== 1 ? 's' : ''}`)
  }
}

async function preloadImageRefs(images) {
  preloadingImages.value = true
  imageProgressLines.value = []
  const failures = []
  try {
    for (const image of scannedImages.value) {
      imageProgressLines.value.push(`==> ${image}`)
      try {
        await api.loadKindDockerImage(image, (ev) => {
          if (ev.type === 'output') imageProgressLines.value.push(ev.line)
        })
      } catch (e) {
        failures.push(`${image}: ${e.message}`)
        imageProgressLines.value.push(`ERROR: ${e.message}`)
      }
    }
    return failures
  } finally {
    preloadingImages.value = false
  }
}

async function copyYaml() {
  await navigator.clipboard.writeText(activeYaml.value)
  toasts.success('YAML copied')
}
</script>

<template>
  <div class="p-6 h-full flex flex-col gap-5">
    <div class="flex items-center justify-between gap-4">
      <div>
        <h2 class="text-xl font-semibold">Deploy</h2>
        <p class="text-sm text-[var(--text-secondary)] mt-1">Apply Kubernetes workloads to the active cluster.</p>
      </div>
      <div class="flex items-center gap-2 rounded-md border border-[var(--border)] bg-[var(--bg-surface)] p-1">
        <button class="px-3 py-1.5 rounded text-sm flex items-center gap-2" :class="mode === 'form' ? 'bg-[var(--accent)] text-white' : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'" @click="mode = 'form'">
          <Wand2 class="w-4 h-4" /> Simple
        </button>
        <button class="px-3 py-1.5 rounded text-sm flex items-center gap-2" :class="mode === 'yaml' ? 'bg-[var(--accent)] text-white' : 'text-[var(--text-secondary)] hover:bg-[var(--bg-hover)]'" @click="mode = 'yaml'">
          <FileCode class="w-4 h-4" /> YAML
        </button>
      </div>
    </div>

    <div class="grid grid-cols-1 xl:grid-cols-[360px_1fr] gap-5 min-h-0 flex-1">
      <section class="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4 overflow-auto">
        <template v-if="mode === 'form'">
          <div class="space-y-4">
            <label class="block">
              <span class="text-xs text-[var(--text-secondary)]">Name</span>
              <input v-model="form.name" class="mt-1 w-full rounded bg-[var(--bg-primary)] border-[var(--border)] text-sm" placeholder="web" />
            </label>
            <label class="block">
              <span class="text-xs text-[var(--text-secondary)]">Namespace</span>
              <input v-model="form.namespace" class="mt-1 w-full rounded bg-[var(--bg-primary)] border-[var(--border)] text-sm" placeholder="current default" />
            </label>
            <label class="block">
              <span class="text-xs text-[var(--text-secondary)]">Container Image</span>
              <input v-model="form.image" class="mt-1 w-full rounded bg-[var(--bg-primary)] border-[var(--border)] text-sm" placeholder="nginx:latest" />
            </label>
            <div class="grid grid-cols-2 gap-3">
              <label class="block">
                <span class="text-xs text-[var(--text-secondary)]">Replicas</span>
                <input v-model.number="form.replicas" type="number" min="1" class="mt-1 w-full rounded bg-[var(--bg-primary)] border-[var(--border)] text-sm" />
              </label>
              <label class="block">
                <span class="text-xs text-[var(--text-secondary)]">Port</span>
                <input v-model.number="form.port" type="number" min="1" max="65535" class="mt-1 w-full rounded bg-[var(--bg-primary)] border-[var(--border)] text-sm" />
              </label>
            </div>
            <label class="flex items-center gap-2 text-sm">
              <input v-model="form.service" type="checkbox" class="rounded border-[var(--border)]" />
              Create ClusterIP Service
            </label>
          </div>
        </template>
        <template v-else>
          <div class="text-sm text-[var(--text-secondary)] mb-3">Paste any Kubernetes YAML. Multi-document manifests are supported.</div>
          <textarea v-model="rawYaml" spellcheck="false" class="font-mono text-xs leading-5 w-full min-h-[520px] rounded bg-[var(--bg-primary)] border border-[var(--border)] p-3 text-[var(--text-primary)] resize-none"></textarea>
        </template>
      </section>

      <section class="min-h-0 flex flex-col bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
        <div class="flex items-center justify-between gap-3 px-4 py-3 border-b border-[var(--border)]">
          <div class="text-sm font-medium">Manifest</div>
          <button class="p-1.5 rounded hover:bg-[var(--bg-hover)] text-[var(--text-secondary)]" title="Copy YAML" @click="copyYaml">
            <Copy class="w-4 h-4" />
          </button>
        </div>
        <pre class="flex-1 overflow-auto p-4 text-xs leading-5 font-mono whitespace-pre-wrap text-[var(--text-primary)] bg-[var(--bg-primary)]">{{ activeYaml }}</pre>
        <div class="px-4 py-3 border-t border-[var(--border)] flex flex-wrap items-center gap-3">
          <button :disabled="applying" class="inline-flex items-center gap-2 px-4 py-2 rounded bg-[var(--success)] text-white text-sm disabled:opacity-60" @click="apply">
            <Loader2 v-if="applying" class="w-4 h-4 animate-spin" />
            <Rocket v-else class="w-4 h-4" />
            {{ applying ? 'Applying...' : 'Apply YAML' }}
          </button>
          <button :disabled="scanningImages || preloadingImages || applying" class="inline-flex items-center gap-2 px-3 py-2 rounded bg-[var(--bg-hover)] hover:bg-[var(--border)] text-sm disabled:opacity-60" @click="scanImages">
            <Loader2 v-if="scanningImages" class="w-4 h-4 animate-spin" />
            <Search v-else class="w-4 h-4" />
            {{ scanningImages ? 'Scanning...' : 'Scan Images' }}
          </button>
          <button :disabled="!canPreloadImages" class="inline-flex items-center gap-2 px-3 py-2 rounded bg-[var(--accent)] text-white text-sm disabled:opacity-40" @click="preloadImages">
            <Loader2 v-if="preloadingImages" class="w-4 h-4 animate-spin" />
            <Download v-else class="w-4 h-4" />
            {{ preloadingImages ? 'Preloading...' : `Preload Images (${scannedImages.length})` }}
          </button>
          <label class="inline-flex items-center gap-2 text-sm text-[var(--text-secondary)]">
            <input v-model="preloadBeforeApply" type="checkbox" class="rounded border-[var(--border)]" />
            Preload images before apply
          </label>
          <span v-if="yamlError" class="text-sm text-[var(--danger)]">{{ yamlError }}</span>
          <span v-if="scannedImages.length && !store.activeContext?.startsWith('kind-')" class="inline-flex items-center gap-1 text-sm text-[var(--warning)]">
            <AlertTriangle class="w-4 h-4" /> Preload is only available for active KinD clusters.
          </span>
          <div v-if="applied.length" class="flex flex-wrap gap-2">
            <span v-for="r in applied" :key="`${r.kind}-${r.namespace}-${r.name}`" class="inline-flex items-center gap-1 text-xs px-2 py-1 rounded bg-[var(--success)]/15 text-[var(--success)]">
              <CheckCircle2 class="w-3 h-3" /> {{ r.action }} {{ r.kind }}/{{ r.name }}
            </span>
          </div>
        </div>
        <div v-if="scannedImages.length || imageProgressLines.length" class="border-t border-[var(--border)] bg-[var(--bg-surface)] px-4 py-3 space-y-3">
          <div v-if="scannedImages.length" class="flex flex-wrap gap-2">
            <span v-for="image in scannedImages" :key="image" class="font-mono text-[11px] px-2 py-1 rounded bg-[var(--bg-primary)] border border-[var(--border)] text-[var(--text-secondary)]">
              {{ image }}
            </span>
          </div>
          <pre v-if="imageProgressLines.length" class="max-h-44 overflow-auto rounded border border-[var(--border)] bg-[var(--bg-primary)] p-3 text-xs whitespace-pre-wrap text-[var(--text-secondary)]">{{ imageProgressLines.join('\n') }}</pre>
        </div>
      </section>
    </div>
  </div>
</template>
