<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, type Component } from 'vue'
import {
  ArrowDown,
  Box,
  Clock,
  Coin,
  Document,
  Monitor,
  Moon,
  Odometer,
  Refresh,
  Setting,
  Sunny,
  Tools,
} from '@element-plus/icons-vue'
import AuditLog from './views/AuditLog.vue'
import Diagnostics from './views/Diagnostics.vue'
import Instances from './views/Instances.vue'
import DeployService from './views/DeployService.vue'
import Images from './views/Images.vue'
import Jobs from './views/Jobs.vue'
import Overview from './views/Overview.vue'
import Settings from './views/Settings.vue'
import { api } from './api'
import { isMobile } from './media'
import { THEME_LABEL, isDark, setTheme, themeMode, type ThemeMode } from './theme'
import { useDiag } from './useDiag'

type ViewKey = 'overview' | 'instances' | 'deploy' | 'images' | 'audit' | 'jobs' | 'settings' | 'diag'

interface NavItem {
  key: ViewKey
  label: string
  icon: Component
}

const navs: NavItem[] = [
  { key: 'overview', label: '概览', icon: Odometer },
  { key: 'instances', label: '数据库', icon: Coin },
  { key: 'images', label: '镜像', icon: Box },
  { key: 'audit', label: '操作记录', icon: Document },
  // 这两页界面已画好，但后端 /jobs、/settings 还没实现（见 doc 待办），
  // 测试包里如实标成"待开发"，避免用户点进去以为坏了
  { key: 'jobs', label: '任务中心', icon: Clock },
  { key: 'settings', label: '设置', icon: Setting },
]

const VIEW_KEYS: ViewKey[] = ['overview', 'instances', 'images', 'audit', 'jobs', 'settings', 'diag']

// 部署页不是导航项：它从「数据库」页的按钮进来，靠返回/完成回列表
// 部署页可接受一个预选版本（从镜像页/列表带过来）
const deployVersion = ref('')
const deployNotice = ref<{ id: string; name: string; port: number } | null>(null)

// 支持 ?view=jobs 这类深链（通知里可以直接给链接）；没带参数就落到数据库页
function initialView(): ViewKey {
  const want = new URLSearchParams(window.location.search).get('view')
  return VIEW_KEYS.includes(want as ViewKey) ? (want as ViewKey) : 'instances'
}

// 数据库是主工作台，直接作为落地页；没有引擎时页面自己引导安装
const current = ref<ViewKey>(initialView())
const { report, loading, error, load } = useDiag()

const version = computed<string>(() => report.value?.env.version ?? '0.1.0')

// 顶栏任务角标：进行中的任务数，轻量轮询；接口没就绪时安静地不显示
const activeJobs = ref(0)
let jobsTimer: number | undefined

async function refreshJobs(): Promise<void> {
  try {
    const data = await api.listJobs()
    activeJobs.value = data.active
  } catch {
    activeJobs.value = 0
  }
}

// 图标放在 public 下，按页面 base 拼 URL：网关下页面在 /app/niudbm/，
// 直接写相对路径会被 Vite 当成模块导入，写绝对路径又会丢掉网关前缀。
const logoUrl = new URL('./icon.svg', document.baseURI).href

const title = computed<string>(() => {
  if (current.value === 'diag') return '平台自检'
  if (current.value === 'deploy') return '部署服务'
  return navs.find((nav) => nav.key === current.value)?.label ?? ''
})

const statusText = computed<string>(() => {
  if (loading.value) return '检测中…'
  // 前端单独预览（vite dev）时没有后端，这里如实说没连上，别让人误以为页面坏了
  if (error.value) return '后端未连接'
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

const statusTagType = computed<'success' | 'warning' | 'danger' | 'info'>(() => {
  return { ok: 'success', warn: 'warning', bad: 'danger', idle: 'info' }[statusKind.value] as
    | 'success'
    | 'warning'
    | 'danger'
    | 'info'
})

const themeOptions: ThemeMode[] = ['auto', 'light', 'dark']

const themeIcon = computed(() => {
  if (themeMode.value === 'auto') return Monitor
  return isDark.value ? Moon : Sunny
})

const themeText = computed<string>(() => THEME_LABEL[themeMode.value])

function go(view: ViewKey): void {
  current.value = view
}

function onOverviewNavigate(view: 'instances' | 'images' | 'jobs' | 'audit' | 'diag'): void {
  go(view)
}

// 数据库页把"部署服务"的活交给独立页面，避免四步向导挤在弹窗里
function onInstancesNavigate(view: 'images' | 'jobs' | 'deploy', version?: string): void {
  if (view === 'deploy') {
    deployVersion.value = version ?? ''
    current.value = 'deploy'
    return
  }
  go(view)
}

function onDeployDone(name: string, port: number, id: string): void {
  deployNotice.value = { id, name, port }
  current.value = 'instances'
}

function onSelect(index: string): void {
  go(index as ViewKey)
}

function onTheme(command: string | number | object): void {
  if (command === 'auto' || command === 'light' || command === 'dark') setTheme(command)
}

onMounted(() => {
  void load()
  void refreshJobs()
  jobsTimer = window.setInterval(() => void refreshJobs(), 15000)
})

onBeforeUnmount(() => {
  if (jobsTimer !== undefined) window.clearInterval(jobsTimer)
})
</script>

<template>
  <div class="shell">
    <aside class="side">
      <div class="brand">
        <img class="brand-logo" :src="logoUrl" alt="牛库管家" width="32" height="32" />
        <div class="brand-text">
          <strong>牛库管家</strong>
          <small>v{{ version }}</small>
        </div>
      </div>

      <el-menu
        class="nav"
        :mode="isMobile ? 'horizontal' : 'vertical'"
        :ellipsis="false"
        :default-active="current"
        @select="onSelect"
      >
        <el-menu-item v-for="nav in navs" :key="nav.key" :index="nav.key">
          <el-icon><component :is="nav.icon" /></el-icon>
          <span>{{ nav.label }}</span>
        </el-menu-item>
      </el-menu>

      <div class="nav-dev">
        <p class="nav-dev-title">开发</p>
        <el-menu
          class="nav"
          :mode="isMobile ? 'horizontal' : 'vertical'"
          :ellipsis="false"
          :default-active="current === 'diag' ? 'diag' : ''"
          @select="onSelect"
        >
          <el-menu-item index="diag">
            <el-icon><Tools /></el-icon>
            <span>平台自检</span>
          </el-menu-item>
        </el-menu>
      </div>
    </aside>

    <main class="main">
      <header class="topbar">
        <h1>{{ title }}</h1>
        <div class="topbar-right">
          <el-tag class="status" :type="statusTagType" size="small" round :effect="statusKind === 'idle' ? 'plain' : 'light'">
            {{ statusText }}
          </el-tag>
          <el-badge :value="activeJobs" :hidden="activeJobs === 0">
            <el-button size="small" :icon="Clock" @click="go('jobs')">任务</el-button>
          </el-badge>
          <el-button
            v-if="current === 'overview' || current === 'diag'"
            size="small"
            :icon="Refresh"
            :loading="loading"
            @click="load()"
          >
            重新检测
          </el-button>
          <el-dropdown trigger="click" @command="onTheme">
            <el-button size="small" :icon="themeIcon">
              <span class="theme-text">{{ themeText }}</span>
              <el-icon class="caret"><ArrowDown /></el-icon>
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item
                  v-for="option in themeOptions"
                  :key="option"
                  :command="option"
                  :class="{ picked: themeMode === option }"
                >
                  <el-icon>
                    <Monitor v-if="option === 'auto'" />
                    <Sunny v-else-if="option === 'light'" />
                    <Moon v-else />
                  </el-icon>
                  {{ THEME_LABEL[option] }}
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </header>

      <section class="content">
        <Overview v-if="current === 'overview'" @navigate="onOverviewNavigate" />
        <Instances v-else-if="current === 'instances'" :notice="deployNotice" @navigate="onInstancesNavigate" />
        <DeployService
          v-else-if="current === 'deploy'"
          :version="deployVersion"
          @done="onDeployDone"
          @cancel="go('instances')"
        />
        <Images v-else-if="current === 'images'" />
        <AuditLog v-else-if="current === 'audit'" />
        <Jobs v-else-if="current === 'jobs'" />
        <Settings v-else-if="current === 'settings'" />
        <Diagnostics v-else-if="current === 'diag'" />
      </section>
    </main>
  </div>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: 208px minmax(0, 1fr);
  height: 100%;
  min-height: 0;
}

.side {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 16px 10px;
  background: var(--bg-elevated);
  border-right: 1px solid var(--line);
  min-height: 0;
}

.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 8px;
}

/* 图标本身就是圆角方形，这里只负责尺寸与圆角裁切，不再自绘背景 */
.brand-logo {
  flex: none;
  width: 32px;
  height: 32px;
  border-radius: 9px;
  display: block;
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
  border-right: 0;
  background: transparent;
}

.nav :deep(.el-menu-item) {
  height: 38px;
  line-height: 38px;
  margin: 2px 0;
  padding: 0 10px;
  border-radius: 8px;
  font-size: 13px;
  color: var(--fg);
}

.nav :deep(.el-menu-item:hover) {
  background: var(--bg);
}

.nav :deep(.el-menu-item.is-active) {
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}

.nav :deep(.el-menu-item.is-active .el-icon) {
  color: var(--accent);
}

.tag {
  margin-left: auto;
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
  padding: 12px 20px;
  border-bottom: 1px solid var(--line);
  background: var(--bg-elevated);
}

.topbar h1 {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  white-space: nowrap;
}

.topbar-right {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.theme-text {
  margin: 0 4px;
}

.caret {
  color: var(--fg-muted);
  font-size: 11px;
}

.picked {
  color: var(--accent);
}

.content {
  flex: 1;
  min-height: 0;
  padding: 20px;
  overflow: auto;
}

/* 中等宽度（平板横屏、小笔记本）：侧栏收成图标条，把横向空间让给内容 */
@media (max-width: 1180px) and (min-width: 721px) {
  .shell {
    grid-template-columns: 76px minmax(0, 1fr);
  }

  .brand {
    justify-content: center;
    padding: 0;
  }

  .brand-text {
    display: none;
  }

  .nav :deep(.el-menu-item) {
    justify-content: center;
    padding: 0;
  }

  .nav :deep(.el-menu-item span) {
    display: none;
  }

  .nav :deep(.el-menu-item .el-icon) {
    margin-right: 0;
  }

  .nav-dev-title {
    display: none;
  }

  .nav-dev :deep(.el-menu-item) {
    justify-content: center;
    padding: 0;
  }

  .content {
    padding: 16px;
  }
}

/* 大屏：外框留白随屏幕放大，内容宽度由各页 .page 的 max-width 控制 */
@media (min-width: 1440px) {
  .shell {
    grid-template-columns: 224px minmax(0, 1fr);
  }

  .topbar {
    padding: 14px 28px;
  }

  .topbar h1 {
    font-size: 16px;
  }

  .content {
    padding: 24px 28px;
  }
}

@media (min-width: 1800px) {
  .content {
    padding: 28px 40px;
  }
}

@media (max-width: 720px) {
  .shell {
    grid-template-columns: minmax(0, 1fr);
    grid-template-rows: auto 1fr;
  }

  /* 手机上把侧栏变成顶部横条：左上角是应用图标，导航向右滑动 */
  .side {
    position: sticky;
    top: 0;
    z-index: 20;
    flex-direction: row;
    align-items: center;
    gap: 6px;
    padding: 6px 8px;
    padding-top: calc(6px + env(safe-area-inset-top));
    overflow-x: auto;
    overflow-y: hidden;
    border-right: 0;
    border-bottom: 1px solid var(--line);
    scrollbar-width: none;
  }

  .side::-webkit-scrollbar {
    display: none;
  }

  .brand {
    flex: none;
    padding: 0 2px 0 0;
  }

  .brand-logo {
    width: 30px;
    height: 30px;
    border-radius: 8px;
  }

  .brand-text {
    display: none;
  }

  .nav {
    flex: none;
    overflow: visible;
  }

  .nav :deep(.el-menu-item) {
    height: 34px;
    line-height: 34px;
    margin: 0 2px;
    padding: 0 10px;
    background: var(--bg);
    border-radius: 8px;
  }

  .tag {
    display: none;
  }

  .nav-dev {
    margin-top: 0;
    margin-left: auto;
    padding-top: 0;
    padding-left: 6px;
    border-top: 0;
    border-left: 1px solid var(--line);
  }

  .nav-dev-title {
    display: none;
  }

  .topbar {
    gap: 8px;
    padding: 8px 12px;
  }

  .topbar h1 {
    font-size: 14px;
  }

  .theme-text {
    display: none;
  }

  .caret {
    display: none;
  }

  .content {
    padding: 12px 12px calc(12px + env(safe-area-inset-bottom));
  }
}
</style>
