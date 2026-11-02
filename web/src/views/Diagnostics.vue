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

const LEVEL_TAG: Record<Level, 'success' | 'warning' | 'danger'> = {
  pass: 'success',
  warn: 'warning',
  fail: 'danger',
}

function levelText(level: Level): string {
  return LEVEL_TEXT[level]
}
</script>

<template>
  <div class="page">
    <el-alert v-if="error" type="error" :closable="false" show-icon>
      <template #title>
        <div class="alert-row">
          <div>
            <strong>自检未完成</strong>
            <p class="alert-detail">{{ error }}</p>
          </div>
          <el-button size="small" @click="load()">重试</el-button>
        </div>
      </template>
    </el-alert>

    <el-skeleton v-else-if="loading && groups.length === 0" :rows="5" animated />

    <template v-else>
      <p class="meta">
        <span v-if="generatedAt">生成时间 {{ generatedAt }}</span>
        <span class="dot-sep">·</span>
        <span>结果来自服务端实时检测，非缓存</span>
      </p>

      <el-card v-for="group in groups" :key="group.name" shadow="never" class="group">
        <template #header>{{ group.name }}</template>
        <div v-for="item in group.items" :key="item.id" class="item">
          <el-tag class="badge" size="small" :type="LEVEL_TAG[item.level]" effect="light">
            {{ levelText(item.level) }}
          </el-tag>
          <div class="body">
            <div class="head">
              <strong>{{ item.name }}</strong>
              <el-tag v-if="item.ref" size="small" type="info" effect="plain" class="ref">{{ item.ref }}</el-tag>
            </div>
            <p class="detail" :class="{ bad: item.level === 'fail' }">{{ item.detail }}</p>
          </div>
        </div>
      </el-card>
    </template>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 1100px;
  margin: 0 auto;
}

.meta {
  margin: 0;
  color: var(--fg-muted);
  font-size: 13px;
}

.dot-sep {
  margin: 0 6px;
}

.group :deep(.el-card__header) {
  padding: 10px 16px;
  color: var(--fg-muted);
  font-size: 13px;
  font-weight: 600;
  letter-spacing: 0.02em;
}

.group :deep(.el-card__body) {
  padding: 0;
}

.item {
  display: flex;
  gap: 12px;
  padding: 11px 16px;
  border-bottom: 1px solid var(--line);
}

.item:last-child {
  border-bottom: 0;
}

.badge {
  flex: none;
  align-self: flex-start;
  min-width: 52px;
  justify-content: center;
}

.body {
  min-width: 0;
}

.head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.head strong {
  font-size: 13.5px;
  font-weight: 600;
}

.ref {
  font-family: 'SF Mono', ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 10px;
}

.detail {
  margin: 2px 0 0;
  color: var(--fg-muted);
  font-size: 13px;
  line-height: 1.7;
  word-break: break-word;
}

.detail.bad {
  color: var(--bad);
}

.alert-row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  width: 100%;
}

.alert-detail {
  margin: 2px 0 0;
  color: var(--fg-muted);
  font-size: 13px;
}
</style>
