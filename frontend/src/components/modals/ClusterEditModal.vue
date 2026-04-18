<script setup>
import { ref } from 'vue'

const props = defineProps({
  context: { type: String, required: true },
  tag: { type: String, default: '' },
  color: { type: String, default: '' },
})
const emit = defineEmits(['save', 'cancel'])

const tag = ref(props.tag)
const color = ref(props.color)

// Curated palette — enough variety for dev/staging/prod plus a few
// extras, but small enough that the user doesn't have to think.
const swatches = [
  { name: 'Green',  value: '#16a34a' },
  { name: 'Blue',   value: '#2563eb' },
  { name: 'Red',    value: '#dc2626' },
  { name: 'Amber',  value: '#d97706' },
  { name: 'Purple', value: '#7c3aed' },
  { name: 'Teal',   value: '#0d9488' },
  { name: 'Pink',   value: '#db2777' },
  { name: 'Gray',   value: '#6b7280' },
]

const presetTags = ['dev', 'staging', 'prod', 'test']

function pickSwatch(v) {
  color.value = color.value === v ? '' : v
}

function pickTag(t) {
  tag.value = tag.value === t ? '' : t
}

function submit() {
  emit('save', { tag: tag.value.trim(), color: color.value.trim() })
}
</script>

<template>
  <Teleport to="body">
    <div class="fixed inset-0 z-40 flex items-center justify-center">
      <div class="absolute inset-0 bg-black/60 backdrop-blur-sm" @click="emit('cancel')" />
      <div class="relative bg-[var(--bg-surface)] rounded-lg border border-[var(--border)] p-6 max-w-sm w-full mx-4 shadow-2xl">
        <h3 class="text-sm font-medium mb-1">Cluster identity</h3>
        <p class="text-xs text-[var(--muted)] mb-4 font-mono truncate">{{ context }}</p>

        <label class="block text-xs text-[var(--text-secondary)] mb-1">Tag</label>
        <input
          v-model="tag"
          @keydown.enter="submit"
          @keydown.escape="emit('cancel')"
          placeholder="e.g. dev"
          class="w-full px-3 py-1.5 text-sm rounded bg-[var(--bg-primary)] border border-[var(--border)] focus:border-[var(--accent)] focus:outline-none font-mono"
        />
        <div class="flex gap-1 mt-2 flex-wrap">
          <button
            v-for="t in presetTags"
            :key="t"
            class="px-2 py-0.5 text-[11px] uppercase rounded border transition-colors"
            :class="tag === t
              ? 'bg-[var(--accent)]/20 border-[var(--accent)] text-[var(--accent)]'
              : 'bg-[var(--bg-hover)] border-[var(--border)] text-[var(--text-secondary)] hover:border-[var(--accent)]'"
            @click="pickTag(t)"
          >{{ t }}</button>
        </div>

        <label class="block text-xs text-[var(--text-secondary)] mt-4 mb-1">Accent colour</label>
        <div class="flex gap-1.5 flex-wrap">
          <button
            v-for="s in swatches"
            :key="s.value"
            class="w-6 h-6 rounded border-2 transition-transform hover:scale-110"
            :style="{
              background: s.value,
              borderColor: color === s.value ? 'var(--text-primary)' : 'transparent',
            }"
            :title="s.name"
            @click="pickSwatch(s.value)"
          />
          <button
            class="w-6 h-6 rounded border-2 border-dashed border-[var(--muted)] text-[var(--muted)] text-[10px] hover:border-[var(--text-primary)] hover:text-[var(--text-primary)]"
            :class="{ 'bg-[var(--bg-hover)]': color === '' }"
            title="Clear"
            @click="color = ''"
          >✕</button>
        </div>
        <input
          v-model="color"
          placeholder="#hex or blank"
          class="mt-2 w-full px-3 py-1.5 text-xs rounded bg-[var(--bg-primary)] border border-[var(--border)] focus:border-[var(--accent)] focus:outline-none font-mono"
        />

        <div class="flex justify-end gap-3 mt-5">
          <button
            @click="emit('cancel')"
            class="px-4 py-2 text-sm rounded bg-[var(--bg-hover)] hover:bg-[var(--border)] transition-colors"
          >Cancel</button>
          <button
            @click="submit"
            class="px-4 py-2 text-sm rounded bg-[var(--accent)] hover:bg-blue-600 transition-colors"
          >Save</button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
