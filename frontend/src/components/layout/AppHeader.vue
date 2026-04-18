<script setup>
import { computed } from 'vue'
import { useVmStore } from '../../stores/vmStore.js'
import { useChatStore } from '../../stores/chatStore.js'
import { logout } from '../../api/client.js'
import { Server, LogOut, MessageSquare, ChevronRight } from 'lucide-vue-next'

const store = useVmStore()
const chatStore = useChatStore()

// Breadcrumb label: strip the `kind-` prefix so "kind-prod" reads as
// "prod" — consistent with the sidebar switcher chips. Falls back to
// the raw context, then to hostname, then to a neutral placeholder
// when no cluster is active yet.
const clusterLabel = computed(() => {
  const ctx = store.activeContext
  if (!ctx) return 'no cluster'
  return ctx.startsWith('kind-') ? ctx.slice(5) : ctx
})

const clusterColor = computed(() => store.activeClusterColor)
const clusterTag = computed(() => store.activeClusterTag)

function jumpToCluster() {
  store.selectNode(null)
}

async function handleLogout() {
  try { await logout() } catch { /* ignore */ }
  chatStore.clearHistory()
  store.authenticated = false
}
</script>

<template>
  <header class="flex items-center justify-between px-4 py-2 bg-[var(--bg-secondary)] border-b border-[var(--border)]">
    <div class="flex items-center gap-3 min-w-0">
      <Server class="w-5 h-5 text-[var(--accent)] flex-shrink-0" />
      <span class="font-semibold text-lg flex-shrink-0">KubeGo</span>

      <!-- Active-cluster breadcrumb: persistent reminder of which
           environment you're operating on. Colour + tag come from
           ClusterMeta; clicking jumps to the cluster's overview. -->
      <ChevronRight class="w-4 h-4 text-[var(--muted)] flex-shrink-0" />
      <button
        class="group flex items-center gap-1.5 px-2 py-1 rounded hover:bg-[var(--bg-hover)] transition-colors min-w-0"
        :style="clusterColor ? {
          borderLeft: `2px solid ${clusterColor}`,
          paddingLeft: '7px',
        } : {}"
        :title="store.activeContext || 'Select a cluster from the sidebar'"
        @click="jumpToCluster"
      >
        <span
          class="w-2 h-2 rounded-full flex-shrink-0"
          :style="{ background: clusterColor || 'var(--muted)' }"
        />
        <span class="text-sm truncate">{{ clusterLabel }}</span>
        <span
          v-if="clusterTag"
          class="px-1 py-0 text-[9px] uppercase rounded flex-shrink-0"
          :style="clusterColor
            ? { background: `${clusterColor}33`, color: clusterColor, border: `1px solid ${clusterColor}66` }
            : { background: 'var(--bg-surface)', color: 'var(--muted)' }"
        >{{ clusterTag }}</span>
      </button>
    </div>
    <div class="flex items-center gap-2 text-sm text-[var(--text-secondary)]">
      <button
        @click="chatStore.togglePanel"
        class="flex items-center gap-1.5 px-2 py-1 rounded hover:bg-[var(--bg-hover)] transition-colors"
        :class="{ 'text-[var(--accent)]': chatStore.isOpen }"
        title="AI Chat"
      >
        <MessageSquare class="w-4 h-4" />
      </button>
      <button
        @click="handleLogout"
        class="flex items-center gap-1.5 px-2 py-1 rounded hover:bg-[var(--bg-hover)] transition-colors"
        title="Logout"
      >
        <LogOut class="w-4 h-4" />
      </button>
    </div>
  </header>
</template>
