<template>
  <div id="error-root">
    <div class="error-icon">⚠</div>
    <div class="error-message">{{ message }}</div>
    <button class="btn-danger-solid" @click="close">OK</button>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { Events } from '@wailsio/runtime'
import { CloseErrorWindow } from '../bindings/freesurf/app.js'

const message = ref('')

let offError
onMounted(() => {
  offError = Events.On('app:error', (ev) => {
    message.value = ev.data ?? 'An unexpected error occurred.'
  })
})

onUnmounted(() => {
  offError?.()
})

function close() {
  CloseErrorWindow()
}
</script>
