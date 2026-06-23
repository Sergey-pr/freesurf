<template>
  <div id="app-root">
    <div class="titlebar">
      <h1>FreeSurf</h1>

      <div class="add-wrap" ref="addWrap">
        <button class="add-btn" title="Add server" @click="menuOpen = !menuOpen">+</button>
        <div v-if="menuOpen" class="add-menu">
          <button class="add-menu-item" @click="handleAdd">
            Paste from clipboard
          </button>
        </div>
      </div>
    </div>

    <div class="main-content">
      <!-- Big connect button -->
      <div class="hero">
        <button
          class="power-btn"
          :class="{ connected: store.conn.connected }"
          :disabled="!store.conn.connected && !store.selectedNodeId"
          @click="store.toggleConnection()"
        >
          <span class="power-glyph">⏻</span>
          <span class="power-label">{{ store.conn.connected ? 'Stop' : 'Start' }}</span>
        </button>

        <div class="hero-status">
          <template v-if="store.conn.connected">
            <span class="status-dot on" />
            Connected{{ store.activeNode ? ' · ' + store.activeNode.name : '' }}
          </template>
          <template v-else-if="store.selectedNode">
            <span class="status-dot" />
            {{ store.selectedNode.name }}
          </template>
          <template v-else>
            <span class="status-dot" />
            No server selected
          </template>
        </div>
      </div>

      <!-- Server list -->
      <div class="server-section">
        <div class="section-header">Servers</div>

        <div v-if="!store.servers.length" class="empty-state">
          No servers yet. Copy a subscription link or share URI, then tap
          <strong>+</strong> → <em>Paste from clipboard</em>.
        </div>

        <div v-else class="server-list">
          <ServerItem
            v-for="server in store.servers"
            :key="server.id"
            :server="server"
            :selected-node-id="store.selectedNodeId"
            :active-node-id="store.conn.connected ? store.conn.nodeId : 0"
            @select="store.select($event)"
            @delete="store.deleteServer($event)"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { useServerStore } from './stores/serverStore.js'
import ServerItem from './components/ServerItem.vue'

const store = useServerStore()
const menuOpen = ref(false)
const addWrap = ref(null)

async function handleAdd() {
  menuOpen.value = false
  await store.addFromClipboard()
}

function onDocClick(e) {
  if (addWrap.value && !addWrap.value.contains(e.target)) {
    menuOpen.value = false
  }
}

onMounted(async () => {
  await store.load()
  await store.refreshConn()
  document.addEventListener('click', onDocClick)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick)
})
</script>

<style scoped>
/* Add (+) menu */
.add-wrap {
  margin-left: auto;
  position: relative;
  -webkit-app-region: no-drag;
}

.add-btn {
  background: transparent;
  color: var(--muted);
  font-size: 20px;
  line-height: 1;
  padding: 2px 8px;
  border-radius: 6px;
}
.add-btn:hover { color: var(--text); background: var(--surface2); }

.add-menu {
  position: absolute;
  top: 30px;
  right: 0;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
  box-shadow: 0 8px 24px rgba(0,0,0,0.5);
  min-width: 180px;
  padding: 4px;
  z-index: 50;
}

.add-menu-item {
  display: block;
  width: 100%;
  text-align: left;
  background: none;
  color: var(--text);
  font-size: 12px;
  padding: 8px 10px;
  border-radius: 6px;
}
.add-menu-item:hover { background: var(--surface2); }

/* Hero / power button */
.hero {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 14px;
  padding: 12px 0 4px;
}

.power-btn {
  width: 140px;
  height: 140px;
  border-radius: 50%;
  background: var(--surface);
  border: 2px solid var(--border);
  color: var(--muted);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  transition: all 0.2s;
  box-shadow: 0 0 0 0 rgba(108,99,255,0.0);
}
.power-btn:not(:disabled):hover {
  border-color: var(--accent);
  color: var(--text);
}
.power-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.power-btn.connected {
  border-color: var(--success);
  color: var(--success);
  background: rgba(62,207,142,0.08);
  box-shadow: 0 0 0 6px rgba(62,207,142,0.08);
}

.power-glyph { font-size: 44px; line-height: 1; }
.power-label {
  font-size: 13px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.1em;
}

.hero-status {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 12px;
  color: var(--muted);
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--muted);
  flex-shrink: 0;
}
.status-dot.on { background: var(--success); }

/* Server section */
.section-header {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--muted);
  padding: 0 2px 8px;
}

.server-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.empty-state {
  font-size: 12px;
  color: var(--muted);
  line-height: 1.6;
  text-align: center;
  padding: 24px 16px;
  border: 1px dashed var(--border);
  border-radius: 8px;
}
.empty-state strong { color: var(--accent); }
.empty-state em { color: var(--text); font-style: normal; }
</style>
