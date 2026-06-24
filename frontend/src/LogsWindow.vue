<template>
  <div id="logs-root">
    <div class="logs-toolbar">
      <span class="logs-title">Logs</span>
      <div class="logs-actions">
        <button class="btn-ghost" @click="copy">{{ copied ? 'Copied' : 'Copy' }}</button>
        <button class="btn-ghost" @click="clear">Clear</button>
      </div>
    </div>
    <pre ref="pane" class="logs-pane">{{ text || 'No log output yet. Press Start to connect.' }}</pre>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import { Events } from '@wailsio/runtime'
import { GetLog, ClearLog } from '../bindings/free-surf/app.js'

const text = ref('')
const copied = ref(false)
const pane = ref(null)
let offLine, offCleared

function scrollToBottom() {
  nextTick(() => {
    if (pane.value) pane.value.scrollTop = pane.value.scrollHeight
  })
}

async function load() {
  text.value = await GetLog()
  scrollToBottom()
}

function append(line) {
  text.value = text.value ? text.value + '\n' + line : line
  scrollToBottom()
}

async function clear() {
  await ClearLog()
  text.value = ''
}

async function copy() {
  try {
    await navigator.clipboard.writeText(text.value)
    copied.value = true
    setTimeout(() => (copied.value = false), 1200)
  } catch (_) { /* clipboard may be unavailable */ }
}

onMounted(() => {
  load()
  offLine = Events.On('log:line', ev => append(ev.data))
  offCleared = Events.On('log:cleared', () => { text.value = '' })
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
  padding: 10px 12px;
  overflow: auto;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 11.5px;
  line-height: 1.55;
  color: var(--text);
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
