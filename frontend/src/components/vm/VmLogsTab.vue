<script setup>
import { ref, computed, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useVmStore } from '../../stores/vmStore.js'
import { getVMEvents, getVMPodLogs } from '../../api/client.js'
import { RefreshCw, AlertCircle } from 'lucide-vue-next'

const store = useVmStore()
const subTab = ref('events')

const events = ref([])
const eventsError = ref('')
const eventsLoading = ref(false)

const podLog = ref('')
const podLogError = ref('')
const podLogLoading = ref(false)
const autoScroll = ref(true)
const logPane = ref(null)

let timer = null
const POLL_MS = 5000

const vmName = computed(() => store.selectedNode)

async function loadEvents() {
  if (!vmName.value) return
  eventsLoading.value = true
  try {
    const data = await getVMEvents(vmName.value)
    events.value = Array.isArray(data) ? data : []
    eventsError.value = ''
  } catch (e) {
    eventsError.value = e.message || 'failed to load events'
  } finally {
    eventsLoading.value = false
  }
}

async function loadLogs() {
  if (!vmName.value) return
  podLogLoading.value = true
  try {
    const text = await getVMPodLogs(vmName.value, 500)
    podLog.value = text
    podLogError.value = ''
    if (autoScroll.value) {
      nextTick(() => {
        if (logPane.value) logPane.value.scrollTop = logPane.value.scrollHeight
      })
    }
  } catch (e) {
    // 404 during creation = no pod yet — show it as info not error
    podLogError.value = e.message || 'failed to load logs'
  } finally {
    podLogLoading.value = false
  }
}

function refreshActive() {
  if (subTab.value === 'events') loadEvents()
  else loadLogs()
}

function startPolling() {
  stopPolling()
  timer = setInterval(refreshActive, POLL_MS)
}
function stopPolling() {
  if (timer) { clearInterval(timer); timer = null }
}

watch(subTab, refreshActive)
watch(vmName, () => {
  events.value = []
  podLog.value = ''
  refreshActive()
})

onMounted(() => {
  refreshActive()
  startPolling()
})
onUnmounted(stopPolling)

function eventRowClass(ev) {
  if (ev.type === 'Warning') return 'bg-amber-900/10 border-l-2 border-amber-500/60'
  return ''
}
function fmtTime(t) {
  try { return new Date(t).toLocaleTimeString() } catch { return t }
}
</script>

<template>
  <div class="flex flex-col h-full">
    <!-- Sub-tab bar -->
    <div class="flex items-center justify-between border-b border-[var(--border)] bg-[var(--bg-secondary)] px-4">
      <div class="flex">
        <button
          class="px-4 py-2 text-sm transition-colors relative"
          :class="subTab === 'events'
            ? 'text-[var(--accent)]'
            : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'"
          @click="subTab = 'events'"
        >
          Events
          <div v-if="subTab === 'events'" class="absolute bottom-0 left-0 right-0 h-0.5 bg-[var(--accent)]" />
        </button>
        <button
          class="px-4 py-2 text-sm transition-colors relative"
          :class="subTab === 'pod'
            ? 'text-[var(--accent)]'
            : 'text-[var(--text-secondary)] hover:text-[var(--text-primary)]'"
          @click="subTab = 'pod'"
        >
          Pod Logs
          <div v-if="subTab === 'pod'" class="absolute bottom-0 left-0 right-0 h-0.5 bg-[var(--accent)]" />
        </button>
      </div>
      <div class="flex items-center gap-3 text-xs text-[var(--text-secondary)]">
        <label v-if="subTab === 'pod'" class="flex items-center gap-1 cursor-pointer">
          <input type="checkbox" v-model="autoScroll" class="accent-[var(--accent)]" />
          Auto-scroll
        </label>
        <span class="text-[var(--muted)]">Polls every {{ POLL_MS / 1000 }}s</span>
        <button
          @click="refreshActive"
          class="flex items-center gap-1 px-2 py-1 rounded bg-[var(--bg-hover)] hover:bg-[var(--border)] transition-colors"
          title="Refresh now"
        >
          <RefreshCw class="w-3 h-3" :class="{ 'animate-spin': eventsLoading || podLogLoading }" />
          Refresh
        </button>
      </div>
    </div>

    <!-- Events view -->
    <div v-if="subTab === 'events'" class="flex-1 overflow-auto">
      <div v-if="eventsError" class="m-4 flex items-start gap-2 bg-red-900/20 border border-red-800/40 rounded px-3 py-2 text-sm text-red-300">
        <AlertCircle class="w-4 h-4 mt-0.5 shrink-0" />
        {{ eventsError }}
      </div>
      <div v-else-if="events.length === 0" class="p-6 text-sm text-[var(--text-secondary)]">
        No events yet.
      </div>
      <table v-else class="w-full text-xs">
        <thead class="sticky top-0 bg-[var(--bg-secondary)] border-b border-[var(--border)] text-[var(--text-secondary)]">
          <tr>
            <th class="text-left px-4 py-2 font-medium w-20">Time</th>
            <th class="text-left px-4 py-2 font-medium w-20">Type</th>
            <th class="text-left px-4 py-2 font-medium w-32">Reason</th>
            <th class="text-left px-4 py-2 font-medium w-48">Object</th>
            <th class="text-left px-4 py-2 font-medium">Message</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(ev, idx) in events"
            :key="`${ev.time}-${idx}`"
            class="border-b border-[var(--border)] last:border-b-0"
            :class="eventRowClass(ev)"
          >
            <td class="px-4 py-1.5 font-mono text-[var(--text-secondary)] whitespace-nowrap">{{ fmtTime(ev.time) }}</td>
            <td class="px-4 py-1.5">
              <span :class="ev.type === 'Warning' ? 'text-amber-400' : 'text-[var(--text-secondary)]'">
                {{ ev.type }}
              </span>
            </td>
            <td class="px-4 py-1.5 font-mono text-[var(--text-primary)]">{{ ev.reason }}</td>
            <td class="px-4 py-1.5 font-mono text-[var(--text-secondary)]">
              <span class="text-[var(--muted)]">{{ ev.kind }}/</span>{{ ev.object }}
            </td>
            <td class="px-4 py-1.5 text-[var(--text-primary)]">{{ ev.message }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Pod logs view -->
    <div v-else class="flex-1 overflow-hidden flex flex-col">
      <div v-if="podLogError && !podLog" class="m-4 flex items-start gap-2 bg-amber-900/20 border border-amber-800/40 rounded px-3 py-2 text-sm text-amber-300">
        <AlertCircle class="w-4 h-4 mt-0.5 shrink-0" />
        {{ podLogError }}
      </div>
      <pre
        v-else
        ref="logPane"
        class="flex-1 overflow-auto m-0 px-4 py-3 text-xs font-mono text-[var(--text-primary)] bg-[var(--bg-primary)] whitespace-pre-wrap"
      >{{ podLog || 'No log output yet.' }}</pre>
    </div>
  </div>
</template>
