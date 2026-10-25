<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, type Table } from '../api'
import { errText } from '../format'

const props = defineProps<{ instanceId: string }>()

const tables = ref<Table[]>([])
const activeName = ref('')
const loading = ref(false)
const error = ref('')

const active = computed<Table | null>(
  () => tables.value.find((t) => t.name === activeName.value) ?? null,
)

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const data = await api.schema(props.instanceId)
    tables.value = data.tables
    // 刷新后尽量保住当前选中的表，避免用户正看着列定义被重置
    if (!tables.value.some((t) => t.name === activeName.value)) {
      activeName.value = tables.value[0]?.name ?? ''
    }
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)

defineExpose({ load })
</script>

<template>
  <div class="panel">
    <div v-if="error" class="notice notice-bad">
      <span>{{ error }}</span>
      <button type="button" class="btn btn-sm" @click="load()">重试</button>
    </div>

    <div v-else-if="!loading && tables.length === 0" class="empty">
      <strong>还没有表</strong>
      <span>到「SQL 控制台」执行 CREATE TABLE 就能看到结构。</span>
    </div>

    <div v-else class="split">
      <aside class="list">
        <div class="list-head">
          <span>表与视图</span>
          <button type="button" class="btn btn-sm" :disabled="loading" @click="load()">
            {{ loading ? '…' : '刷新' }}
          </button>
        </div>
        <button
          v-for="t in tables"
          :key="t.name"
          type="button"
          class="item"
          :class="{ active: t.name === activeName }"
          @click="activeName = t.name"
        >
          <span class="mono name">{{ t.name }}</span>
          <span class="pill">{{ t.type === 'view' ? '视图' : `${t.rows} 行` }}</span>
        </button>
      </aside>

      <section v-if="active" class="detail card">
        <div class="detail-head">
          <h2 class="mono">{{ active.name }}</h2>
          <span class="pill pill-accent">{{ active.type === 'view' ? '视图' : '表' }}</span>
        </div>
        <table class="data cols">
          <thead>
            <tr>
              <th>列名</th>
              <th>类型</th>
              <th>约束</th>
              <th>默认值</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="c in active.columns" :key="c.name">
              <td class="mono">{{ c.name }}</td>
              <td class="mono muted">{{ c.type || '—' }}</td>
              <td>
                <span v-if="c.pk" class="pill pill-accent">主键</span>
                <span v-if="c.notNull" class="pill">非空</span>
                <span v-if="!c.pk && !c.notNull" class="muted">—</span>
              </td>
              <td class="mono muted">{{ c.default ?? '—' }}</td>
            </tr>
          </tbody>
        </table>
      </section>
    </div>
  </div>
</template>

<style scoped>
.panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.split {
  display: grid;
  grid-template-columns: 220px minmax(0, 1fr);
  gap: 12px;
  align-items: start;
}

.list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
  max-height: 520px;
  overflow: auto;
}

.list-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 2px 4px 8px;
  color: var(--fg-muted);
  font-size: 11px;
}

.item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 6px 8px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  text-align: left;
}

.item:hover {
  background: var(--bg);
}

.item.active {
  background: var(--accent-soft);
  color: var(--accent);
}

.item .name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.detail-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}

.detail-head h2 {
  margin: 0;
  font-size: 13px;
  color: var(--fg);
}

.cols {
  border: 0;
}

@media (max-width: 720px) {
  .split {
    grid-template-columns: 1fr;
  }
}
</style>
