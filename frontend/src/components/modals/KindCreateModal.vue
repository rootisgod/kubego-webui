<script setup>
import { ref, onMounted, computed } from 'vue'

const emit = defineEmits(['confirm', 'cancel'])
const name = ref('')
const inputRef = ref(null)

// Mirrors kindNameRe in handlers_clusters.go. Keep these in sync —
// a mismatch just shifts where the validation error is rendered.
const valid = computed(() => /^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/.test(name.value))

function submit() {
  if (valid.value) emit('confirm', name.value)
}

onMounted(() => inputRef.value?.focus())
</script>

<template>
  <Teleport to="body">
    <div class="fixed inset-0 z-40 flex items-center justify-center">
      <div class="absolute inset-0 bg-black/60 backdrop-blur-sm" @click="emit('cancel')" />
      <div class="relative bg-[var(--bg-surface)] rounded-lg border border-[var(--border)] p-6 max-w-sm w-full mx-4 shadow-2xl">
        <h3 class="text-sm font-medium mb-2">New KinD cluster</h3>
        <p class="text-xs text-[var(--muted)] mb-3">
          Creates a local KinD cluster. The resulting context will be named
          <code class="text-[var(--text-secondary)]">kind-&lt;name&gt;</code> and will become active on success.
        </p>
        <input
          ref="inputRef"
          v-model="name"
          @keydown.enter="submit"
          @keydown.escape="emit('cancel')"
          placeholder="cluster name (e.g. dev)"
          class="w-full px-3 py-2 text-sm rounded bg-[var(--bg-primary)] border border-[var(--border)] focus:border-[var(--accent)] focus:outline-none font-mono"
        />
        <p v-if="name && !valid" class="mt-2 text-[11px] text-[var(--danger)]">
          lowercase alphanumeric + dashes, 2-32 chars
        </p>
        <div class="flex justify-end gap-3 mt-4">
          <button
            @click="emit('cancel')"
            class="px-4 py-2 text-sm rounded bg-[var(--bg-hover)] hover:bg-[var(--border)] transition-colors"
          >Cancel</button>
          <button
            @click="submit"
            :disabled="!valid"
            class="px-4 py-2 text-sm rounded bg-[var(--accent)] hover:bg-blue-600 transition-colors disabled:opacity-40"
          >Create</button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
