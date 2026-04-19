<script setup>
import { ref, computed, onMounted } from 'vue'
import { useVmStore } from '../../stores/vmStore.js'
import { useToastStore } from '../../stores/toastStore.js'
import * as api from '../../api/client.js'
import { Loader2, MonitorSmartphone, AlertCircle, ExternalLink } from 'lucide-vue-next'

const emit = defineEmits(['close'])
const store = useVmStore()
const toasts = useToastStore()

const name = ref('')
const installerPVC = ref('')
const virtioPVC = ref('')
const hostname = ref('')
const adminPassword = ref('')
const enableRDP = ref(true)
const secureBoot = ref(true)
const tpm = ref(true)
const cpus = ref(4)
const memoryMB = ref(8192)
const diskGB = ref(60)
const submitting = ref(false)

const images = ref([])
const loadingImages = ref(true)

const installerImages = computed(() =>
  images.value.filter((i) => i.kind === 'iso' && i.phase === 'Succeeded'),
)
const virtioImages = computed(() =>
  images.value.filter((i) => i.kind === 'iso' && i.phase === 'Succeeded'),
)

const hasReadyISOs = computed(() => installerImages.value.length > 0)

onMounted(async () => {
  try {
    const data = await api.listImageUploads()
    images.value = Array.isArray(data) ? data : []
  } catch (e) {
    toasts.error('Failed to load images: ' + e.message)
  } finally {
    loadingImages.value = false
  }
})

async function submit() {
  if (!installerPVC.value) {
    toasts.error('Pick an installer ISO')
    return
  }
  if (!adminPassword.value) {
    toasts.error('Admin password is required')
    return
  }

  submitting.value = true
  try {
    const opts = {
      name: name.value || '',
      installer_iso: installerPVC.value,
      virtio_win_iso: virtioPVC.value || '',
      hostname: hostname.value || '',
      admin_password: adminPassword.value,
      enable_rdp: enableRDP.value,
      cpus: Number(cpus.value),
      memory_mb: Number(memoryMB.value),
      disk_gb: Number(diskGB.value),
      secure_boot: secureBoot.value,
      tpm: tpm.value,
    }
    await api.createWindowsVM(opts)
    toasts.success('Windows VM launch started — watch via the Graphics tab')
    store.fetchVMs()
    emit('close')
  } catch (e) {
    toasts.error(e.message)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Teleport to="body">
    <div class="fixed inset-0 z-40 flex items-center justify-center">
      <div class="absolute inset-0 bg-black/60 backdrop-blur-sm" @click="emit('close')" />
      <div class="relative bg-[var(--bg-surface)] rounded-lg border border-[var(--border)] p-6 max-w-xl w-full mx-4 shadow-2xl max-h-[90vh] overflow-y-auto">
        <div class="flex items-center gap-2 mb-4">
          <MonitorSmartphone class="w-5 h-5 text-[var(--accent)]" />
          <h3 class="text-lg font-semibold">Launch Windows VM</h3>
        </div>

        <div v-if="loadingImages" class="text-sm text-[var(--text-secondary)] py-8 text-center">
          Loading image catalog…
        </div>

        <div
          v-else-if="!hasReadyISOs"
          class="p-4 rounded bg-yellow-900/20 border border-yellow-800 text-sm text-yellow-200 flex items-start gap-2"
        >
          <AlertCircle class="w-4 h-4 flex-shrink-0 mt-0.5" />
          <div>
            <div class="font-medium mb-1">No installer ISOs available</div>
            <div class="text-xs text-yellow-300">
              Upload a Windows ISO (and ideally a virtio-win ISO) from the <strong>Images</strong> page before launching a Windows VM.
            </div>
          </div>
        </div>

        <div v-else class="space-y-4">
          <!-- Name -->
          <div>
            <label class="block text-xs text-[var(--text-secondary)] mb-1">VM name</label>
            <input
              v-model="name"
              type="text"
              placeholder="win-dev"
              class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
            />
            <p class="text-xs text-[var(--text-secondary)] mt-1">Leave empty for an auto-generated name.</p>
          </div>

          <!-- Installer ISO -->
          <div>
            <label class="block text-xs text-[var(--text-secondary)] mb-1">Windows installer ISO</label>
            <select
              v-model="installerPVC"
              class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
            >
              <option value="" disabled>Select…</option>
              <option v-for="img in installerImages" :key="img.pvc_name" :value="img.pvc_name">
                {{ img.name }} ({{ img.size }})
              </option>
            </select>
          </div>

          <!-- virtio-win ISO -->
          <div>
            <label class="block text-xs text-[var(--text-secondary)] mb-1">
              virtio-win drivers ISO
              <a
                href="https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/stable-virtio/virtio-win.iso"
                target="_blank"
                rel="noopener"
                class="inline-flex items-center gap-0.5 text-[var(--accent)] hover:underline"
              >
                <ExternalLink class="w-3 h-3" />
                download
              </a>
            </label>
            <select
              v-model="virtioPVC"
              class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
            >
              <option value="">None (virtio root disk will not be visible during install)</option>
              <option v-for="img in virtioImages" :key="'v-' + img.pvc_name" :value="img.pvc_name">
                {{ img.name }} ({{ img.size }})
              </option>
            </select>
          </div>

          <!-- Hostname + admin password -->
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="block text-xs text-[var(--text-secondary)] mb-1">Hostname</label>
              <input
                v-model="hostname"
                type="text"
                placeholder="Defaults to VM name"
                class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
              />
            </div>
            <div>
              <label class="block text-xs text-[var(--text-secondary)] mb-1">Administrator password *</label>
              <input
                v-model="adminPassword"
                type="password"
                class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
              />
            </div>
          </div>

          <!-- Resources -->
          <div class="grid grid-cols-3 gap-3">
            <div>
              <label class="block text-xs text-[var(--text-secondary)] mb-1">CPUs</label>
              <input
                v-model.number="cpus"
                type="number"
                min="2"
                class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
              />
            </div>
            <div>
              <label class="block text-xs text-[var(--text-secondary)] mb-1">RAM (MB)</label>
              <input
                v-model.number="memoryMB"
                type="number"
                min="2048"
                step="1024"
                class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
              />
            </div>
            <div>
              <label class="block text-xs text-[var(--text-secondary)] mb-1">Disk (GB)</label>
              <input
                v-model.number="diskGB"
                type="number"
                min="40"
                class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
              />
            </div>
          </div>

          <!-- Toggles -->
          <div class="space-y-2">
            <label class="flex items-center gap-2 text-sm cursor-pointer">
              <input v-model="enableRDP" type="checkbox" class="accent-[var(--accent)]" />
              Enable Remote Desktop on first boot (recommended)
            </label>
            <label class="flex items-center gap-2 text-sm cursor-pointer">
              <input v-model="secureBoot" type="checkbox" class="accent-[var(--accent)]" />
              Secure Boot (required for Windows 11; forces SMM)
            </label>
            <label class="flex items-center gap-2 text-sm cursor-pointer">
              <input v-model="tpm" type="checkbox" class="accent-[var(--accent)]" />
              Virtual TPM (required for Windows 11)
            </label>
          </div>
        </div>

        <div class="flex justify-end gap-3 mt-6">
          <button
            @click="emit('close')"
            class="px-4 py-2 text-sm rounded bg-[var(--bg-hover)] hover:bg-[var(--border)] transition-colors"
          >Cancel</button>
          <button
            @click="submit"
            :disabled="submitting || !hasReadyISOs"
            class="flex items-center gap-2 px-4 py-2 text-sm rounded bg-[var(--accent)] hover:bg-blue-600 transition-colors disabled:opacity-40 text-white"
          >
            <Loader2 v-if="submitting" class="w-4 h-4 animate-spin" />
            Launch
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
