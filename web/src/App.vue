<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, type Component } from 'vue'
import {
  ArrowDown,
  Box,
  Clock,
  Coin,
  Document,
  Files,
  MoreFilled,
  Monitor,
  Moon,
  Odometer,
  Refresh,
  Setting,
  Sunny,
  SwitchButton,
  Tools,
  User,
} from '@element-plus/icons-vue'
import AuditLog from './views/AuditLog.vue'
import Diagnostics from './views/Diagnostics.vue'
import Instances from './views/Instances.vue'
import DeployService from './views/DeployService.vue'
import Images from './views/Images.vue'
import Jobs from './views/Jobs.vue'
import Login from './views/Login.vue'
import Overview from './views/Overview.vue'
import Settings from './views/Settings.vue'
import BackupCenter from './views/BackupCenter.vue'
import { UNAUTHORIZED_EVENT, api } from './api'
import { useAuth } from './auth'
import { isMobile } from './media'
import { THEME_LABEL, isDark, setTheme, themeMode, type ThemeMode } from './theme'
import { useDiag } from './useDiag'
import { appRoute, navigate, type ViewKey } from './router'
import { assetUrl } from './assets'

interface NavItem {
  key: ViewKey
  label: string
  icon: Component
}

const navs: NavItem[] = [
  { key: 'overview', label: '工作台', icon: Odometer },
  { key: 'instances', label: '数据库服务', icon: Coin },
  { key: 'backups', label: '备份与恢复', icon: Files },
  { key: 'jobs', label: '任务中心', icon: Clock },
  { key: 'images', label: '镜像', icon: Box },
  { key: 'audit', label: '操作记录', icon: Document },
  { key: 'settings', label: '设置', icon: Setting },
]

// 部署页不是导航项：它从「数据库」页的按钮进来，靠返回/完成回列表
// 部署页可接受一个预选版本（从镜像页/列表带过来）
const deployVersion = ref('')
// 部署向导按引擎给不同的版本清单与文案：MySQL 与 PostgreSQL 不能混着填
const deployEngine = ref('mysql')
// 数据库页当前该看哪个引擎：部署 PG 之后必须落到 PostgreSQL 页签，
// 否则新建好的服务会"消失"在过滤之外
const instancesEngine = ref<'mysql' | 'postgresql' | 'mongodb'>('mysql')
const deployNotice = ref<{ id: string; name: string; port: number } | null>(null)
const mobileMore = ref(false)

const current = computed<ViewKey>(() => appRoute.value.view)
const { report, loading, error, load } = useDiag()

/*
 * 登录闸门：没登录就只渲染登录页。
 *
 * 应用不自建账号，身份来自飞牛网关，登录本身只是「用当前身份换一份会话」；
 * 但没有这一步的话，退出登录会在下一个请求上被网关身份头立刻带回界面里。
 */
const { state: authState, error: authError, load: loadAuth, logout } = useAuth()
const authReady = ref(false)
const authed = computed(() => authState.value?.authenticated === true)
const currentUser = computed<string>(() => authState.value?.user?.username ?? '')
const systemAdmin = computed<boolean>(() => authState.value?.user?.isAdmin === true)
const visibleNavs = computed<NavItem[]>(() =>
  systemAdmin.value
    ? navs
    : navs.filter((item) => !['images', 'audit', 'settings'].includes(item.key)),
)

const version = computed<string>(() => report.value?.env.version ?? '1.0.0')

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
const logoUrl = assetUrl('icon.svg')

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
  mobileMore.value = false
  navigate({ view })
}

function onOverviewNavigate(view: 'instances' | 'images' | 'jobs' | 'audit' | 'diag'): void {
  go(view)
}

// 数据库页把"部署服务"的活交给独立页面，避免四步向导挤在弹窗里
function onInstancesNavigate(view: 'images' | 'jobs' | 'deploy', version?: string, engine?: string): void {
  if (view === 'deploy') {
    deployVersion.value = version ?? ''
    deployEngine.value = engine || 'mysql'
    if (engine === 'postgres') instancesEngine.value = 'postgresql'
    else if (engine === 'mysql') instancesEngine.value = 'mysql'
    navigate({ view: 'deploy' })
    return
  }
  go(view)
}

function onDeployDone(name: string, port: number, id: string): void {
  deployNotice.value = { id, name, port }
  // 落到刚部署的那个引擎的页签上，服务才会出现在列表里
  instancesEngine.value = deployEngine.value === 'postgres' ? 'postgresql' : 'mysql'
  navigate({ view: 'instances' })
}

function openService(id: string, tab = 'overview'): void {
  navigate({ view: 'instances', instanceId: id, tab })
}

function closeService(): void {
  navigate({ view: 'instances' })
}

function changeServiceTab(id: string, tab: string): void {
  navigate({ view: 'instances', instanceId: id, tab }, true)
}

function onSelect(index: string): void {
  go(index as ViewKey)
}

function onTheme(command: string | number | object): void {
  if (command === 'auto' || command === 'light' || command === 'dark') setTheme(command)
}

function onAccount(command: string | number | object): void {
  if (command === 'logout') void onLogout()
}

/** 登录之后才开始拉数据：登录页不该先发一堆注定 401 的请求。 */
function startWork(): void {
  void load()
  void refreshJobs()
  if (jobsTimer === undefined) jobsTimer = window.setInterval(() => void refreshJobs(), 15000)
}

function stopWork(): void {
  if (jobsTimer !== undefined) {
    window.clearInterval(jobsTimer)
    jobsTimer = undefined
  }
}

/** 任何接口报 401（会话过期、被注销）都回到登录页重新问一次身份。 */
function onUnauthorized(): void {
  stopWork()
  void loadAuth()
}

async function onLoggedIn(): Promise<void> {
  await loadAuth()
  if (authed.value) startWork()
}

async function onLogout(): Promise<void> {
  stopWork()
  await logout()
}

onMounted(async () => {
  window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
  await loadAuth()
  authReady.value = true
  if (authed.value) startWork()
})

onBeforeUnmount(() => {
  window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
  stopWork()
})
</script>

<template>
  <!-- 首屏先问一次「我是谁」，避免界面闪一下再跳登录页 -->
  <div v-if="!authReady" class="boot">正在检查登录状态…</div>
  <Login v-else-if="!authed" :state="authState" :error="authError" @done="onLoggedIn" />

  <div v-else class="shell">
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
        <el-menu-item v-for="nav in visibleNavs" :key="nav.key" :index="nav.key">
          <el-icon><component :is="nav.icon" /></el-icon>
          <span>{{ nav.label }}</span>
        </el-menu-item>
      </el-menu>

      <div v-if="systemAdmin" class="nav-dev">
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

          <!-- 当前账号：身份来自 NAS，这里只展示，不提供切换账号（换账号等于换 NAS 登录） -->
          <el-dropdown trigger="click" @command="onAccount">
            <el-button size="small" :icon="User">
              <span class="account-text">{{ currentUser }}</span>
              <el-icon class="caret"><ArrowDown /></el-icon>
            </el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item disabled>
                  <span class="account-menu-name">{{ currentUser }} · {{ systemAdmin ? 'NAS 管理员' : '数据库成员' }}</span>
                </el-dropdown-item>
                <el-dropdown-item divided command="logout">
                  <el-icon><SwitchButton /></el-icon>
                  退出登录
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </header>

      <section class="content">
        <Overview v-if="current === 'overview'" @navigate="onOverviewNavigate" @open="openService" />
        <Instances
          v-else-if="current === 'instances'"
          :notice="deployNotice"
          :initial-engine="instancesEngine"
          :initial-id="appRoute.instanceId"
          :initial-tab="appRoute.tab"
          @navigate="onInstancesNavigate"
          @open="openService"
          @tab-change="changeServiceTab"
          @back="closeService"
        />
        <DeployService
          v-else-if="current === 'deploy'"
          :version="deployVersion"
          :engine="deployEngine"
          @done="onDeployDone"
          @cancel="go('instances')"
        />
        <Images v-else-if="current === 'images' && systemAdmin" />
        <BackupCenter v-else-if="current === 'backups'" @open="openService" />
        <AuditLog v-else-if="current === 'audit' && systemAdmin" />
        <Jobs v-else-if="current === 'jobs'" />
        <Settings v-else-if="current === 'settings' && systemAdmin" />
        <Diagnostics v-else-if="current === 'diag' && systemAdmin" />
        <el-result v-else icon="warning" title="没有访问权限" sub-title="当前账号不能访问这个页面。">
          <template #extra>
            <el-button type="primary" @click="go('overview')">返回工作台</el-button>
          </template>
        </el-result>
      </section>
    </main>

    <nav class="mobile-nav" aria-label="主导航">
      <button
        v-for="item in visibleNavs.slice(0, 4)"
        :key="item.key"
        type="button"
        :class="{ active: current === item.key || (item.key === 'instances' && appRoute.instanceId) }"
        @click="go(item.key)"
      >
        <el-icon><component :is="item.icon" /></el-icon>
        <span>{{ item.key === 'instances' ? '数据库' : item.label }}</span>
      </button>
      <button type="button" :class="{ active: mobileMore }" @click="mobileMore = true">
        <el-icon><MoreFilled /></el-icon>
        <span>更多</span>
      </button>
    </nav>
  </div>

  <el-drawer
    v-if="authed"
    v-model="mobileMore"
    title="更多"
    direction="btt"
    size="auto"
    class="mobile-more"
    @closed="mobileMore = false"
  >
    <div class="more-grid">
      <button v-if="systemAdmin" type="button" @click="go('images')">
        <el-icon><Box /></el-icon><span>镜像管理</span>
      </button>
      <button v-if="systemAdmin" type="button" @click="go('audit')">
        <el-icon><Document /></el-icon><span>操作记录</span>
      </button>
      <button v-if="systemAdmin" type="button" @click="go('settings')">
        <el-icon><Setting /></el-icon><span>设置</span>
      </button>
      <button v-if="systemAdmin" type="button" @click="go('diag')">
        <el-icon><Tools /></el-icon><span>平台自检</span>
      </button>
      <button type="button" @click="mobileMore = false; void onLogout()">
        <el-icon><SwitchButton /></el-icon><span>退出登录</span>
      </button>
    </div>
  </el-drawer>
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: 208px minmax(0, 1fr);
  height: 100%;
  min-height: 0;
}

.mobile-nav {
  display: none;
}

.more-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
}

.more-grid button {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 48px;
  padding: 10px 14px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--bg);
  color: var(--fg);
  text-align: left;
}

.more-grid .el-icon {
  color: var(--accent);
  font-size: 18px;
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

.boot {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--fg-muted);
  font-size: 13px;
}

.theme-text {
  margin: 0 4px;
}

.account-text {
  margin: 0 4px;
  max-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.account-menu-name {
  font-size: 12px;
  color: var(--fg-muted);
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
    grid-template-rows: minmax(0, 1fr) auto;
  }

  .side {
    display: none;
  }

  .mobile-nav {
    display: grid;
    grid-template-columns: repeat(5, minmax(0, 1fr));
    padding: 4px 4px calc(4px + env(safe-area-inset-bottom));
    border-top: 1px solid var(--line);
    background: var(--bg-elevated);
  }

  .mobile-nav button {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 2px;
    min-width: 0;
    min-height: 48px;
    border: 0;
    border-radius: 7px;
    background: transparent;
    color: var(--fg-muted);
    font-size: 11px;
  }

  .mobile-nav button.active {
    background: var(--accent-soft);
    color: var(--accent);
  }

  .mobile-nav .el-icon {
    font-size: 18px;
  }

  .topbar {
    gap: 8px;
    padding: 8px 12px;
  }

  .topbar h1 {
    font-size: 14px;
  }

  .status {
    display: none;
  }

  .theme-text,
  .account-text {
    display: none;
  }

  .caret {
    display: none;
  }

  .content {
    padding: 12px;
  }
}
</style>
