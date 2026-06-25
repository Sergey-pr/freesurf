<template>
  <div id="logs-root">
    <div class="logs-toolbar">
      <span class="logs-title">Logs</span>
      <div class="logs-actions">
        <button class="btn-ghost" @click="copy">{{ copied ? 'Copied' : 'Copy' }}</button>
        <button class="btn-ghost" @click="clear">Clear</button>
      </div>
    </div>
    <div ref="pane" class="logs-pane">
      <div v-if="!entries.length" class="logs-empty">No log output yet. Press Start to connect.</div>
      <div v-for="e in entries" :key="e.key" class="logs-row">
        <span class="logs-time">{{ e.time }}</span>
        <span class="logs-msg">{{ e.msg }}</span>
        <span v-if="e.count > 1" class="logs-count">×{{ e.count }}</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import { Events } from '@wailsio/runtime'
import { GetLog, ClearLog } from '../bindings/freesurf/app.js'

// entries holds one row per unique message; repeats bump the count and refresh
// the timestamp instead of adding another line.
const entries = ref([])
const copied = ref(false)
const pane = ref(null)
let offLine, offCleared
const byMsg = new Map() // msg text -> entry object

function scrollToBottom() {
  nextTick(() => {
    if (pane.value) pane.value.scrollTop = pane.value.scrollHeight
  })
}

function reset() {
  entries.value = []
  byMsg.clear()
}

// ingest folds a raw buffer line ("HH:MM:SS  message") into the grouped view.
function ingest(line) {
  const m = line.match(/^(\d{2}:\d{2}:\d{2})\s+([\s\S]*)$/)
  const time = m ? m[1] : ''
  const msg = m ? m[2] : line
  const existing = byMsg.get(msg)
  if (existing) {
    existing.time = time
    existing.count++
    // Move the refreshed entry to the bottom so it reads newest-last.
    const i = entries.value.indexOf(existing)
    if (i !== -1 && i !== entries.value.length - 1) {
      entries.value.splice(i, 1)
      entries.value.push(existing)
    }
  } else {
    const entry = { key: msg, msg, time, count: 1 }
    byMsg.set(msg, entry)
    entries.value.push(entry)
  }
}

async function load() {
  reset()
  const text = await GetLog()
  if (text) text.split('\n').forEach(ingest)
  scrollToBottom()
}

async function clear() {
  await ClearLog()
  reset()
}

async function copy() {
  const text = entries.value
    .map(e => `${e.time}  ${e.msg}${e.count > 1 ? `  (×${e.count})` : ''}`)
    .join('\n')
  try {
    await navigator.clipboard.writeText(text)
    copied.value = true
    setTimeout(() => (copied.value = false), 1200)
  } catch (_) { /* clipboard may be unavailable */ }
}

onMounted(() => {
  load()
  offLine = Events.On('log:line', ev => { ingest(ev.data); scrollToBottom() })
  offCleared = Events.On('log:cleared', reset)
})

onUnmounted(() => {
  offLine?.()
  offCleared?.()
})
</script>

<style scoped>
#logs-root {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: var(--bg);
}

.logs-toolbar {
  height: 38px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 12px;
  background: var(--surface);
  border-bottom: 1px solid var(--border);
}

.logs-title {
  font-size: 12px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--muted);
}

.logs-actions { display: flex; gap: 6px; }

.logs-pane {
  flex: 1;
  margin: 0;
  padding: 6px 12px;
  overflow: auto;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 11.5px;
  line-height: 1.55;
  color: var(--text);
}

.logs-empty { color: var(--muted); padding: 4px 0; }

.logs-row {
  display: flex;
  gap: 8px;
  align-items: baseline;
  padding: 1px 0;
  word-break: break-word;
}

.logs-time {
  flex-shrink: 0;
  color: var(--muted);
  font-variant-numeric: tabular-nums;
}

.logs-msg {
  flex: 1;
  white-space: pre-wrap;
}

.logs-count {
  flex-shrink: 0;
  align-self: center;
  padding: 0 6px;
  border-radius: 8px;
  background: var(--surface);
  border: 1px solid var(--border);
  color: var(--muted);
  font-size: 10.5px;
  font-variant-numeric: tabular-nums;
}
</style>
