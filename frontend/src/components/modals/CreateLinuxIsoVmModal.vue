<script setup>
import { ref, computed, onMounted } from 'vue'
import { useVmStore } from '../../stores/vmStore.js'
import { useToastStore } from '../../stores/toastStore.js'
import * as api from '../../api/client.js'
import { Loader2, Terminal, AlertCircle, ExternalLink } from 'lucide-vue-next'

const emit = defineEmits(['close'])
const store = useVmStore()
const toasts = useToastStore()

function openImages() {
  emit('close')
  store.selectNode('__images__')
}

const name = ref('')
const installerPVC = ref('')
const cpus = ref(2)
const memoryMB = ref(2048)
const diskGB = ref(20)
const uefi = ref(false)
const submitting = ref(false)

const images = ref([])
const loadingImages = ref(true)

const installerImages = computed(() =>
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

  submitting.value = true
  try {
    const opts = {
      name: name.value || '',
      installer_iso: installerPVC.value,
      cpus: Number(cpus.value),
      memory_mb: Number(memoryMB.value),
      disk_gb: Number(diskGB.value),
      uefi: uefi.value,
    }
    await api.createLinuxIsoVM(opts)
    toasts.success('Linux ISO VM launch started — drive the installer via the Graphics tab')
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
          <Terminal class="w-5 h-5 text-[var(--success)]" />
          <h3 class="text-lg font-semibold">Launch Linux VM from ISO</h3>
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
              Upload a Linux ISO (Ubuntu, Debian, Fedora, …) on the
              <button
                type="button"
                @click="openImages"
                class="inline-flex items-center gap-0.5 text-[var(--accent)] hover:underline font-medium"
              >Images<ExternalLink class="w-3 h-3" /></button>
              page first. For cloud-image installs without an ISO, use the basic <strong>New VM</strong> flow instead.
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
              placeholder="linux-iso"
              class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
            />
            <p class="text-xs text-[var(--text-secondary)] mt-1">Leave empty for an auto-generated name.</p>
          </div>

          <!-- Installer ISO -->
          <div>
            <label class="flex items-center justify-between text-xs text-[var(--text-secondary)] mb-1">
              <span>Linux installer ISO</span>
              <button
                type="button"
                @click="openImages"
                class="inline-flex items-center gap-0.5 text-[var(--accent)] hover:underline"
              >Manage images<ExternalLink class="w-3 h-3" /></button>
            </label>
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

          <!-- Resources -->
          <div class="grid grid-cols-3 gap-3">
            <div>
              <label class="block text-xs text-[var(--text-secondary)] mb-1">CPUs</label>
              <input
                v-model.number="cpus"
                type="number"
                min="1"
                class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
              />
            </div>
            <div>
              <label class="block text-xs text-[var(--text-secondary)] mb-1">RAM (MB)</label>
              <input
                v-model.number="memoryMB"
                type="number"
                min="512"
                step="512"
                class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
              />
            </div>
            <div>
              <label class="block text-xs text-[var(--text-secondary)] mb-1">Disk (GB)</label>
              <input
                v-model.number="diskGB"
                type="number"
                min="8"
                class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-2 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
              />
            </div>
          </div>

          <!-- Firmware -->
          <div class="space-y-2">
            <label class="flex items-center gap-2 text-sm cursor-pointer">
              <input v-model="uefi" type="checkbox" class="accent-[var(--accent)]" />
              UEFI firmware (OVMF without Secure Boot)
            </label>
            <p class="text-[11px] text-[var(--text-secondary)] pl-6">
              Leave off for BIOS/SeaBIOS — works for most distro ISOs. Enable for UEFI-only installers.
            </p>
          </div>

          <div class="p-3 rounded bg-[var(--bg-primary)] border border-[var(--border)] text-xs text-[var(--text-secondary)]">
            After launch, open the <strong>Graphics</strong> tab on the VM to run the installer interactively.
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
            class="flex items-center gap-2 px-4 py-2 text-sm rounded bg-[var(--success)] hover:bg-green-600 transition-colors disabled:opacity-40 text-white"
          >
            <Loader2 v-if="submitting" class="w-4 h-4 animate-spin" />
            Launch
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
