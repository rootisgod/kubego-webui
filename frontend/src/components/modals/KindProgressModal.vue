<script setup>
import { nextTick, ref, watch } from 'vue'
import { Loader2, CheckCircle2, AlertCircle, X } from 'lucide-vue-next'

const props = defineProps({
  title: { type: String, required: true },
  status: { type: String, required: true }, // 'running' | 'done' | 'error'
  lines: { type: Array, required: true },
  errorMessage: { type: String, default: '' },
})

const emit = defineEmits(['close'])
const logBox = ref(null)

// Auto-scroll to bottom as new lines arrive. `lines` is the array from
// the parent — watching it deeply would re-run on every mutation. We
// only need length as a cheap proxy for "new line pushed".
watch(
  () => props.lines.length,
  async () => {
    await nextTick()
    if (logBox.value) logBox.value.scrollTop = logBox.value.scrollHeight
  }
)

function canClose() {
  return props.status !== 'running'
}
</script>

<template>
  <Teleport to="body">
    <div class="fixed inset-0 z-40 flex items-center justify-center">
      <div
        class="absolute inset-0 bg-black/60 backdrop-blur-sm"
        @click="canClose() && emit('close')"
      />
      <div class="relative bg-[var(--bg-surface)] rounded-lg border border-[var(--border)] p-5 max-w-2xl w-full mx-4 shadow-2xl">
        <div class="flex items-center gap-2 mb-3">
          <Loader2 v-if="status === 'running'" class="w-4 h-4 animate-spin text-[var(--accent)]" />
          <CheckCircle2 v-else-if="status === 'done'" class="w-4 h-4 text-[var(--success)]" />
          <AlertCircle v-else class="w-4 h-4 text-[var(--danger)]" />
          <h3 class="text-sm font-medium flex-1">{{ title }}</h3>
          <button
            v-if="canClose()"
            @click="emit('close')"
            class="w-6 h-6 flex items-center justify-center rounded hover:bg-[var(--bg-hover)] transition-colors"
            title="Close"
          >
            <X class="w-4 h-4" />
          </button>
        </div>
        <div
          ref="logBox"
          class="font-mono text-[11px] leading-snug bg-[var(--bg-primary)] border border-[var(--border)] rounded p-3 h-80 overflow-y-auto whitespace-pre-wrap"
        >
          <div v-for="(line, i) in lines" :key="i" class="text-[var(--text-secondary)]">{{ line }}</div>
          <div v-if="lines.length === 0" class="text-[var(--muted)] italic">waiting for output...</div>
        </div>
        <div v-if="status === 'error'" class="mt-3 text-xs text-[var(--danger)]">
          {{ errorMessage || 'command failed' }}
        </div>
        <div v-if="status === 'done'" class="mt-3 text-xs text-[var(--success)]">
          Done.
        </div>
      </div>
    </div>
  </Teleport>
</template>
