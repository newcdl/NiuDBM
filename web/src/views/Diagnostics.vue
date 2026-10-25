<script setup lang="ts">
import { computed } from 'vue'
import { LEVEL_TEXT, type Level } from '../api'
import { useDiag } from '../useDiag'

const { report, loading, error, load } = useDiag()

const groups = computed(() => report.value?.groups ?? [])
const generatedAt = computed<string>(() => {
  const raw = report.value?.generatedAt
  if (!raw) return ''
  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) return raw
  return date.toLocaleString('zh-CN', { hour12: false })
})

function levelText(level: Level): string {
  return LEVEL_TEXT[level]
}
</script>

<template>
  <div class="page">
    <div v-if="error" class="notice notice-bad">
      <div>
        <strong>自检未完成</strong>
        <p>{{ error }}</p>
      </div>
      <button type="button" class="btn" @click="load()">重试</button>
    </div>

    <div v-else-if="loading && groups.length === 0" class="notice">正在检测…</div>

    <template v-else>
      <p class="meta">
        <span v-if="generatedAt">生成时间 {{ generatedAt }}</span>
        <span class="dot-sep">·</span>
        <span>结果来自服务端实时检测，非缓存</span>
      </p>

      <section v-for="group in groups" :key="group.name" class="group">
        <h2>{{ group.name }}</h2>
        <ul class="list">
          <li v-for="item in group.items" :key="item.id" class="item" :class="`lv-${item.level}`">
            <span class="badge">{{ levelText(item.level) }}</span>
            <div class="body">
              <div class="head">
                <strong>{{ item.name }}</strong>
                <code v-if="item.ref" class="ref">{{ item.ref }}</code>
              </div>
              <p class="detail">{{ item.detail }}</p>
            </div>
          </li>
        </ul>
      </section>
    </template>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 18px;
  max-width: 880px;
}

.meta {
  margin: 0;
  color: var(--fg-muted);
  font-size: 12px;
}

.dot-sep {
  margin: 0 6px;
}

.group h2 {
  margin: 0 0 8px;
  font-size: 12px;
  font-weight: 600;
  color: var(--fg-muted);
  letter-spacing: 0.02em;
}

.list {
  margin: 0;
  padding: 0;
  list-style: none;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
  overflow: hidden;
}

.item {
  display: flex;
  gap: 12px;
  padding: 11px 14px;
  border-bottom: 1px solid var(--line);
}

.item:last-child {
  border-bottom: 0;
}

.badge {
  flex: none;
  align-self: flex-start;
  min-width: 52px;
  padding: 2px 8px;
  border-radius: 20px;
  font-size: 11px;
  text-align: center;
}

.lv-pass .badge {
  background: var(--ok-bg);
  color: var(--ok);
}

.lv-warn .badge {
  background: var(--warn-bg);
  color: var(--warn);
}

.lv-fail .badge {
  background: var(--bad-bg);
  color: var(--bad);
}

.body {
  min-width: 0;
}

.head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.head strong {
  font-size: 13px;
  font-weight: 600;
}

.ref {
  padding: 0 5px;
  border-radius: 4px;
  background: var(--bg);
  color: var(--fg-muted);
  font-size: 10px;
}

.detail {
  margin: 2px 0 0;
  color: var(--fg-muted);
  font-size: 12px;
  word-break: break-word;
}

.lv-fail .detail {
  color: var(--bad);
}

.notice {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
  font-size: 13px;
}

.notice p {
  margin: 4px 0 0;
  color: var(--fg-muted);
  font-size: 12px;
}

.notice-bad {
  border-color: var(--bad);
  background: var(--bad-bg);
}

.btn {
  padding: 6px 12px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--bg-elevated);
  font-size: 12px;
}
</style>
