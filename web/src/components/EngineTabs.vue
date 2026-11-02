<script setup lang="ts">
/**
 * 引擎页签。目前只有 MySQL 可用，PostgreSQL 与 MongoDB 先占位，
 * 让"以后会支持什么"在界面上有明确的落点，而不是等做完再突然多出来。
 */
export type Engine = 'mysql' | 'postgresql' | 'mongodb'

const props = defineProps<{ modelValue: Engine }>()
const emit = defineEmits<{ 'update:modelValue': [Engine] }>()

const ENGINES: { key: Engine; label: string; ready: boolean }[] = [
  { key: 'mysql', label: 'MySQL', ready: true },
  { key: 'postgresql', label: 'PostgreSQL', ready: false },
  { key: 'mongodb', label: 'MongoDB', ready: false },
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
      <span v-if="!e.ready" class="wip">开发中</span>
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

.wip {
  padding: 1px 5px;
  border-radius: 4px;
  background: rgb(240 180 60 / 18%);
  color: #a86b00;
  font-size: 11px;
  font-style: normal;
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
