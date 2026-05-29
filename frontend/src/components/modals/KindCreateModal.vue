<script setup>
import { ref, onMounted, computed, watch } from 'vue'
import * as api from '../../api/client.js'

const emit = defineEmits(['confirm', 'cancel'])
const name = ref('')
const inputRef = ref(null)
const ingress = ref(false)
const ingressHttp = ref(80)
const ingressHttps = ref(443)
const checkingPorts = ref(false)
const portStatus = ref([])
const portError = ref('')
let checkSeq = 0

// Mirrors kindNameRe in handlers_clusters.go. Keep these in sync —
// a mismatch just shifts where the validation error is rendered.
const valid = computed(() => /^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/.test(name.value))
const portsValid = computed(() => {
  if (!ingress.value) return true
  return validPort(ingressHttp.value) &&
    validPort(ingressHttps.value) &&
    Number(ingressHttp.value) !== Number(ingressHttps.value) &&
    !portError.value &&
    portStatus.value.every(p => p.available)
})
const canSubmit = computed(() => valid.value && portsValid.value && !checkingPorts.value)

function validPort(port) {
  const n = Number(port)
  return Number.isInteger(n) && n >= 1 && n <= 65535
}

function portLabel(port) {
  const s = portStatus.value.find(p => p.port === Number(port))
  if (!s) return ''
  return s.available ? 'available' : 'in use'
}

function submit() {
  if (!canSubmit.value) return
  emit('confirm', {
    name: name.value,
    ingress: ingress.value,
    ingress_http: Number(ingressHttp.value),
    ingress_https: Number(ingressHttps.value),
  })
}

async function checkPorts() {
  portError.value = ''
  portStatus.value = []
  if (!ingress.value) return
  if (!validPort(ingressHttp.value) || !validPort(ingressHttps.value)) {
    portError.value = 'Ports must be between 1 and 65535.'
    return
  }
  if (Number(ingressHttp.value) === Number(ingressHttps.value)) {
    portError.value = 'HTTP and HTTPS ports must be different.'
    return
  }
  const seq = ++checkSeq
  checkingPorts.value = true
  try {
    const res = await api.checkHostPorts([Number(ingressHttp.value), Number(ingressHttps.value)])
    if (seq === checkSeq) portStatus.value = res.ports || []
  } catch (e) {
    if (seq === checkSeq) portError.value = e.message
  } finally {
    if (seq === checkSeq) checkingPorts.value = false
  }
}

watch([ingress, ingressHttp, ingressHttps], () => checkPorts(), { immediate: false })
onMounted(() => inputRef.value?.focus())
</script>

<template>
  <Teleport to="body">
    <div class="fixed inset-0 z-40 flex items-center justify-center">
      <div class="absolute inset-0 bg-black/60 backdrop-blur-sm" @click="emit('cancel')" />
      <div class="relative bg-[var(--bg-surface)] rounded-lg border border-[var(--border)] p-6 max-w-md w-full mx-4 shadow-2xl">
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

        <div class="mt-4 rounded border border-[var(--border)] bg-[var(--bg-primary)] p-3">
          <label class="flex items-center gap-2 text-sm">
            <input v-model="ingress" type="checkbox" class="rounded border-[var(--border)]" />
            Bind host ports for ingress
          </label>
          <div v-if="ingress" class="mt-3 grid grid-cols-2 gap-3">
            <label class="block">
              <span class="text-[11px] text-[var(--muted)]">HTTP host port</span>
              <input
                v-model.number="ingressHttp"
                type="number"
                min="1"
                max="65535"
                class="mt-1 w-full px-2 py-1.5 text-sm rounded bg-[var(--bg-surface)] border border-[var(--border)] focus:border-[var(--accent)] focus:outline-none font-mono"
              />
              <span
                v-if="portLabel(ingressHttp)"
                class="mt-1 block text-[11px]"
                :class="portLabel(ingressHttp) === 'available' ? 'text-[var(--success)]' : 'text-[var(--danger)]'"
              >{{ portLabel(ingressHttp) }}</span>
            </label>
            <label class="block">
              <span class="text-[11px] text-[var(--muted)]">HTTPS host port</span>
              <input
                v-model.number="ingressHttps"
                type="number"
                min="1"
                max="65535"
                class="mt-1 w-full px-2 py-1.5 text-sm rounded bg-[var(--bg-surface)] border border-[var(--border)] focus:border-[var(--accent)] focus:outline-none font-mono"
              />
              <span
                v-if="portLabel(ingressHttps)"
                class="mt-1 block text-[11px]"
                :class="portLabel(ingressHttps) === 'available' ? 'text-[var(--success)]' : 'text-[var(--danger)]'"
              >{{ portLabel(ingressHttps) }}</span>
            </label>
          </div>
          <p v-if="ingress && checkingPorts" class="mt-2 text-[11px] text-[var(--muted)]">Checking ports...</p>
          <p v-if="ingress && portError" class="mt-2 text-[11px] text-[var(--danger)]">{{ portError }}</p>
        </div>

        <div class="flex justify-end gap-3 mt-4">
          <button
            @click="emit('cancel')"
            class="px-4 py-2 text-sm rounded bg-[var(--bg-hover)] hover:bg-[var(--border)] transition-colors"
          >Cancel</button>
          <button
            @click="submit"
            :disabled="!canSubmit"
            class="px-4 py-2 text-sm rounded bg-[var(--accent)] hover:bg-blue-600 transition-colors disabled:opacity-40"
          >Create</button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
