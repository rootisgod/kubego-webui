<script setup>
import { ref, onMounted, computed } from 'vue'
import { hostCheck, applyHostSysctls } from '../../api/client.js'
import { useToastStore } from '../../stores/toastStore.js'
import { Wrench, RefreshCw, CheckCircle2, XCircle, AlertTriangle, Terminal, Zap, Loader2 } from 'lucide-vue-next'

const toasts = useToastStore()

const data = ref(null)
const loading = ref(false)
const applying = ref(false)
const applyResults = ref(null)
const error = ref('')

// All required tools available AND all sysctls meet their recommended
// minimums → green banner at the top of the panel.
const allGreen = computed(() => {
  if (!data.value) return false
  const toolsOk = data.value.tools.every(t => !t.required || t.available)
  const sysctlsOk = data.value.sysctls.every(s => s.ok)
  return toolsOk && sysctlsOk
})

const hasSysctlFailures = computed(() => {
  if (!data.value) return false
  return data.value.sysctls.some(s => !s.ok)
})

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await hostCheck()
  } catch (e) {
    error.value = e.message
  } finally {
    loading.value = false
  }
}

async function apply() {
  applying.value = true
  applyResults.value = null
  try {
    const { results } = await applyHostSysctls()
    applyResults.value = results
    const ok = results.every(r => r.applied)
    if (ok) toasts.success('Sysctls applied')
    else toasts.error('Some sysctls failed to apply')
  } catch (e) {
    toasts.error(e.message)
  } finally {
    applying.value = false
    await load()
  }
}

onMounted(load)
</script>

<template>
  <div class="p-6 max-w-5xl">
    <div class="flex items-center justify-between mb-6">
      <h2 class="text-xl font-semibold flex items-center gap-2">
        <Wrench class="w-5 h-5" />
        Machine Check
      </h2>
      <button
        class="flex items-center gap-1.5 px-3 py-1.5 text-sm rounded bg-[var(--bg-hover)] hover:bg-[var(--border)] transition-colors"
        :disabled="loading"
        @click="load"
      >
        <RefreshCw class="w-3.5 h-3.5" :class="{ 'animate-spin': loading }" />
        Refresh
      </button>
    </div>

    <div v-if="error" class="mb-4 px-4 py-3 rounded bg-red-900/20 border border-red-800/30 text-sm text-[var(--danger)]">
      {{ error }}
    </div>

    <div v-if="loading && !data" class="flex items-center gap-2 text-[var(--text-secondary)]">
      <Loader2 class="w-4 h-4 animate-spin" />
      Checking…
    </div>

    <template v-if="data">
      <!-- Overall banner -->
      <div
        v-if="allGreen"
        class="mb-6 flex items-center gap-2 px-4 py-3 rounded bg-green-900/20 border border-green-800/30 text-sm text-[var(--success)]"
      >
        <CheckCircle2 class="w-4 h-4" />
        All required tools available and sysctls meet recommended minimums.
      </div>
      <div
        v-else
        class="mb-6 flex items-start gap-2 px-4 py-3 rounded bg-amber-900/20 border border-amber-800/30 text-sm text-[var(--warning)]"
      >
        <AlertTriangle class="w-4 h-4 mt-0.5 flex-shrink-0" />
        <div>
          Some prerequisites need attention. Review the details below.
        </div>
      </div>

      <!-- Tools -->
      <section class="mb-8">
        <h3 class="text-sm uppercase tracking-wide text-[var(--text-secondary)] mb-3">External tools</h3>
        <div class="bg-[var(--bg-surface)] rounded-lg border border-[var(--border)] overflow-hidden">
          <table class="w-full text-sm">
            <thead class="text-xs text-[var(--muted)] bg-[var(--bg-primary)]">
              <tr>
                <th class="text-left px-4 py-2 font-normal w-28">Tool</th>
                <th class="text-left px-4 py-2 font-normal">Version</th>
                <th class="text-left px-4 py-2 font-normal">Path</th>
                <th class="text-center px-4 py-2 font-normal w-28">Status</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="t in data.tools"
                :key="t.name"
                class="border-t border-[var(--border)]"
              >
                <td class="px-4 py-2">
                  <div class="flex items-center gap-2">
                    <Terminal class="w-3.5 h-3.5 text-[var(--muted)]" />
                    <span class="font-mono">{{ t.name }}</span>
                    <span v-if="!t.required" class="text-[10px] text-[var(--muted)] uppercase">optional</span>
                  </div>
                  <div class="text-xs text-[var(--text-secondary)] mt-1">{{ t.reason }}</div>
                </td>
                <td class="px-4 py-2 font-mono text-xs">
                  <span v-if="t.version" class="text-[var(--text-primary)]">{{ t.version }}</span>
                  <span v-else class="text-[var(--muted)]">—</span>
                </td>
                <td class="px-4 py-2 font-mono text-xs text-[var(--text-secondary)] break-all">
                  {{ t.path || '—' }}
                </td>
                <td class="px-4 py-2 text-center">
                  <span v-if="t.available" class="inline-flex items-center gap-1 text-[var(--success)] text-xs">
                    <CheckCircle2 class="w-3.5 h-3.5" /> OK
                  </span>
                  <span
                    v-else
                    class="inline-flex items-center gap-1 text-xs"
                    :class="t.required ? 'text-[var(--danger)]' : 'text-[var(--muted)]'"
                  >
                    <XCircle class="w-3.5 h-3.5" /> Missing
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- Sysctls -->
      <section class="mb-6">
        <div class="flex items-center justify-between mb-3">
          <h3 class="text-sm uppercase tracking-wide text-[var(--text-secondary)]">Kernel sysctls ({{ data.os }})</h3>
          <button
            v-if="hasSysctlFailures && data.can_apply"
            class="flex items-center gap-1.5 px-3 py-1.5 text-sm rounded bg-green-900/30 hover:bg-[var(--success)] text-green-300 hover:text-white transition-colors disabled:opacity-40"
            :disabled="applying"
            @click="apply"
          >
            <Zap v-if="!applying" class="w-3.5 h-3.5" />
            <Loader2 v-else class="w-3.5 h-3.5 animate-spin" />
            {{ applying ? 'Applying…' : 'Apply recommended settings' }}
          </button>
        </div>

        <!-- Apply disabled reason -->
        <div
          v-if="hasSysctlFailures && !data.can_apply"
          class="mb-3 px-4 py-2 rounded bg-[var(--bg-surface)] border border-[var(--border)] text-xs text-[var(--text-secondary)]"
        >
          Apply disabled: {{ data.apply_reason }}
        </div>

        <div class="bg-[var(--bg-surface)] rounded-lg border border-[var(--border)] overflow-hidden">
          <table class="w-full text-sm">
            <thead class="text-xs text-[var(--muted)] bg-[var(--bg-primary)]">
              <tr>
                <th class="text-left px-4 py-2 font-normal">Key</th>
                <th class="text-right px-4 py-2 font-normal w-24">Current</th>
                <th class="text-right px-4 py-2 font-normal w-24">Recommended</th>
                <th class="text-center px-4 py-2 font-normal w-24">Status</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="s in data.sysctls"
                :key="s.key"
                class="border-t border-[var(--border)]"
              >
                <td class="px-4 py-2">
                  <div class="font-mono text-xs">{{ s.key }}</div>
                  <div class="text-xs text-[var(--text-secondary)] mt-1">{{ s.reason }}</div>
                  <div v-if="s.error" class="text-xs text-[var(--danger)] mt-1">{{ s.error }}</div>
                </td>
                <td class="px-4 py-2 font-mono text-right">
                  <span v-if="s.current" :class="s.ok ? 'text-[var(--success)]' : 'text-[var(--danger)]'">{{ s.current }}</span>
                  <span v-else class="text-[var(--muted)]">—</span>
                </td>
                <td class="px-4 py-2 font-mono text-right text-[var(--text-secondary)]">{{ s.recommended }}</td>
                <td class="px-4 py-2 text-center">
                  <span v-if="s.ok" class="inline-flex items-center gap-1 text-[var(--success)] text-xs">
                    <CheckCircle2 class="w-3.5 h-3.5" /> OK
                  </span>
                  <span v-else class="inline-flex items-center gap-1 text-[var(--danger)] text-xs">
                    <XCircle class="w-3.5 h-3.5" /> Low
                  </span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- Apply results -->
        <div v-if="applyResults" class="mt-3 text-xs space-y-1">
          <div v-for="r in applyResults" :key="r.key" class="flex items-center gap-2">
            <CheckCircle2 v-if="r.applied" class="w-3.5 h-3.5 text-[var(--success)]" />
            <XCircle v-else class="w-3.5 h-3.5 text-[var(--danger)]" />
            <span class="font-mono">{{ r.key }}</span>
            <span v-if="r.applied" class="text-[var(--text-secondary)]">→ {{ r.new_value }}</span>
            <span v-else class="text-[var(--danger)]">{{ r.error }}</span>
          </div>
        </div>

        <!-- Persistence hint -->
        <div v-if="data.persist_hint && hasSysctlFailures" class="mt-4">
          <div class="text-xs text-[var(--text-secondary)] mb-2">
            The Apply button writes to <code class="font-mono">/proc/sys</code> and is lost on reboot. To persist:
          </div>
          <pre class="text-xs bg-[var(--bg-surface)] border border-[var(--border)] rounded p-3 overflow-x-auto font-mono text-[var(--text-secondary)] whitespace-pre-wrap">{{ data.persist_hint }}</pre>
        </div>
      </section>
    </template>
  </div>
</template>
