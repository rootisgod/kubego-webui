<script setup>
import { ref, computed, onMounted } from 'vue'
import { useVmStore } from '../../stores/vmStore.js'
import { useToastStore } from '../../stores/toastStore.js'
import * as api from '../../api/client.js'
import ActionButton from '../shared/ActionButton.vue'
import ConfirmModal from '../modals/ConfirmModal.vue'
import { Plus, Trash2 } from 'lucide-vue-next'

const store = useVmStore()
const toasts = useToastStore()
const disks = ref([])
const loading = ref(false)
const loadError = ref('')
const showAddForm = ref(false)
const newName = ref('')
const newClaim = ref('')
const confirmAction = ref(null)

const vm = computed(() => store.selectedVm)
const isRunning = computed(() => vm.value?.state === 'Running')

async function loadDisks() {
  loading.value = true
  loadError.value = ''
  try {
    const data = await api.listDisks(store.selectedNode)
    disks.value = Array.isArray(data) ? data : []
  } catch (e) {
    disks.value = []
    loadError.value = e.message || 'Failed to load disks'
  } finally {
    loading.value = false
  }
}

async function attach() {
  if (!newName.value || !newClaim.value) return
  try {
    await api.attachDisk(store.selectedNode, { name: newName.value, claim: newClaim.value })
    toasts.success('Disk attached')
    newName.value = ''
    newClaim.value = ''
    showAddForm.value = false
    loadDisks()
  } catch (e) { toasts.error(e.message) }
}

function confirmDetach(name) {
  confirmAction.value = {
    message: `Detach disk '${name}'?`,
    fn: async () => {
      try {
        await api.detachDisk(store.selectedNode, name)
        toasts.success('Disk detached')
        loadDisks()
      } catch (e) { toasts.error(e.message) }
    },
  }
}

async function executeConfirmed() {
  const fn = confirmAction.value?.fn
  confirmAction.value = null
  if (fn) await fn()
}

onMounted(loadDisks)
</script>

<template>
  <div class="p-6">
    <div class="flex items-center justify-between mb-4">
      <h3 class="text-lg font-semibold">Disks</h3>
      <ActionButton label="Attach Disk" :icon="Plus" variant="success" :disabled="!isRunning" @click="showAddForm = !showAddForm" />
    </div>

    <p class="text-xs text-[var(--text-secondary)] mb-4">
      Hot-plug a PersistentVolumeClaim as a disk on this VM. The claim must already exist in the cluster.
    </p>

    <!-- Attach form -->
    <div v-if="showAddForm" class="bg-[var(--bg-surface)] rounded-lg border border-[var(--border)] p-4 mb-4">
      <div class="grid grid-cols-2 gap-4 mb-3">
        <div>
          <label class="block text-xs text-[var(--text-secondary)] mb-1">Disk name</label>
          <input
            v-model="newName"
            type="text"
            placeholder="data-disk"
            class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-1.5 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
          />
        </div>
        <div>
          <label class="block text-xs text-[var(--text-secondary)] mb-1">PVC claim name</label>
          <input
            v-model="newClaim"
            type="text"
            placeholder="my-pvc"
            class="w-full bg-[var(--bg-primary)] border border-[var(--border)] rounded px-3 py-1.5 text-sm text-[var(--text-primary)] focus:border-[var(--accent)] focus:ring-1 focus:ring-[var(--accent)]"
          />
        </div>
      </div>
      <div class="flex gap-2">
        <button
          @click="attach"
          class="px-3 py-1.5 text-sm rounded bg-[var(--accent)] hover:bg-blue-600 transition-colors"
        >Attach</button>
        <button
          @click="showAddForm = false"
          class="px-3 py-1.5 text-sm rounded bg-[var(--bg-hover)] hover:bg-[var(--border)] transition-colors"
        >Cancel</button>
      </div>
    </div>

    <!-- Disk table -->
    <div v-if="disks.length > 0" class="bg-[var(--bg-surface)] rounded-lg border border-[var(--border)] overflow-hidden">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-[var(--border)] text-[var(--text-secondary)]">
            <th class="text-left px-4 py-2.5 font-medium">Name</th>
            <th class="text-left px-4 py-2.5 font-medium">Claim</th>
            <th class="text-left px-4 py-2.5 font-medium">Size</th>
            <th class="text-right px-4 py-2.5 font-medium">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="disk in disks"
            :key="disk.name"
            class="border-b border-[var(--border)] last:border-b-0 hover:bg-[var(--bg-hover)]"
          >
            <td class="px-4 py-2.5 font-mono text-xs">{{ disk.name }}</td>
            <td class="px-4 py-2.5 font-mono text-xs">{{ disk.claim || '—' }}</td>
            <td class="px-4 py-2.5 text-xs text-[var(--text-secondary)]">{{ disk.size || '—' }}</td>
            <td class="px-4 py-2.5 text-right">
              <button
                class="p-1 rounded hover:bg-[var(--danger)] transition-colors"
                title="Detach"
                @click="confirmDetach(disk.name)"
              >
                <Trash2 class="w-4 h-4" />
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-else-if="!loading && loadError" class="text-[var(--text-secondary)] text-sm">
      {{ loadError }}
    </div>
    <div v-else-if="!loading" class="text-[var(--text-secondary)] text-sm">
      No disks attached
    </div>

    <ConfirmModal
      v-if="confirmAction"
      :message="confirmAction.message"
      @confirm="executeConfirmed"
      @cancel="confirmAction = null"
    />
  </div>
</template>
