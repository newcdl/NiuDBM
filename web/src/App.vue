<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AuditLog from './views/AuditLog.vue'
import ComingSoon from './views/ComingSoon.vue'
import Diagnostics from './views/Diagnostics.vue'
import Instances from './views/Instances.vue'
import Overview from './views/Overview.vue'
import { useDiag } from './useDiag'

type ViewKey = 'overview' | 'instances' | 'audit' | 'jobs' | 'settings' | 'diag'

interface NavItem {
  key: ViewKey
  label: string
  soon?: boolean
}

const navs: NavItem[] = [
  { key: 'overview', label: '概览' },
  { key: 'instances', label: '数据库' },
  { key: 'audit', label: '操作记录' },
  { key: 'jobs', label: '任务中心', soon: true },
  { key: 'settings', label: '设置', soon: true },
]

const current = ref<ViewKey>('overview')
const { report, loading, error, load } = useDiag()

const version = computed<string>(() => report.value?.env.version ?? '0.1.0')

const title = computed<string>(() => {
  if (current.value === 'diag') return '平台自检'
  return navs.find((nav) => nav.key === current.value)?.label ?? ''
})

const statusText = computed<string>(() => {
  if (loading.value) return '检测中…'
  if (error.value) return '检测失败'
  const summary = report.value?.summary
  if (!summary) return '未检测'
  if (summary.fail > 0) return `${summary.fail} 项未通过`
  if (summary.warn > 0) return `${summary.warn} 项需确认`
  return '全部通过'
})

const statusKind = computed<'ok' | 'warn' | 'bad' | 'idle'>(() => {
  if (loading.value) return 'idle'
  if (error.value) return 'bad'
  const summary = report.value?.summary
  if (!summary) return 'idle'
  if (summary.fail > 0) return 'bad'
  if (summary.warn > 0) return 'warn'
  return 'ok'
})

function go(view: ViewKey): void {
  current.value = view
}

onMounted(() => {
  void load()
})
</script>

<template>
  <div class="shell">
    <aside class="side">
      <div class="brand">
        <span class="logo">牛</span>
        <div class="brand-text">
          <strong>牛库管家</strong>
          <small>v{{ version }}</small>
        </div>
      </div>

      <nav class="nav">
        <button
          v-for="nav in navs"
          :key="nav.key"
          type="button"
          class="nav-item"
          :class="{ active: current === nav.key }"
          @click="go(nav.key)"
        >
          <span>{{ nav.label }}</span>
          <span v-if="nav.soon" class="tag">待开发</span>
        </button>
      </nav>

      <div class="nav-dev">
        <p class="nav-dev-title">开发</p>
        <button
          type="button"
          class="nav-item"
          :class="{ active: current === 'diag' }"
          @click="go('diag')"
        >
          <span>平台自检</span>
        </button>
      </div>
    </aside>

    <main class="main">
      <header class="topbar">
        <h1>{{ title }}</h1>
        <div class="topbar-right">
          <span class="status" :class="`status-${statusKind}`">
            <i class="dot" />{{ statusText }}
          </span>
          <button
            v-if="current === 'overview' || current === 'diag'"
            type="button"
            class="btn"
            :disabled="loading"
            @click="load()"
          >
            {{ loading ? '检测中' : '重新检测' }}
          </button>
        </div>
      </header>

      <section class="content">
        <Overview v-if="current === 'overview'" @navigate="go" />
        <Instances v-else-if="current === 'instances'" />
        <AuditLog v-else-if="current === 'audit'" />
        <Diagnostics v-else-if="current === 'diag'" />
        <ComingSoon v-else :name="title" />
      </section>
    </main>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: 208px 1fr;
  height: 100%;
  min-height: 0;
}

.side {
  display: flex;
  flex-direction: column;
  gap: 20px;
  padding: 20px 12px;
  background: var(--bg-elevated);
  border-right: 1px solid var(--line);
}

.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 8px;
}

.logo {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  border-radius: 9px;
  background: linear-gradient(180deg, #12857b, #0a3533);
  color: #fff;
  font-weight: 600;
  font-size: 15px;
}

.brand-text {
  display: flex;
  flex-direction: column;
  line-height: 1.3;
}

.brand-text strong {
  font-size: 14px;
}

.brand-text small {
  color: var(--fg-muted);
  font-size: 11px;
}

.nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.nav-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 10px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--fg);
  font-size: 13px;
  text-align: left;
}

.nav-item:hover {
  background: var(--bg);
}

.nav-item.active {
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}

.tag {
  padding: 1px 6px;
  border-radius: 20px;
  background: var(--bg);
  color: var(--fg-muted);
  font-size: 10px;
  font-weight: 400;
}

.nav-dev {
  margin-top: auto;
  padding-top: 12px;
  border-top: 1px solid var(--line);
}

.nav-dev-title {
  margin: 0 0 4px;
  padding: 0 10px;
  color: var(--fg-muted);
  font-size: 11px;
}

.main {
  display: flex;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 20px;
  border-bottom: 1px solid var(--line);
  background: var(--bg-elevated);
}

.topbar h1 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}

.topbar-right {
  display: flex;
  align-items: center;
  gap: 12px;
}

.status {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--fg-muted);
}

.dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--fg-muted);
}

.status-ok .dot {
  background: var(--ok);
}
.status-ok {
  color: var(--ok);
}
.status-warn .dot {
  background: var(--warn);
}
.status-warn {
  color: var(--warn);
}
.status-bad .dot {
  background: var(--bad);
}
.status-bad {
  color: var(--bad);
}

.btn {
  padding: 6px 12px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--bg-elevated);
  font-size: 12px;
}

.btn:hover:not(:disabled) {
  border-color: var(--accent);
  color: var(--accent);
}

.btn:disabled {
  opacity: 0.55;
  cursor: default;
}

.content {
  flex: 1;
  min-height: 0;
  padding: 20px;
  overflow: auto;
}

@media (max-width: 720px) {
  .shell {
    grid-template-columns: 1fr;
    grid-template-rows: auto 1fr;
  }

  .side {
    flex-direction: row;
    align-items: center;
    gap: 12px;
    padding: 10px 12px;
    overflow-x: auto;
    border-right: 0;
    border-bottom: 1px solid var(--line);
  }

  .brand-text {
    display: none;
  }

  .nav {
    flex-direction: row;
  }

  .nav-dev {
    margin-top: 0;
    padding-top: 0;
    border-top: 0;
  }

  .nav-dev-title,
  .tag {
    display: none;
  }
}
</style>
