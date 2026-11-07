<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Download, Refresh } from '@element-plus/icons-vue'
import { api, engineLabel, type BackupWithInstance } from '../api'
import { errText, formatBytes, formatTime, relativeTime } from '../format'
import { isMobile } from '../media'

const emit = defineEmits<{ open: [id: string, tab?: string] }>()
const items = ref<BackupWithInstance[]>([])
const loading = ref(false)
const error = ref('')
const filter = ref<'all' | 'succeeded' | 'failed'>('all')

const shown = computed(() => items.value.filter((item) => filter.value === 'all' || item.status === filter.value))
const succeeded = computed(() => items.value.filter((item) => item.status === 'succeeded').length)
const failed = computed(() => items.value.filter((item) => item.status === 'failed').length)
const totalSize = computed(() =>
  items.value.reduce((sum, item) => sum + (item.status === 'succeeded' ? item.sizeBytes : 0), 0),
)

const STATUS: Record<string, string> = {
  running: '进行中',
  succeeded: '成功',
  failed: '失败',
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    items.value = (await api.listBackupCenter()).items
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="page" v-loading="loading">
    <header class="head">
      <div>
        <h2>备份与恢复</h2>
        <p class="muted">集中查看所有已授权数据库服务的备份，进入服务详情可恢复或调整策略。</p>
      </div>
      <button type="button" class="btn" :disabled="loading" @click="load">
        <el-icon><Refresh /></el-icon>{{ loading ? '读取中' : '刷新' }}
      </button>
    </header>

    <el-alert v-if="error" type="error" :closable="false" show-icon :title="error" />

    <section class="summary">
      <div><span>备份总数</span><strong>{{ items.length }}</strong></div>
      <div><span>成功</span><strong>{{ succeeded }}</strong></div>
      <div><span>失败</span><strong class="bad-text">{{ failed }}</strong></div>
      <div><span>可恢复体积</span><strong>{{ formatBytes(totalSize) }}</strong></div>
    </section>

    <div class="filters">
      <button v-for="item in [
        { key: 'all', text: '全部' },
        { key: 'succeeded', text: '成功' },
        { key: 'failed', text: '失败' },
      ] as const" :key="item.key" type="button" :class="{ active: filter === item.key }" @click="filter = item.key">
        {{ item.text }}
      </button>
    </div>

    <el-table v-if="shown.length && !isMobile" :data="shown" row-key="id" class="table">
      <el-table-column label="数据库服务" min-width="170">
        <template #default="{ row }">
          <button type="button" class="service-link" @click="emit('open', row.instanceId, 'backups')">
            {{ row.instanceName || row.instanceId }}
          </button>
          <div class="muted small">{{ engineLabel(row.engine) }}</div>
        </template>
      </el-table-column>
      <el-table-column label="数据库" min-width="130">
        <template #default="{ row }"><span class="mono">{{ row.database || '—' }}</span></template>
      </el-table-column>
      <el-table-column label="状态" width="92">
        <template #default="{ row }">
          <el-tag size="small" :type="row.status === 'failed' ? 'danger' : row.status === 'succeeded' ? 'success' : 'warning'">
            {{ STATUS[row.status] ?? row.status }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="大小" width="100">
        <template #default="{ row }">{{ row.sizeBytes ? formatBytes(row.sizeBytes) : '—' }}</template>
      </el-table-column>
      <el-table-column label="时间" width="160">
        <template #default="{ row }">
          <span :title="formatTime(row.startedAt)">{{ relativeTime(row.startedAt) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="150" align="right">
        <template #default="{ row }">
          <a v-if="row.status === 'succeeded'" class="download" :href="api.backupDownloadUrl(row.instanceId, row.id)">
            <el-icon><Download /></el-icon>下载
          </a>
          <button type="button" class="text-action" @click="emit('open', row.instanceId, 'backups')">管理</button>
        </template>
      </el-table-column>
    </el-table>

    <div v-else-if="shown.length" class="mobile-list">
      <article v-for="row in shown" :key="row.id" class="backup-card">
        <header>
          <button type="button" class="service-link" @click="emit('open', row.instanceId, 'backups')">
            {{ row.instanceName || row.instanceId }}
          </button>
          <el-tag size="small" :type="row.status === 'failed' ? 'danger' : row.status === 'succeeded' ? 'success' : 'warning'">
            {{ STATUS[row.status] ?? row.status }}
          </el-tag>
        </header>
        <div class="card-meta">
          <span>{{ engineLabel(row.engine) }}</span>
          <span class="mono">{{ row.database || '—' }}</span>
          <span>{{ row.sizeBytes ? formatBytes(row.sizeBytes) : '—' }}</span>
          <span>{{ relativeTime(row.startedAt) }}</span>
        </div>
        <div class="card-actions">
          <a v-if="row.status === 'succeeded'" class="download" :href="api.backupDownloadUrl(row.instanceId, row.id)">
            <el-icon><Download /></el-icon>下载
          </a>
          <button type="button" class="text-action" @click="emit('open', row.instanceId, 'backups')">打开服务</button>
        </div>
      </article>
    </div>

    <el-empty v-else-if="!loading" description="还没有备份记录" :image-size="72">
      <span class="muted">进入数据库服务的「备份与恢复」即可创建手动备份。</span>
    </el-empty>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 1400px;
  margin: 0 auto;
}

.head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.head h2 {
  margin: 0 0 4px;
  font-size: 18px;
}

.head p {
  margin: 0;
  font-size: 13px;
}

.summary {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
}

.summary div {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 12px 14px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--bg-elevated);
}

.summary span {
  color: var(--fg-muted);
  font-size: 12px;
}

.summary strong {
  font-size: 20px;
}

.filters {
  display: flex;
  gap: 4px;
}

.filters button {
  padding: 6px 14px;
  border: 1px solid var(--line);
  border-radius: 7px;
  background: var(--bg-elevated);
  color: var(--fg-muted);
}

.filters button.active {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent);
}

.table {
  border: 1px solid var(--line);
  border-radius: 8px;
  overflow: hidden;
}

.service-link,
.text-action {
  padding: 0;
  border: 0;
  background: none;
  color: var(--accent);
  cursor: pointer;
}

.download {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  color: var(--accent);
  text-decoration: none;
}

.text-action {
  margin-left: 12px;
}

.mobile-list {
  display: grid;
  gap: 8px;
}

.backup-card {
  padding: 12px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--bg-elevated);
}

.backup-card header,
.card-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.card-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  margin: 8px 0;
  color: var(--fg-muted);
  font-size: 12px;
}

.card-actions {
  justify-content: flex-start;
}

.small {
  font-size: 12px;
}

@media (max-width: 720px) {
  .head {
    flex-direction: column;
  }

  .summary {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
