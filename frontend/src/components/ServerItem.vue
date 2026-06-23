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
      </div>

      <button class="btn-icon server-del" title="Delete" @click="$emit('delete', server.id)">✕</button>
    </div>

    <div v-if="hasNodes && open" class="node-list">
      <button
        v-for="node in server.nodes"
        :key="node.id"
        class="node-row"
        :class="{ selected: node.id === selectedNodeId, active: node.id === activeNodeId }"
        @click="$emit('select', node.id)"
      >
        <span class="node-dot" />
        <span class="node-name">{{ node.name }}</span>
        <span class="node-proto">{{ node.protocol }}</span>
        <span v-if="node.id === activeNodeId" class="node-active-badge">on</span>
      </button>
    </div>

    <div v-else-if="!hasNodes" class="node-empty">
      No nodes yet — they appear after the subscription is fetched.
    </div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'

const props = defineProps({
  server: { type: Object, required: true },
  selectedNodeId: { type: Number, default: 0 },
  activeNodeId: { type: Number, default: 0 },
})

defineEmits(['select', 'delete'])

const open = ref(true)
const hasNodes = computed(() => (props.server.nodes?.length ?? 0) > 0)
const collapsible = computed(() => (props.server.nodes?.length ?? 0) > 1)
</script>

<style scoped>
.server-item {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  overflow: hidden;
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

.server-del { flex-shrink: 0; font-size: 11px; }
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

.node-active-badge {
  font-size: 9px;
  text-transform: uppercase;
  color: var(--success);
  background: rgba(62,207,142,0.15);
  border-radius: 3px;
  padding: 1px 5px;
}

.node-empty {
  font-size: 11px;
  color: var(--muted);
  padding: 4px 12px 10px 24px;
}
</style>
