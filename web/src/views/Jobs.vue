<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh, VideoPause } from '@element-plus/icons-vue'
import ViewSwitch from '../components/ViewSwitch.vue'
import { api, type Job, type JobStatus } from '../api'
import { errText, relativeTime, jobTypeText } from '../format'
import { isMobile } from '../media'
import { isCardView } from '../view-mode'
import Pager from '../components/Pager.vue'
import { usePager } from '../pager'

type Filter = 'all' | JobStatus

const FILTERS: { value: Filter; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'running', label: '进行中' },
  { value: 'queued', label: '排队中' },
  { value: 'succeeded', label: '成功' },
  { value: 'failed', label: '失败' },
  { value: 'canceled', label: '已取消' },
]

const STATUS_TEXT: Record<JobStatus, string> = {
  queued: '排队中',
  running: '进行中',
  succeeded: '成功',
  failed: '失败',
  canceled: '已取消',
}

const STATUS_TAG: Record<JobStatus, 'success' | 'warning' | 'danger' | 'info'> = {
  queued: 'info',
  running: 'warning',
  succeeded: 'success',
  failed: 'danger',
  canceled: 'info',
}

const items = ref<Job[]>([])
const activeCount = ref(0)
const loading = ref(false)
const error = ref('')
const filter = ref<Filter>('all')
const canceling = ref('')

const rows = computed(() => (filter.value === 'all' ? items.value : items.value.filter((j) => j.status === filter.value)))

// 任务会一直堆，不翻页的话翻两屏就找不到刚才那一条
const { page: pageNo, pageSize, total: pageTotal, items: pagedRows } = usePager(rows, 10)

const hasActive = computed(() => items.value.some((j) => j.status === 'running' || j.status === 'queued'))

async function load(): Promise<void> {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    const data = await api.listJobs()
    items.value = data.items
    activeCount.value = data.active
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
  watchActive()
}

async function cancel(job: Job): Promise<void> {
  if (canceling.value) return
  canceling.value = job.id
  try {
    await api.cancelJob(job.id)
    ElMessage.success('已取消该任务')
    await load()
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    canceling.value = ''
  }
}

// 只要有进行中的任务就轻量轮询，用户能看着进度往前走；没有就停，不空转
let timer: number | undefined

function ensureTimer(): void {
  if (timer !== undefined) return
  timer = window.setInterval(() => {
    void load()
  }, 3000)
}

function stopTimer(): void {
  if (timer === undefined) return
  window.clearInterval(timer)
  timer = undefined
}

function watchActive(): void {
  if (hasActive.value) ensureTimer()
  else stopTimer()
}

onMounted(() => {
  void load().then(watchActive)
})

onBeforeUnmount(stopTimer)
</script>

<template>
  <div class="page">
    <div class="toolbar">
      <div class="grow">
        <p class="lead">超过几秒的操作都会在这里排队执行，页面关掉也不会中断。</p>
        <p class="meta muted">
          共 {{ items.length }} 条<template v-if="activeCount">，{{ activeCount }} 个进行中</template>
        </p>
      </div>
      <div class="tools">
        <ViewSwitch />
        <el-button :icon="Refresh" :loading="loading" @click="load()">刷新</el-button>
      </div>
    </div>

    <el-radio-group v-model="filter" class="filters" size="small">
      <el-radio-button v-for="f in FILTERS" :key="f.value" :value="f.value">{{ f.label }}</el-radio-button>
    </el-radio-group>

    <el-alert v-if="error" type="error" :closable="false" show-icon :title="error">
      <template #default>
        <el-button size="small" text type="primary" @click="load()">重试</el-button>
      </template>
    </el-alert>

    <div v-if="!loading && !rows.length" class="empty-state">
      <el-empty :description="filter === 'all' ? '还没有任务' : '没有匹配的任务'" :image-size="72">
        <span class="muted">建库、备份、恢复、导入导出这些超过几秒的操作都会出现在这里。</span>
      </el-empty>
    </div>

    <el-table v-else-if="!isCardView" v-loading="loading" :data="pagedRows" row-key="id" class="table">
      <el-table-column label="任务" :min-width="isMobile ? 120 : 170">
        <template #default="{ row }">
          <div class="job-cell">
            <span class="job-type">{{ jobTypeText(row.type) }}</span>
            <span v-if="row.targetId" class="mono job-target">{{ row.targetId }}</span>
          </div>
        </template>
      </el-table-column>

      <el-table-column label="状态" width="96">
        <template #default="{ row }">
          <el-tag size="small" :type="STATUS_TAG[row.status as JobStatus]">
            {{ STATUS_TEXT[row.status as JobStatus] }}
          </el-tag>
        </template>
      </el-table-column>

      <el-table-column label="进度" :width="isMobile ? 96 : 150">
        <template #default="{ row }">
          <el-progress
            v-if="row.status === 'running'"
            :percentage="row.progress"
            :stroke-width="6"
            :text-inside="false"
          />
          <span v-else class="muted small">{{ row.status === 'succeeded' ? '100%' : row.status === 'queued' ? '等待中' : '—' }}</span>
        </template>
      </el-table-column>

      <el-table-column v-if="!isMobile" label="时间" width="150">
        <template #default="{ row }">
          <span class="muted small">{{ relativeTime(row.createdAt) }}</span>
        </template>
      </el-table-column>

      <el-table-column label="操作" :width="isMobile ? 76 : 120" align="right">
        <template #default="{ row }">
          <el-button
            v-if="row.status === 'running' || row.status === 'queued'"
            link
            type="danger"
            size="small"
            :icon="VideoPause"
            :loading="canceling === row.id"
            @click="cancel(row as Job)"
          >
            <span v-if="!isMobile">取消</span>
          </el-button>
          <span v-else class="muted small">—</span>
        </template>
      </el-table-column>

      <el-table-column v-if="!isMobile" label="说明" min-width="220">
        <template #default="{ row }">
          <span v-if="row.errorHint" class="small err">{{ row.errorHint }}</span>
          <span v-else class="muted small">{{ row.result ? row.result : '—' }}</span>
        </template>
      </el-table-column>
    </el-table>

    <div v-else v-loading="loading" class="list-cards">
      <div v-for="job in pagedRows" :key="job.id" class="list-card">
        <div class="list-card-head">
          <span class="card-type">{{ jobTypeText(job.type) }}</span>
          <el-tag size="small" :type="STATUS_TAG[job.status as JobStatus]">
            {{ STATUS_TEXT[job.status as JobStatus] }}
          </el-tag>
        </div>
        <el-progress
          v-if="job.status === 'running'"
          :percentage="job.progress"
          :stroke-width="6"
        />
        <div v-if="job.targetId" class="mono card-target">{{ job.targetId }}</div>
        <div class="list-card-meta">
          <span>{{ relativeTime(job.createdAt) }}</span>
          <span v-if="job.attempts > 1">重试 {{ job.attempts - 1 }} 次</span>
        </div>
        <p v-if="job.errorHint" class="card-err">{{ job.errorHint }}</p>
        <div v-if="job.status === 'running' || job.status === 'queued'" class="list-card-ops">
          <el-button
            size="small"
            type="danger"
            plain
            :icon="VideoPause"
            :loading="canceling === job.id"
            @click="cancel(job)"
          >
            取消
          </el-button>
        </div>
      </div>
    </div>

    <Pager v-model:page="pageNo" v-model:page-size="pageSize" :total="pageTotal" />
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

.toolbar {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  flex-wrap: wrap;
}

.lead {
  margin: 0;
  font-size: 13.5px;
}

.meta {
  margin: 4px 0 0;
  font-size: 13px;
}

.tools {
  display: flex;
  align-items: center;
  gap: 8px;
}

.tools .el-button + .el-button {
  margin-left: 0;
}

.filters {
  align-self: flex-start;
}

.job-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.job-type {
  font-size: 13.5px;
}

.job-target {
  color: var(--fg-muted);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.small {
  font-size: 12px;
}

.err {
  color: var(--bad);
}

.table {
  border: 1px solid var(--line);
  border-radius: var(--radius);
  overflow: hidden;
}

.empty-state {
  border: 1px dashed var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
}

.card-type {
  min-width: 0;
  overflow: hidden;
  font-size: 13.5px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-target {
  overflow-wrap: anywhere;
  color: var(--fg-muted);
  font-size: 12px;
}

.card-err {
  margin: 0;
  color: var(--bad);
  font-size: 13px;
  line-height: 1.6;
}

@media (max-width: 720px) {
  .tools {
    width: 100%;
  }
}
</style>
