<template>
  <Teleport to="body">
    <div v-if="visible" class="settings-overlay" @click.self="emit('close')">
      <div class="settings-dialog">
        <div class="settings-title">Settings</div>

        <label class="settings-field">
          <span class="field-label">Auto-refresh subscriptions (minutes)</span>
          <input
            class="field-input"
            type="number"
            min="1"
            v-model.number="minutes"
          />
        </label>

        <div class="settings-actions">
          <button class="btn-cancel" @click="emit('close')">Cancel</button>
          <button class="btn-primary" @click="save">Save</button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup>
import { ref, watch } from 'vue'
import { GetAutoRefreshMinutes, SetAutoRefreshMinutes } from '../../bindings/freesurf/app.js'

const props = defineProps({
  visible: Boolean,
})
const emit = defineEmits(['close'])

const minutes = ref(30)

watch(
  () => props.visible,
  async (open) => {
    if (open) {
      minutes.value = await GetAutoRefreshMinutes()
    }
  }
)

async function save() {
  const v = Math.max(1, Math.round(Number(minutes.value) || 30))
  minutes.value = await SetAutoRefreshMinutes(v)
  emit('close')
}
</script>

<style scoped>
.settings-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
}

.settings-dialog {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 20px 24px;
  min-width: 260px;
  max-width: 320px;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.6);
}

.settings-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
  margin-bottom: 18px;
}

.settings-field {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-bottom: 20px;
}

.field-label {
  font-size: 12px;
  color: var(--muted);
}

.field-input {
  background: var(--surface2);
  border: 1px solid var(--border);
  border-radius: 6px;
  color: var(--text);
  font-size: 13px;
  padding: 7px 10px;
}
.field-input:focus {
  outline: none;
  border-color: var(--accent);
}

.settings-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.btn-cancel {
  background: transparent;
  border: 1px solid var(--border);
  color: var(--muted);
  font-size: 12px;
  padding: 5px 14px;
  border-radius: 6px;
}
.btn-cancel:hover { border-color: var(--muted); color: var(--text); }

.btn-primary {
  background: var(--accent);
  border: none;
  color: #fff;
  font-size: 12px;
  font-weight: 600;
  padding: 5px 14px;
  border-radius: 6px;
}
.btn-primary:hover { opacity: 0.9; }
</style>
