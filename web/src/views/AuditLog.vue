<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, type AuditRecord } from '../api'
import { errText, formatTime } from '../format'

const items = ref<AuditRecord[]>([])
const loading = ref(false)
const error = ref('')
const limit = ref(100)

const ACTION_TEXT: Record<string, string> = {
  'instance.create': '创建实例',
  'instance.rename': '重命名实例',
  'instance.delete': '删除实例',
  'instance.query.exec': '执行写操作',
  'backup.create': '创建备份',
  'backup.restore': '恢复备份',
  'backup.delete': '删除备份',
}

const RESULT_TEXT: Record<string, string> = {
  ok: '成功',
  failed: '失败',
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const data = await api.audit(limit.value)
    items.value = data.items
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}

function actionText(action: string): string {
  return ACTION_TEXT[action] ?? action
}

onMounted(load)
</script>

<template>
  <div class="page">
    <div class="toolbar">
      <div class="grow">
        <p class="lead">所有会改变数据的操作都会留痕，便于回溯「谁在什么时候动了什么」。</p>
        <p class="meta muted">共 {{ items.length }} 条记录</p>
      </div>
      <div class="row">
        <select v-model.number="limit" class="select amount" @change="load()">
          <option :value="50">最近 50 条</option>
          <option :value="100">最近 100 条</option>
          <option :value="300">最近 300 条</option>
        </select>
        <button type="button" class="btn" :disabled="loading" @click="load()">
          {{ loading ? '读取中' : '刷新' }}
        </button>
      </div>
    </div>

    <div v-if="error" class="notice notice-bad">
      <span>{{ error }}</span>
      <button type="button" class="btn btn-sm" @click="load()">重试</button>
    </div>

    <div v-if="!loading && !error && items.length === 0" class="empty">
      <strong>还没有操作记录</strong>
      <span>建库、备份、恢复、删库都会记在这里。</span>
    </div>

    <div v-else-if="items.length > 0" class="table-wrap">
      <table class="data">
        <thead>
          <tr>
            <th>时间</th>
            <th>操作者</th>
            <th>动作</th>
            <th>结果</th>
            <th>详情</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="it in items" :key="it.id">
            <td class="mono small time">{{ formatTime(it.ts) }}</td>
            <td>{{ it.username || it.userId || '系统' }}</td>
            <td>
              <div>{{ actionText(it.action) }}</div>
              <div v-if="it.targetId" class="mono small muted">{{ it.targetId }}</div>
            </td>
            <td>
              <span class="pill" :class="it.result === 'ok' ? 'pill-ok' : 'pill-bad'">
                {{ RESULT_TEXT[it.result] ?? it.result }}
              </span>
            </td>
            <td class="small detail">{{ it.detail || '—' }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 1020px;
}

.toolbar {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  flex-wrap: wrap;
}

.lead {
  margin: 0;
  font-size: 13px;
}

.meta {
  margin: 4px 0 0;
  font-size: 12px;
}

.amount {
  width: 130px;
}

.small {
  font-size: 11px;
}

.detail {
  max-width: 380px;
  /* 断行优先在空格与分隔符处，避免把 ULID 从中间切断 */
  overflow-wrap: break-word;
}

.time {
  white-space: nowrap;
}
</style>
