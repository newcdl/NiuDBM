<script setup lang="ts">
import { computed } from 'vue'
import { useDiag } from '../useDiag'

const emit = defineEmits<{ navigate: [view: 'diag'] }>()

const { report, loading, error, load } = useDiag()

interface Row {
  label: string
  value: string
}

const rows = computed<Row[]>(() => {
  const env = report.value?.env
  if (!env) return []
  return [
    { label: '应用', value: `${env.appName} ${env.version}` },
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
          <button type="button" class="btn btn-primary" @click="emit('navigate', 'diag')">
            查看明细
          </button>
        </section>
      </div>

      <section class="card note">
        <h2>当前版本说明</h2>
        <p>
          这是 <code>0.1.0</code> 骨架包，<strong>尚不包含数据库管理功能</strong
          >。它的用途是把平台能力逐条验证清楚：目录软链是否与变量一致、权限边界是否真的生效、生命周期退出码是否正确。
        </p>
        <p class="muted">
          自检结果对应核对清单 P-02 / P-03 / P-05 / P-06 / P-12 / P-17 / P-19 / P-21。
          「未通过」项处理完，才进入数据库功能开发。
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

.btn {
  padding: 6px 12px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--bg-elevated);
  font-size: 12px;
}

.btn-primary {
  border-color: var(--accent);
  background: var(--accent);
  color: #fff;
}

.btn-primary:hover {
  opacity: 0.9;
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
