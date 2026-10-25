<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, type HealthInfo } from '../api'
import { useDiag } from '../useDiag'

const emit = defineEmits<{ navigate: [view: 'diag' | 'instances'] }>()

const { report, loading, error, load } = useDiag()

// 概览额外拉一次 health：实例数是用户最关心的一个数字，不去数据库页也能看到
const health = ref<HealthInfo | null>(null)

interface Row {
  label: string
  value: string
}

const rows = computed<Row[]>(() => {
  const env = report.value?.env
  if (!env) return []
  return [
    { label: '应用', value: `${env.appName} ${env.version}` },
    { label: '数据库实例', value: health.value ? `${health.value.instances} 个` : '—' },
    { label: '运行身份', value: `${env.user}（uid=${env.uid} gid=${env.gid}）` },
    { label: '当前用户', value: env.isAdmin ? '管理员' : '普通用户' },
    { label: 'CPU 架构', value: env.arch },
    { label: '监听方式', value: env.listen },
    { label: '系统', value: env.osName },
    { label: 'C 运行库', value: env.glibc },
  ]
})

const summary = computed(() => report.value?.summary ?? { pass: 0, warn: 0, fail: 0, total: 0 })
const hasData = computed(() => report.value !== null)

onMounted(async () => {
  try {
    health.value = await api.health()
  } catch {
    // 实例数只用于展示，拿不到就不显示具体数字，不打断页面
  }
})
</script>

<template>
  <div class="page">
    <div v-if="error" class="notice notice-bad">
      <div>
        <strong>无法读取运行环境</strong>
        <p>{{ error }}</p>
      </div>
      <button type="button" class="btn" @click="load()">重试</button>
    </div>

    <div v-else-if="loading && !hasData" class="notice">正在读取运行环境…</div>

    <template v-else>
      <div class="grid">
        <section class="card">
          <h2>运行环境</h2>
          <dl class="kv">
            <template v-for="row in rows" :key="row.label">
              <dt>{{ row.label }}</dt>
              <dd>{{ row.value }}</dd>
            </template>
          </dl>
        </section>

        <section class="card">
          <h2>平台自检</h2>
          <div class="stats">
            <div class="stat stat-ok">
              <b>{{ summary.pass }}</b>
              <span>通过</span>
            </div>
            <div class="stat stat-warn">
              <b>{{ summary.warn }}</b>
              <span>需确认</span>
            </div>
            <div class="stat stat-bad">
              <b>{{ summary.fail }}</b>
              <span>未通过</span>
            </div>
          </div>
          <p class="hint">
            共 {{ summary.total }} 项检查，覆盖目录映射、权限边界与运行身份。
          </p>
          <div class="row actions">
            <button type="button" class="btn" @click="emit('navigate', 'diag')">自检明细</button>
            <button type="button" class="btn btn-primary" @click="emit('navigate', 'instances')">
              进入数据库管理
            </button>
          </div>
        </section>
      </div>

      <section class="card note">
        <h2>这个版本能做什么</h2>
        <p>
          当前版本提供 <strong>SQLite 实例的完整管理</strong
          >：建库、表结构浏览、SQL 控制台、一致性快照备份与一键恢复，所有写操作都有审计记录。
          引擎随应用一起安装，不需要额外下载。
        </p>
        <p class="muted">
          下一阶段接入 MySQL / PostgreSQL / Redis（需下载引擎与授权校验），以及定时备份、成员权限与资源限制。
        </p>
      </section>
    </template>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 980px;
}

.grid {
  display: grid;
  grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr);
  gap: 16px;
}

.card {
  padding: 16px 18px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
  box-shadow: var(--shadow);
}

.card h2 {
  margin: 0 0 12px;
  font-size: 13px;
  font-weight: 600;
  color: var(--fg-muted);
}

.kv {
  display: grid;
  grid-template-columns: 88px minmax(0, 1fr);
  gap: 8px 12px;
  margin: 0;
}

.kv dt {
  color: var(--fg-muted);
  font-size: 12px;
}

.kv dd {
  margin: 0;
  font-size: 13px;
  word-break: break-all;
}

.stats {
  display: flex;
  gap: 10px;
}

.stat {
  flex: 1;
  padding: 10px 12px;
  border-radius: 8px;
  text-align: center;
}

.stat b {
  display: block;
  font-size: 24px;
  line-height: 1.2;
}

.stat span {
  font-size: 11px;
  color: var(--fg-muted);
}

.stat-ok {
  background: var(--ok-bg);
  color: var(--ok);
}
.stat-warn {
  background: var(--warn-bg);
  color: var(--warn);
}
.stat-bad {
  background: var(--bad-bg);
  color: var(--bad);
}

.hint {
  margin: 12px 0 14px;
  color: var(--fg-muted);
  font-size: 12px;
}

.actions {
  gap: 8px;
}

.note p {
  margin: 0 0 8px;
  font-size: 13px;
}

.note p:last-child {
  margin-bottom: 0;
}

.muted {
  color: var(--fg-muted);
  font-size: 12px;
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

@media (max-width: 860px) {
  .grid {
    grid-template-columns: 1fr;
  }
}
</style>
