<script setup lang="ts">
/** 引擎页签。MongoDB 适配完成前不展示入口，避免用户点进空页面。 */
import type { Engine } from '../engine'

const props = defineProps<{ modelValue: Engine }>()
const emit = defineEmits<{ 'update:modelValue': [Engine] }>()

const ENGINES: { key: Engine; label: string; ready: boolean }[] = [
  { key: 'mysql', label: 'MySQL', ready: true },
  { key: 'postgresql', label: 'PostgreSQL', ready: true },
  { key: 'mongodb', label: 'MongoDB', ready: true },
]

function pick(key: Engine): void {
  if (key === props.modelValue) return
  emit('update:modelValue', key)
}
</script>

<template>
  <nav class="engine-tabs">
    <button
      v-for="e in ENGINES"
      :key="e.key"
      type="button"
      class="engine-tab"
      :class="{ active: modelValue === e.key }"
      @click="pick(e.key)"
    >
      {{ e.label }}
    </button>
  </nav>
</template>

<style scoped>
.engine-tabs {
  display: flex;
  gap: 4px;
  padding: 4px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--bg-elevated);
  align-self: flex-start;
}

.engine-tab {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--fg-muted);
  font-size: 13.5px;
}

.engine-tab.active {
  background: var(--bg);
  color: var(--fg);
  box-shadow: 0 1px 2px rgb(0 0 0 / 8%);
}

@media (max-width: 720px) {
  .engine-tabs {
    align-self: stretch;
  }

  .engine-tab {
    flex: 1;
    justify-content: center;
    padding: 7px 8px;
  }
}
</style>
