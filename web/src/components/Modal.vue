<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'

const props = defineProps<{
  title: string
  /** 提交中时禁止关闭，避免请求还没回来就把界面抽走 */
  busy?: boolean
}>()

const emit = defineEmits<{ close: [] }>()

function close(): void {
  if (!props.busy) emit('close')
}

function onKey(e: KeyboardEvent): void {
  if (e.key === 'Escape') close()
}

onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="mask" @click.self="close">
    <div class="dialog" role="dialog" aria-modal="true">
      <header class="head">
        <h2>{{ title }}</h2>
        <button type="button" class="x" :disabled="busy" @click="close">关闭</button>
      </header>
      <div class="body">
        <slot />
      </div>
      <footer class="foot">
        <slot name="foot" />
      </footer>
    </div>
  </div>
</template>

<style scoped>
.mask {
  position: fixed;
  inset: 0;
  z-index: 40;
  display: grid;
  place-items: center;
  padding: 20px;
  background: rgba(16, 24, 40, 0.42);
}

.dialog {
  display: flex;
  flex-direction: column;
  width: min(560px, 100%);
  max-height: calc(100vh - 60px);
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
  box-shadow: 0 18px 48px rgba(16, 24, 40, 0.22);
}

.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 18px;
  border-bottom: 1px solid var(--line);
}

.head h2 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
}

.x {
  border: 0;
  background: transparent;
  color: var(--fg-muted);
  font-size: 12px;
}

.body {
  padding: 16px 18px;
  overflow: auto;
}

.foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px 18px;
  border-top: 1px solid var(--line);
}
</style>
