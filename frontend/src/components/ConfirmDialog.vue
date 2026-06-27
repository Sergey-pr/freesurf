<template>
  <Teleport to="body">
    <div v-if="visible" class="confirm-overlay" @click.self="emit('cancel')">
      <div class="confirm-dialog">
        <div class="confirm-message">{{ message }}</div>
        <div class="confirm-actions">
          <button class="btn-cancel" @click="emit('cancel')">Cancel</button>
          <button class="btn-danger" @click="emit('confirm')">Delete</button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup>
defineProps({
  visible: Boolean,
  message: { type: String, default: 'Are you sure?' },
})
const emit = defineEmits(['confirm', 'cancel'])
</script>

<style scoped>
.confirm-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 100;
}

.confirm-dialog {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 20px 24px;
  min-width: 240px;
  max-width: 300px;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.6);
}

.confirm-message {
  font-size: 13px;
  color: var(--text);
  margin-bottom: 18px;
  line-height: 1.5;
}

.confirm-actions {
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

.btn-danger {
  background: var(--danger);
  border: none;
  color: #fff;
  font-size: 12px;
  font-weight: 600;
  padding: 5px 14px;
  border-radius: 6px;
}
.btn-danger:hover { background: #e03e3e; }
</style>
