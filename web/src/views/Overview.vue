<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ArrowRight, CircleCheck, Download, Plus, Refresh, WarningFilled } from '@element-plus/icons-vue'
import { api, type Overview } from '../api'
import { errText, formatBytes, jobTypeText, relativeTime } from '../format'

const emit = defineEmits<{ navigate: [view: 'instances' | 'images' | 'jobs' | 'audit' | 'diag'] }>()

const data = ref<Overview | null>(null)
const loading = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    data.value = await api.getOverview()
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)

const ENGINE_TONE: Record<string, string> = {
  postgres: 'tone-blue',
  mysql: 'tone-green',
  sqlite: 'tone-slate',
}
const ENGINE_BADGE: Record<string, string> = {
  postgres: 'PG',
  mysql: 'My',
  sqlite: 'SQ',
}
// 只有 MySQL 协议系：版本就是镜像 tag


const instances = computed(() => data.value?.instances.items ?? [])
const images = computed(() => data.value?.images ?? [])
const recentBackups = computed(() => data.value?.backups.recent ?? [])
const recentJobs = computed(() => data.value?.jobs.recent ?? [])
const recentAudit = computed(() => data.value?.audit.recent ?? [])

const readyImages = computed(() => images.value.filter((img) => img.local).length)
const totalImages = computed(() => images.value.length)

// 没有任何实例时不要摆一堆 0：直接把"下一步做什么"说清楚
const empty = computed(() => (data.value?.instances.total ?? 0) === 0)

const headline = computed(() => {
  const d = data.value
  if (!d) return '正在读取真实状态…'
  if (d.instances.total === 0) return '还没有数据库实例，先建一个吧。'
  const parts = [`${d.instances.total} 个实例`]
  if (d.instances.running) parts.push(`${d.instances.running} 个在运行`)
  if (d.instances.error) parts.push(`${d.instances.error} 个异常`)
  if (d.jobs.active) parts.push(`${d.jobs.active} 个任务进行中`)
  return parts.join(' · ')
})

const lastBackupText = computed(() => {
  const at = data.value?.backups.latestAt ?? 0
  return at ? relativeTime(at) : '还没有备份'
})

// 需要提醒的事情：失败备份、异常实例、失败任务；没有就显示"一切正常"
const alerts = computed(() => {
  const d = data.value
  if (!d) return []
  const out: { tone: 'warn' | 'info'; title: string; sub: string }[] = []
  if (d.instances.error)
    out.push({ tone: 'warn', title: `${d.instances.error} 个实例状态异常`, sub: '打开「数据库」看具体原因' })
  if (d.backups.failed)
    out.push({ tone: 'warn', title: `${d.backups.failed} 个备份失败`, sub: '检查共享目录空间与权限' })
  if (d.jobs.failed24h)
    out.push({ tone: 'warn', title: `24 小时内有 ${d.jobs.failed24h} 个任务失败`, sub: '在「任务中心」查看错误信息' })
  if (!d.docker.available)
    out.push({ tone: 'info', title: 'Docker 不可用', sub: 'MySQL / PostgreSQL 无法用容器方式运行' })
  return out
})

function statusText(status: string) {
  return { running: '运行中', stopped: '已停止', error: '异常', creating: '创建中' }[status] ?? status
}
</script>

<template>
  <div class="page" v-loading="loading">
    <el-alert v-if="error" type="error" :closable="false" show-icon :title="error" />

    <el-card shadow="never" class="hero">
      <div class="hero-main">
        <h2>数据库总览</h2>
        <p class="hero-sub">{{ headline }}</p>
        <div class="hero-facts">
          <span>{{ readyImages }} / {{ totalImages }} 个镜像已在本机</span>
          <span class="dot-sep">·</span>
          <span>
            数据 {{ formatBytes(data?.instances.sizeBytes ?? 0) }}
            <template v-if="data?.instances.sizeUnknown">（另有 {{ data.instances.sizeUnknown }} 个卷内未计入）</template>
          </span>
          <span class="dot-sep">·</span>
          <span>最近备份 {{ lastBackupText }}</span>
          <span class="dot-sep">·</span>
          <span>
            Docker
            {{ data?.docker.available ? `可用（${data.docker.version}）` : '不可用' }}
          </span>
        </div>
      </div>
      <div class="hero-actions">
        <el-button :icon="Refresh" :loading="loading" @click="load">刷新</el-button>
        <el-button type="primary" :icon="Plus" @click="emit('navigate', 'instances')">创建数据库</el-button>
      </div>
    </el-card>

    <div class="metrics">
      <button type="button" class="metric" @click="emit('navigate', 'instances')">
        <span class="metric-label">实例</span>
        <strong>{{ data?.instances.total ?? 0 }}</strong>
        <span class="metric-sub">
          {{ data?.instances.running ?? 0 }} 运行中<template v-if="data?.instances.stopped">
            · {{ data.instances.stopped }} 已停止</template>
          <template v-if="data?.instances.creating"> · {{ data.instances.creating }} 创建中</template>
          <template v-if="data?.instances.failed"> · {{ data.instances.failed }} 部署失败</template>
        </span>
      </button>
      <button type="button" class="metric" @click="emit('navigate', 'images')">
        <span class="metric-label">镜像</span>
        <strong>{{ readyImages }}<small>/{{ totalImages }}</small></strong>
        <span class="metric-sub">{{ readyImages === totalImages ? '全部已缓存' : '部署时按需拉取' }}</span>
      </button>
      <button type="button" class="metric" @click="emit('navigate', 'instances')">
        <span class="metric-label">备份</span>
        <strong>{{ data?.backups.total ?? 0 }}</strong>
        <span class="metric-sub">{{ lastBackupText }}{{ data?.backups.failed ? ` · ${data.backups.failed} 失败` : '' }}</span>
      </button>
      <button type="button" class="metric" @click="emit('navigate', 'audit')">
        <span class="metric-label">今日操作</span>
        <strong>{{ data?.audit.today ?? 0 }}</strong>
        <span class="metric-sub">进行中任务 {{ data?.jobs.active ?? 0 }}</span>
      </button>
    </div>

    <el-card shadow="never" class="block">
      <template #header>
        <div class="block-head">
          <span>镜像</span>
          <span class="block-note">
            {{ readyImages }} 个已在本机{{ data?.docker.available ? '，Docker 已就绪' : '，Docker 不可用' }}
          </span>
        </div>
      </template>
      <div class="engines">
        <button
          v-for="img in images"
          :key="img.tag"
          type="button"
          class="engine"
          :class="{ off: !img.local }"
          @click="emit('navigate', 'images')"
        >
          <div class="engine-top">
            <span class="badge" :class="img.family === 'mysql' ? 'tone-blue' : 'tone-slate'">
              {{ img.family === 'mysql' ? 'MY' : 'MA' }}
            </span>
            <span class="engine-id">
              <strong>{{ img.label }}</strong>
              <span class="engine-ver">{{ img.name }}</span>
            </span>
            <span class="state" :class="img.local ? 'ok' : 'warn'">
              <span class="state-dot" />
              {{ img.local ? '已在本机' : '需拉取' }}
            </span>
          </div>
          <div class="engine-meta">
            {{ img.local ? '部署时直接复用，不再下载' : '部署时自动拉取官方镜像' }}
          </div>
          <div class="engine-foot">
            <span class="engine-open">查看镜像<el-icon><ArrowRight /></el-icon></span>
          </div>
        </button>
      </div>
    </el-card>

    <el-card v-if="instances.length" shadow="never" class="block">
      <template #header>
        <div class="block-head">
          <span>最近的实例</span>
          <span class="block-note">按创建时间倒序，最多 8 条</span>
        </div>
      </template>
      <div class="backups">
        <button v-for="i in instances" :key="i.id" type="button" class="backup-row" @click="emit('navigate', 'instances')">
          <span class="backup-dot" :class="i.status === 'running' ? 'ok' : 'bad'" />
          <span class="backup-main">
            <strong>{{ i.name }}</strong>
            <span class="backup-file">
              MySQL {{ i.engineVersion }}
              · Docker
              <template v-if="i.port">· :{{ i.port }}</template>
            </span>
          </span>
          <span class="backup-side">
            <span>{{ statusText(i.status) }}</span>
            <span class="muted">
              {{ !i.sizeBytes ? '卷内' : formatBytes(i.sizeBytes) }}
            </span>
          </span>
        </button>
      </div>
    </el-card>

    <div class="cols">
      <el-card shadow="never" class="block">
        <template #header>
          <div class="block-head">
            <span>最近备份</span>
            <span class="block-note">共 {{ data?.backups.total ?? 0 }} 份</span>
          </div>
        </template>
        <div v-if="recentBackups.length" class="backups">
          <button
            v-for="b in recentBackups"
            :key="b.id"
            type="button"
            class="backup-row"
            @click="emit('navigate', 'instances')"
          >
            <span class="backup-dot" :class="b.status === 'failed' ? 'bad' : 'ok'" />
            <span class="backup-main">
              <strong>{{ b.instanceName || b.instanceId }}</strong>
              <span class="backup-file">{{ b.fileName }}</span>
            </span>
            <span class="backup-side">
              <span>{{ relativeTime(b.startedAt) }}</span>
              <span class="muted">{{ b.sizeBytes ? formatBytes(b.sizeBytes) : b.status }}</span>
            </span>
          </button>
        </div>
        <div v-else class="empty">
          <p>还没有备份。打开实例详情就能立即备份，也可以配定时备份。</p>
        </div>
      </el-card>

      <el-card shadow="never" class="block">
        <template #header>
          <div class="block-head">
            <span>最近任务</span>
            <span class="block-note">{{ data?.jobs.active ?? 0 }} 个进行中</span>
          </div>
        </template>
        <div v-if="recentJobs.length" class="backups">
          <button v-for="j in recentJobs" :key="j.id" type="button" class="backup-row" @click="emit('navigate', 'jobs')">
            <span class="backup-dot" :class="j.status === 'failed' ? 'bad' : 'ok'" />
            <span class="backup-main">
              <strong>{{ jobTypeText(j.type) }}</strong>
              <span class="backup-file">{{ j.targetId || '—' }}</span>
            </span>
            <span class="backup-side">
              <span>{{ relativeTime(j.createdAt) }}</span>
              <span class="muted">{{ j.status }}</span>
            </span>
          </button>
        </div>
        <div v-else class="empty">
          <p>还没有任务记录。建库、备份、恢复都会在这里留痕。</p>
        </div>
      </el-card>
    </div>

    <el-card shadow="never" class="block">
      <template #header>
        <div class="block-head">
          <span>需要注意</span>
          <span class="block-note">今日操作 {{ data?.audit.today ?? 0 }} 次</span>
        </div>
      </template>
      <div v-if="alerts.length" class="tasks">
        <div v-for="a in alerts" :key="a.title" class="task" :class="a.tone">
          <el-icon class="task-icon"><WarningFilled /></el-icon>
          <span class="task-text">
            <strong>{{ a.title }}</strong>
            <span>{{ a.sub }}</span>
          </span>
        </div>
      </div>
      <div v-else class="empty ok">
        <el-icon class="ok-icon"><CircleCheck /></el-icon>
        <p>没有待处理的问题。{{ empty ? '建一个数据库开始使用吧。' : '' }}</p>
      </div>

      <div v-if="recentAudit.length" class="footnote" style="margin-top: 8px">
        <span class="muted">
          最近一次操作：{{ recentAudit[0].username }} {{ recentAudit[0].action }}
          {{ recentAudit[0].result === 'ok' ? '成功' : '失败' }}
          （{{ relativeTime(recentAudit[0].ts) }}）
        </span>
        <el-button link type="primary" @click="emit('navigate', 'audit')">查看操作记录</el-button>
      </div>
    </el-card>
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

/* ---------- 顶部 ---------- */
.hero {
  border-style: dashed;
}

.hero :deep(.el-card__body) {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 18px;
  padding: 18px 20px;
}

.hero-main {
  min-width: 0;
}

.hero h2 {
  margin: 0;
  font-size: 17px;
}

.hero-sub {
  margin: 6px 0 0;
  color: var(--fg-muted);
  font-size: 13.5px;
}

.hero-facts {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  margin-top: 10px;
  color: var(--fg-muted);
  font-size: 13px;
}

.dot-sep {
  opacity: 0.5;
}

.hero-actions {
  display: flex;
  flex-shrink: 0;
  gap: 8px;
}

.hero-actions .el-button + .el-button {
  margin-left: 0;
}

/* ---------- 指标卡 ---------- */
.metrics {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 10px;
}

.metric {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 14px 16px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
  color: inherit;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s, transform 0.15s;
}

.metric:hover {
  border-color: var(--el-color-primary-light-5);
}

.metric-label {
  color: var(--fg-muted);
  font-size: 13px;
}

.metric strong {
  font-size: 24px;
  font-weight: 650;
  line-height: 1.2;
}

.metric strong small {
  color: var(--fg-muted);
  font-size: 13.5px;
  font-weight: 500;
}

.metric-sub {
  color: var(--fg-muted);
  font-size: 12.5px;
}

/* ---------- 区块 ---------- */
.block :deep(.el-card__header) {
  padding: 11px 16px;
}

.block-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  font-size: 13.5px;
  font-weight: 600;
}

.block-note {
  color: var(--fg-muted);
  font-size: 12.5px;
  font-weight: 400;
}

.block :deep(.el-card__body) {
  padding: 14px 16px;
}

/* ---------- 引擎 ---------- */
.engines {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
  gap: 10px;
}

.engine {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 13px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg);
  color: inherit;
  text-align: left;
  cursor: pointer;
  transition: border-color 0.15s, background 0.15s;
}

.engine:hover {
  border-color: var(--el-color-primary-light-5);
  background: var(--bg-elevated);
}

.engine.off {
  border-style: dashed;
  opacity: 0.78;
}

.engine.off:hover {
  opacity: 1;
}

.engine-top {
  display: flex;
  align-items: center;
  gap: 10px;
}

.badge {
  flex: none;
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  border-radius: 10px;
  color: #fff;
  font-size: 12.5px;
  font-weight: 700;
  letter-spacing: 0.3px;
}

.tone-blue {
  background: linear-gradient(140deg, #2f6fed, #4f9bff);
}
.tone-green {
  background: linear-gradient(140deg, #0f9d58, #34c759);
}
.tone-slate {
  background: linear-gradient(140deg, #5b6472, #8b94a3);
}

.engine-id {
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.4;
}

.engine-id strong {
  font-size: 14px;
}

.engine-ver {
  overflow: hidden;
  color: var(--fg-muted);
  font-size: 12.5px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.state {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  margin-left: auto;
  font-size: 12.5px;
  white-space: nowrap;
}

.state-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.state.ok {
  color: var(--ok);
}

.state.warn {
  color: var(--warn);
}

.state.idle {
  color: var(--fg-muted);
}

.engine-meta {
  display: -webkit-box;
  overflow: hidden;
  color: var(--fg-muted);
  font-size: 12.5px;
  line-height: 1.6;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
}

.engine-foot {
  display: flex;
  justify-content: flex-end;
  margin-top: auto;
}

.engine-open {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  color: var(--el-color-primary);
  font-size: 13px;
}

/* ---------- 列表 ---------- */
.cols {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 14px;
  align-items: start;
}

.backups {
  display: flex;
  flex-direction: column;
}

.backup-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 4px;
  border: 0;
  border-bottom: 1px solid var(--line);
  background: transparent;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.backup-row:last-child {
  border-bottom: 0;
}

.backup-row:hover {
  background: var(--bg);
}

.backup-dot {
  flex: none;
  width: 7px;
  height: 7px;
  border-radius: 50%;
}

.backup-dot.ok {
  background: var(--ok);
}

.backup-dot.bad {
  background: var(--bad);
}

.backup-main {
  display: flex;
  flex-direction: column;
  min-width: 0;
  line-height: 1.5;
}

.backup-main strong {
  font-size: 13.5px;
}

.backup-file {
  overflow: hidden;
  color: var(--fg-muted);
  font-size: 12px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.backup-side {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  margin-left: auto;
  font-size: 12.5px;
  line-height: 1.5;
  white-space: nowrap;
}

/* ---------- 待处理 ---------- */
.tasks {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.task {
  display: flex;
  align-items: flex-start;
  gap: 9px;
  padding: 10px 11px;
  border-radius: var(--radius);
  background: var(--bg);
}

.task.warn {
  background: var(--warn-bg);
}

.task.info {
  background: var(--accent-soft);
}

.task-icon {
  flex: none;
  margin-top: 1px;
  font-size: 15px;
}

.task.warn .task-icon {
  color: var(--warn);
}

.task.info .task-icon {
  color: var(--accent);
}

.task-text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  line-height: 1.5;
}

.task-text strong {
  font-size: 13.5px;
}

.task-text span {
  overflow-wrap: anywhere;
  color: var(--fg-muted);
  font-size: 12.5px;
}

.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  padding: 18px 0 14px;
  text-align: center;
}

.empty p {
  margin: 0;
  color: var(--fg-muted);
  font-size: 13.5px;
}

.empty.ok {
  gap: 8px;
}

.ok-icon {
  color: var(--ok);
  font-size: 22px;
}

/* ---------- 页脚 ---------- */
.footnote {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 0 2px;
  font-size: 12.5px;
}

.muted {
  color: var(--fg-muted);
}

/* ---------- 窄屏 ---------- */
@media (max-width: 900px) {
  .metrics {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .cols {
    grid-template-columns: minmax(0, 1fr);
  }
}

@media (max-width: 720px) {
  .hero :deep(.el-card__body) {
    flex-direction: column;
    padding: 16px;
  }

  .hero-actions {
    width: 100%;
  }

  .hero-actions .el-button {
    flex: 1;
  }

  .engines {
    grid-template-columns: minmax(0, 1fr);
  }

  .metric {
    padding: 12px 13px;
  }

  .metric strong {
    font-size: 21px;
  }

  .footnote {
    flex-direction: column;
    align-items: flex-start;
    gap: 4px;
  }

  .backup-file {
    max-width: 46vw;
  }
}
</style>
