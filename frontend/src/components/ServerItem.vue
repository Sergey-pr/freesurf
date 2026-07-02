<template>
  <div class="server-item">
    <div class="server-header">
      <button
        v-if="collapsible"
        class="chevron-btn"
        @click="open = !open"
        :title="open ? 'Collapse' : 'Expand'"
      >
        <span class="chevron" :class="{ open }">›</span>
      </button>
      <span v-else class="chevron-spacer" />

      <!-- A subscription with no child nodes is itself selectable as a placeholder;
           otherwise the header just labels the group. -->
      <div
        class="server-name"
        :class="{ selectable: !hasNodes, selected: !hasNodes && selectedNodeId === 0 && false }"
      >
        {{ server.name }}
        <span v-if="hasNodes" class="server-count">{{ server.nodes.length }}</span>
        <span v-if="server.kind === 'subscription'" class="server-kind">sub</span>
        <span
          v-if="refreshError"
          class="refresh-error-badge"
          :title="refreshError"
        >⚠</span>
      </div>

      <div class="server-actions">
        <button
          v-if="server.url"
          class="btn-ping"
          title="Refresh subscription"
          :disabled="store.refreshing[server.id]"
          @click="store.refreshServer(server.id)"
        >REFRESH</button>
        <button
          v-if="hasNodes"
          class="btn-ping"
          title="Ping all nodes"
          @click="store.pingServer(server.id)"
        >PING</button>
        <button class="btn-icon server-del" title="Delete" @click="$emit('delete', server.id)">✕</button>
      </div>
    </div>

    <Transition name="refresh-overlay">
      <div v-if="store.refreshing[server.id]" class="refresh-overlay">
        <div class="refresh-spinner" />
      </div>
    </Transition>

    <div v-if="hasNodes && open" class="node-list">
      <div
        v-for="node in server.nodes"
        :key="node.id"
        class="node-row"
        :class="{ selected: node.id === selectedNodeId, active: node.id === activeNodeId }"
        @click="$emit('select', node.id)"
      >
        <span class="node-dot" />
        <span class="node-name">{{ node.name }}</span>
        <span class="node-proto">{{ node.protocol }}</span>
        <span v-if="nodeSecurity(node)" class="node-sec" :class="nodeSecurity(node)">{{ nodeSecurity(node) }}</span>
        <span v-if="nodeQuic(node)" class="node-quic" title="QUIC / HTTP-3 (UDP)">H3</span>
        <span v-if="node.id === activeNodeId" class="node-active-badge">on</span>
        <span class="node-ping" :class="pingClass(node.id)">{{ pingLabel(node.id) }}</span>
        <button
          class="btn-ping node-ping-btn"
          title="Ping"
          @click.stop="store.pingNode(node.id)"
        >PING</button>
      </div>
    </div>

    <div v-else-if="!hasNodes" class="node-empty">
      No nodes yet - they appear after the subscription is fetched.
    </div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useServerStore } from '../stores/serverStore.js'

const props = defineProps({
  server: { type: Object, required: true },
  selectedNodeId: { type: Number, default: 0 },
  activeNodeId: { type: Number, default: 0 },
})

defineEmits(['select', 'delete'])

const store = useServerStore()
const open = ref(true)
const hasNodes = computed(() => (props.server.nodes?.length ?? 0) > 0)
const collapsible = computed(() => (props.server.nodes?.length ?? 0) > 1)
const refreshError = computed(() => store.refreshErrors[props.server.id] ?? null)

// nodeSecurity derives the transport security (reality/tls/none/...) from a node's
// share URI, mirroring the backend detection in internal/proxy/config.go: a reality
// public key (pbk) implies reality even when security= is omitted.
function nodeSecurity(node) {
  if (!node.uri) return ''
  try {
    const q = new URL(node.uri).searchParams
    if (q.get('pbk')) return 'reality'
    const sec = (q.get('security') || '').toLowerCase()
    if (sec === '' || sec === 'none') return 'none'
    return sec
  } catch {
    return ''
  }
}

// nodeQuic reports whether the node runs over QUIC/UDP (HTTP/3), signalled by an
// h3 entry in the alpn list. These are probed over UDP, not TCP - mirrors quicNode
// in internal/ping.
function nodeQuic(node) {
  if (!node.uri) return false
  try {
    const alpn = new URL(node.uri).searchParams.get('alpn') || ''
    return alpn.split(',').map((a) => a.trim()).includes('h3')
  } catch {
    return false
  }
}

function pingLabel(id) {
  const p = store.pings[id]
  if (p === undefined) return ''
  if (p === 'ping') return '…'
  if (p < 0) return 'timeout'
  return `${p} ms`
}

function pingClass(id) {
  const p = store.pings[id]
  if (p === 'ping') return 'pinging'
  if (p === undefined) return ''
  if (p < 0) return 'bad'
  if (p < 150) return 'good'
  if (p < 400) return 'ok'
  return 'slow'
}
</script>

<style scoped>
.server-item {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
  position: relative;
}

.refresh-overlay {
  position: absolute;
  inset: 0;
  border-radius: 8px;
  background: rgba(0, 0, 0, 0.45);
  backdrop-filter: blur(2px);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 10;
  pointer-events: none;
}

.refresh-spinner {
  width: 22px;
  height: 22px;
  border: 2px solid rgba(255, 255, 255, 0.15);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
}

@keyframes spin { to { transform: rotate(360deg); } }

.refresh-overlay-enter-active,
.refresh-overlay-leave-active { transition: opacity 0.2s; }
.refresh-overlay-enter-from,
.refresh-overlay-leave-to { opacity: 0; }

.refresh-error-badge {
  font-size: 11px;
  color: var(--danger);
  cursor: default;
  flex-shrink: 0;
}

.server-header {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 10px;
}

.chevron-btn {
  background: none;
  color: var(--muted);
  padding: 0 2px;
  font-size: 14px;
  line-height: 1;
}
.chevron-btn:hover { color: var(--text); }
.chevron {
  display: inline-block;
  transition: transform 0.2s;
}
.chevron.open { transform: rotate(90deg); }
.chevron-spacer { width: 14px; display: inline-block; }

.server-name {
  flex: 1;
  font-size: 13px;
  font-weight: 500;
  color: var(--text);
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.server-name > :first-child { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

.server-count {
  font-size: 10px;
  color: var(--muted);
  background: var(--surface2);
  border-radius: 8px;
  padding: 1px 6px;
}

.server-kind {
  font-size: 9px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--accent);
  background: rgba(108,99,255,0.12);
  border-radius: 3px;
  padding: 1px 5px;
}

.server-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}

.server-del { font-size: 11px; }
.server-del:hover { color: var(--danger); }

.node-list {
  display: flex;
  flex-direction: column;
  border-top: 1px solid var(--border);
}

.node-row {
  display: flex;
  align-items: center;
  gap: 8px;
  background: none;
  border: none;
  border-radius: 0;
  padding: 8px 12px 8px 24px;
  text-align: left;
  width: 100%;
  color: var(--text);
  cursor: pointer;
}
.node-row:hover { background: var(--surface2); }
.node-row.selected { background: rgba(108,99,255,0.12); }
.node-row.selected .node-dot { background: var(--accent); border-color: var(--accent); }

.node-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  border: 1.5px solid var(--muted);
  flex-shrink: 0;
}
.node-row.active .node-dot { background: var(--success); border-color: var(--success); }

.node-name {
  flex: 1;
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.node-proto {
  font-size: 9px;
  text-transform: uppercase;
  color: var(--muted);
}

.node-sec {
  font-size: 9px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--muted);
  background: var(--surface2);
  border: 1px solid var(--border);
  border-radius: 3px;
  padding: 1px 5px;
}
.node-sec.reality {
  color: var(--accent);
  background: rgba(108,99,255,0.12);
  border-color: transparent;
}
.node-sec.tls {
  color: var(--success);
  background: rgba(62,207,142,0.12);
  border-color: transparent;
}
.node-sec.none {
  color: var(--muted);
}

.node-quic {
  font-size: 9px;
  font-weight: 600;
  letter-spacing: 0.03em;
  color: var(--warning);
  background: rgba(240, 180, 41, 0.14);
  border-radius: 3px;
  padding: 1px 5px;
  cursor: default;
}

.node-active-badge {
  font-size: 9px;
  text-transform: uppercase;
  color: var(--success);
  background: rgba(62,207,142,0.15);
  border-radius: 3px;
  padding: 1px 5px;
}

.node-ping {
  font-size: 10px;
  font-variant-numeric: tabular-nums;
  color: var(--muted);
  min-width: 30px;
  text-align: right;
}
.node-ping.good { color: var(--success); }
.node-ping.ok { color: var(--warning); }
.node-ping.slow { color: var(--danger); }
.node-ping.bad { color: var(--danger); opacity: 0.7; }
.node-ping.pinging { color: var(--muted); }

.btn-ping {
  font-size: 9px;
  font-weight: 600;
  letter-spacing: 0.05em;
  color: var(--muted);
  background: var(--surface2);
  border: 1px solid var(--border);
  border-radius: 4px;
  padding: 2px 7px;
  flex-shrink: 0;
  cursor: pointer;
}
.btn-ping:hover { color: var(--accent); border-color: var(--accent); }
.btn-ping:disabled { opacity: 0.5; cursor: default; }
.btn-ping:disabled:hover { color: var(--muted); border-color: var(--border); }

.node-ping-btn { padding: 1px 6px; }

.node-empty {
  font-size: 11px;
  color: var(--muted);
  padding: 4px 12px 10px 24px;
}
</style>
