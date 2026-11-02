<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api, type CharsetOption, type ConnectionInfo, type DatabaseItem, type DbUser, type Instance } from '../api'
import { ElMessageBox } from 'element-plus'
import { errText, formatBytes, formatTime } from '../format'
import Modal from '../components/Modal.vue'
import ConfigPanel from '../components/ConfigPanel.vue'
import ConfigFilePanel from '../components/ConfigFilePanel.vue'
import UserDrawer from '../components/UserDrawer.vue'
import ConnDrawer from '../components/ConnDrawer.vue'
import ViewSwitch from '../components/ViewSwitch.vue'
import Pager from '../components/Pager.vue'
import { usePager } from '../pager'
import TransferPanel from '../components/TransferPanel.vue'
import { copyText, randomPassword } from '../clip'
import { isCardView } from '../view-mode'
import BackupPanel from '../components/BackupPanel.vue'

type TabKey = 'overview' | 'databases' | 'config' | 'cnf' | 'backups' | 'transfer' | 'danger'

const props = defineProps<{ instanceId: string; initialTab?: TabKey | 'users' }>()
const emit = defineEmits<{ back: []; changed: [] }>()

const ALL_TABS: { key: TabKey; label: string }[] = [
  { key: 'overview', label: '概览' },
  // 库与账号是同一件事：账号本来就是按库授权的，分两个页签要来回切着看
  { key: 'databases', label: '数据库与账号' },
  { key: 'config', label: '配置' },
  // 白名单之外要写的参数只能手改文件，单独一个页签比藏在配置页底部好用
  { key: 'cnf', label: '配置文件' },
  { key: 'backups', label: '备份与恢复' },
  { key: 'transfer', label: '导入导出' },
  { key: 'danger', label: '设置' },
]

// 老链接/老按钮里的 ?tab=users 已经没有独立页签了，落到合并后的页面，别让用户撞上空页面
function normalizeTab(key: TabKey | 'users'): TabKey {
  return key === 'users' ? 'databases' : key
}

const STATUS_TEXT: Record<string, string> = {
  running: '运行中',
  stopped: '已停止',
  error: '异常',
  restoring: '恢复中',
}

const PROFILE_TEXT: Record<string, string> = {
  low: '轻量',
  standard: '标准',
  performance: '高性能',
}

const inst = ref<Instance | null>(null)
const loading = ref(false)
const error = ref('')
// 列表页可以带着"用户/配置/备份"这类意图进来，少一次手动点页签
const tab = ref<TabKey>(normalizeTab(props.initialTab ?? 'overview'))
watch(
  () => props.instanceId,
  () => {
    tab.value = normalizeTab(props.initialTab ?? 'overview')
  },
)

const renaming = ref(false)
const newName = ref('')
const renameError = ref('')

const deleting = ref(false)
const confirmName = ref('')
const keepData = ref(false)
const deleteError = ref('')

const busy = ref(false)

// PostgreSQL 实例的额外信息：连接串与开关机
const conn = ref<ConnectionInfo | null>(null)
const showPwd = ref(false)
const copyHint = ref('')
const connectionError = ref('')
// 常驻引擎（PostgreSQL / MySQL）才有连接信息、启停与用户管理
// 这一版本只有 MySQL：实例都是常驻容器，详情页不再按引擎分支。
// 原来 isServer 用来区分"服务型实例"和 SQLite 文件库，现在恒为真。
const isServer = true
// 「数据库」页签只有 MySQL 家族有意义：一个服务里放多个库
const isMySQL = computed(() => inst.value?.engine === 'mysql')
const TABS = computed(() => ALL_TABS.filter((t) => t.key !== 'databases' || isMySQL.value))
// 地址是 host:port：局域网里其他应用就靠这一串连进来
const addr = computed(() => (conn.value ? `${conn.value.host}:${conn.value.port}` : ''))
const title = computed(() => inst.value?.name ?? '数据库')
const statusClass = computed(() => {
  const s = inst.value?.status
  if (s === 'running') return 'pill-ok'
  if (s === 'error') return 'pill-bad'
  return 'pill-warn'
})

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    inst.value = await api.getInstance(props.instanceId)
    syncQuota()
    await loadConn()
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}

// loadConn 只对常驻引擎有意义；SQLite 没有连接信息，失败也不该占错误提示
async function loadConn(): Promise<void> {
  if (false) {
    conn.value = null
    return
  }
  connectionError.value = ''
  try {
    conn.value = await api.connection(props.instanceId)
  } catch (e) {
    conn.value = null
    connectionError.value = errText(e)
  }
}

async function copy(value: string, label: string): Promise<void> {
  if (!value) return
  copyHint.value = await copyText(value) ? `${label}已复制` : '复制失败，请选中文本后手动复制'
}

async function togglePower(): Promise<void> {
  if (!inst.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    if (inst.value.status === 'running') {
      await api.stopInstance(props.instanceId)
    } else {
      await api.startInstance(props.instanceId)
    }
    await load()
    emit('changed')
  } catch (e) {
    error.value = errText(e)
  } finally {
    busy.value = false
  }
}

function openRename(): void {
  newName.value = inst.value?.name ?? ''
  renameError.value = ''
  renaming.value = true
}

async function submitRename(): Promise<void> {
  if (busy.value || !newName.value.trim()) return
  busy.value = true
  renameError.value = ''
  try {
    inst.value = await api.renameInstance(props.instanceId, newName.value.trim())
    renaming.value = false
    emit('changed')
  } catch (e) {
    renameError.value = errText(e)
  } finally {
    busy.value = false
  }
}

function openDelete(): void {
  confirmName.value = ''
  keepData.value = false
  deleteError.value = ''
  deleting.value = true
}

async function submitDelete(): Promise<void> {
  if (busy.value || !inst.value) return
  busy.value = true
  deleteError.value = ''
  try {
    await api.deleteInstance(props.instanceId, confirmName.value, keepData.value)
    deleting.value = false
    emit('changed')
    emit('back')
  } catch (e) {
    deleteError.value = errText(e)
  } finally {
    busy.value = false
  }
}

/* ---------------- 实例（一个服务下的多个逻辑库） ---------------- */

const dbs = ref<DatabaseItem[]>([])

// 库列表同样分页：一个服务里几十个库时，一屏铺下来看不清
const { page: dbPage, pageSize: dbPageSize, total: dbTotal, items: pagedDbs } = usePager(dbs, 10)
const dbLoading = ref(false)
const dbError = ref('')
const dbBusy = ref(false)
// 用户抽屉：账号是库级授权的产物，单独一层滑出，不占页面正文
const usersOpen = ref(false)
// 连接信息独立成抽屉：容器地址/外部地址/远程开关/root 密码都在里面
const connOpen = ref(false)
// 授权管理弹窗：这张表回答"这个库上有哪些账号、各自什么档位"，并能就地撤销
const LEVEL_TEXT: Record<string, string> = {
  none: '无权限',
  read: '只读',
  write: '读写',
  all: '全权',
}

function grantLevelOf(u: DbUser): string {
  const db = grantDB.value
  if (!db) return ''
  const g = (u.grants ?? []).find((x) => x.database === db.name)
  return g ? LEVEL_TEXT[g.level] ?? g.level : ''
}

const grantDB = ref<DatabaseItem | null>(null)
const grantUsers = ref<DbUser[]>([])
const grantLoading = ref(false)
const grantError = ref('')

async function openGrants(db: DatabaseItem): Promise<void> {
  grantDB.value = db
  grantLoading.value = true
  grantError.value = ''
  try {
    const data = await api.listUsers(props.instanceId)
    grantUsers.value = data.items.filter((u) => (u.grants ?? []).some((g) => g.database === db.name))
  } catch (e) {
    grantError.value = errText(e)
    grantUsers.value = []
  } finally {
    grantLoading.value = false
  }
}

// 撤销授权只动这一个库：其余库的授权原样传回去，不能一把清空
async function revokeGrant(u: DbUser): Promise<void> {
  const db = grantDB.value
  if (!db) return
  const rest = (u.grants ?? []).filter((g) => g.database !== db.name)
  grantError.value = ''
  try {
    await api.updateUser(props.instanceId, u.name, { grants: rest })
    await openGrants(db)
    await loadDatabases()
  } catch (e) {
    grantError.value = errText(e)
  }
}

async function loadDatabases(): Promise<void> {
  dbLoading.value = true
  dbError.value = ''
  try {
    const d = await api.listDatabases(props.instanceId)
    dbs.value = d.items
  } catch (e) {
    dbError.value = errText(e)
  } finally {
    dbLoading.value = false
  }
}

const creatingDB = ref(false)
const dbFormError = ref('')
const dbForm = ref({ name: '', charset: 'utf8mb4', withUser: false, user: '', password: '', level: 'write' })

/**
 * 字符集候选以引擎自己报的为准（不同版本能建的不一样：gb18030 到 5.7.4 才有，
 * utf8mb3 这个名字 8.0.28 才出现）。接口取不到时用这几个常用项兜底。
 */
const FALLBACK_CHARSETS: CharsetOption[] = [
  { value: 'utf8mb4', label: 'utf8mb4 · 推荐，支持 emoji', collation: 'utf8mb4_general_ci', recommended: true },
  { value: 'utf8', label: 'utf8 · 兼容旧程序', collation: 'utf8_general_ci' },
  { value: 'gbk', label: 'gbk · 中文旧系统', collation: 'gbk_chinese_ci' },
  { value: 'latin1', label: 'latin1 · 仅英文', collation: 'latin1_swedish_ci' },
]

const charsets = ref<CharsetOption[]>(FALLBACK_CHARSETS)
const charsetsLoaded = ref(false)

async function loadCharsets(): Promise<void> {
  if (charsetsLoaded.value) return
  try {
    const res = await api.listCharsets(props.instanceId)
    if (res.items && res.items.length) {
      charsets.value = res.items
      charsetsLoaded.value = true
    }
  } catch {
    // 实例没跑或接口暂不可用时保持兜底清单，不打断建库流程
  }
}

const LEVELS = [
  { value: 'read', label: '只读' },
  { value: 'write', label: '读写' },
  { value: 'all', label: '全权' },
]

function openCreateDB(): void {
  dbForm.value = {
    name: '',
    charset: 'utf8mb4',
    withUser: true,
    user: '',
    password: randomPassword(),
    level: 'write',
  }
  dbFormError.value = ''
  creatingDB.value = true
  void loadCharsets()
}

async function submitCreateDB(): Promise<void> {
  if (dbBusy.value) return
  const name = dbForm.value.name.trim()
  if (!name) {
    dbFormError.value = '请填写库名'
    return
  }
  const users = dbForm.value.withUser
    ? [{ name: dbForm.value.user.trim(), password: dbForm.value.password, level: dbForm.value.level }]
    : []
  if (dbForm.value.withUser) {
    if (!users[0].name) {
      dbFormError.value = '请填写账号名，或取消「同时创建授权账号」'
      return
    }
    if (users[0].password.length < 8) {
      dbFormError.value = '账号密码至少 8 位'
      return
    }
  }
  dbBusy.value = true
  dbFormError.value = ''
  try {
    await api.createDatabase(props.instanceId, { name, charset: dbForm.value.charset, users })
    creatingDB.value = false
    await Promise.all([loadDatabases(), load()])
    emit('changed')
  } catch (e) {
    dbFormError.value = errText(e)
  } finally {
    dbBusy.value = false
  }
}

const droppingDB = ref<{ name: string; confirm: string; error: string } | null>(null)

function openDropDB(name: string): void {
  droppingDB.value = { name, confirm: '', error: '' }
}

async function submitDropDB(): Promise<void> {
  const target = droppingDB.value
  if (!target || dbBusy.value) return
  if (target.confirm !== target.name) {
    target.error = '请输入库名以确认删除'
    return
  }
  dbBusy.value = true
  try {
    await api.dropDatabase(props.instanceId, target.name, target.confirm)
    droppingDB.value = null
    await Promise.all([loadDatabases(), load()])
    emit('changed')
  } catch (e) {
    target.error = errText(e)
  } finally {
    dbBusy.value = false
  }
}

/* ---------------- 给已有库补账号 ---------------- */

// 库早在、后来才要加接入账号：账号只授这一个库，和建库时一并建号是同一种权限模型
const addingUser = ref<{ db: string; name: string; password: string; level: string; error: string } | null>(null)

function openAddUser(db: string): void {
  addingUser.value = { db, name: '', password: randomPassword(), level: 'write', error: '' }
}

async function submitAddUser(): Promise<void> {
  const t = addingUser.value
  if (!t || dbBusy.value) return
  if (!t.name.trim()) {
    t.error = '请填写账号名'
    return
  }
  if (t.password.length < 8) {
    t.error = '密码至少 8 位'
    return
  }
  dbBusy.value = true
  t.error = ''
  try {
    await api.createDatabaseUser(props.instanceId, t.db, {
      name: t.name.trim(),
      password: t.password,
      level: t.level,
    })
    addingUser.value = null
    await loadDatabases()
  } catch (e) {
    t.error = errText(e)
  } finally {
    dbBusy.value = false
  }
}

/* ---------------- 资源配额 ---------------- */

const CPU_CHOICES = [
  { value: 0, label: '不限制' },
  { value: 0.5, label: '0.5 核' },
  { value: 1, label: '1 核' },
  { value: 2, label: '2 核' },
  { value: 4, label: '4 核' },
]
const MEM_CHOICES = [
  { value: 0, label: '不限制' },
  { value: 512, label: '512 MB' },
  { value: 1024, label: '1 GB' },
  { value: 2048, label: '2 GB' },
  { value: 4096, label: '4 GB' },
  { value: 8192, label: '8 GB' },
]

const quotaForm = ref({ cpuLimit: 0, memLimitMB: 0, port: 0 })
const quotaBusy = ref(false)
const quotaHint = ref('')

// 改端口 = 换容器名 + 重建容器，所以单独一个按钮，别和配额混在一起
const portBusy = ref(false)
const portHint = ref('')

function syncQuota(): void {
  quotaForm.value = {
    cpuLimit: inst.value?.cpuLimit ?? 0,
    memLimitMB: inst.value?.memLimitMB ?? 0,
    port: inst.value?.port ?? 0,
  }
}

async function savePort(): Promise<void> {
  if (portBusy.value) return
  const p = Number(quotaForm.value.port)
  if (!Number.isInteger(p) || p < 1024 || p > 65535) {
    portHint.value = '端口只能是 1024 ~ 65535 的整数'
    return
  }
  if (p === inst.value?.port) {
    portHint.value = '端口没变'
    return
  }
  portBusy.value = true
  portHint.value = ''
  try {
    inst.value = await api.updateLimits(props.instanceId, { port: p })
    syncQuota()
    portHint.value = '端口已切换，容器已按新名字重建'
    emit('changed')
  } catch (e) {
    portHint.value = errText(e)
  } finally {
    portBusy.value = false
  }
}

async function saveQuota(): Promise<void> {
  if (quotaBusy.value) return
  // 资源限制写在容器配置里，改完必须重启容器才会按新限制跑；
  // 这件事不告诉用户，他会以为保存完就生效了。
  try {
    await ElMessageBox.confirm(
      '修改资源限制需要重启服务才能生效。重启期间连接会中断几秒，正在执行的语句也会被断开。',
      '确认修改资源限制',
      { type: 'warning', confirmButtonText: '保存并重启', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  quotaBusy.value = true
  quotaHint.value = ''
  try {
    inst.value = await api.updateLimits(props.instanceId, {
      cpuLimit: quotaForm.value.cpuLimit,
      memLimitMB: quotaForm.value.memLimitMB,
    })
    await api.restartInstance(props.instanceId)
    syncQuota()
    await load()
    quotaHint.value = '已保存并重启，新限制已生效'
    emit('changed')
  } catch (e) {
    quotaHint.value = errText(e)
  } finally {
    quotaBusy.value = false
  }
}

/* ---------------- 重启 ---------------- */

async function restart(): Promise<void> {
  if (!inst.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    await api.restartInstance(props.instanceId)
    await load()
    emit('changed')
  } catch (e) {
    error.value = errText(e)
  } finally {
    busy.value = false
  }
}

// 备份/恢复会改变体积与状态，回到详情时刷新一次头部信息
watch(tab, (next) => {
  showPwd.value = false
  copyHint.value = ''
  if (next === 'overview') void load()
  if (next === 'databases') void loadDatabases()
})

onMounted(async () => {
  await load()
  if (props.initialTab === 'databases' && isMySQL.value) await loadDatabases()
})
</script>

<template>
  <div class="page">
    <!-- 服务头：状态与关键指标一行看完，操作在右手边 -->
    <header class="hero">
      <button type="button" class="btn-back" title="返回列表" @click="emit('back')">←</button>
      <div class="hero-main">
        <div class="hero-title">
          <h1>{{ title }}</h1>
          <span v-if="inst" class="pill" :class="statusClass">
            {{ STATUS_TEXT[inst.status] ?? inst.status }}
          </span>
        </div>
        <div class="hero-meta">
          <span v-if="inst" class="mono">MySQL {{ inst.engineVersion }}</span>
          <span v-if="inst && inst.port" class="mono">端口 {{ inst.port }}</span>
          <span v-if="inst">{{ formatBytes(inst.sizeBytes) }}</span>
          <span v-if="inst && inst.tableCount !== undefined">{{ inst.tableCount }} 张表</span>
          <span v-if="inst" class="muted">创建于 {{ formatTime(inst.createdAt) }}</span>
        </div>
      </div>
      <div class="hero-actions">
        <button v-if="inst" type="button" class="btn btn-sm" @click="connOpen = true">连接信息</button>
        <button v-if="inst" type="button" class="btn btn-sm" @click="usersOpen = true">用户</button>
        <button
          v-if="inst && inst.status === 'running'"
          type="button"
          class="btn btn-sm"
          :disabled="busy"
          @click="restart"
        >
          重启
        </button>
        <button
          v-if="inst"
          type="button"
          class="btn btn-sm"
          :class="inst.status === 'running' ? '' : 'btn-primary'"
          :disabled="busy"
          @click="togglePower"
        >
          {{ inst.status === 'running' ? '停止' : '启动' }}
        </button>
        <button type="button" class="btn btn-sm btn-ghost" :disabled="loading" @click="load()" title="刷新">
          {{ loading ? '…' : '↻' }}
        </button>
      </div>
    </header>

    <div v-if="error" class="notice notice-bad">
      <span>{{ error }}</span>
      <button type="button" class="btn btn-sm" @click="load()">重试</button>
    </div>

    <template v-else-if="inst">
      <nav class="tabs" aria-label="实例管理">
        <button v-for="t in TABS" :key="t.key" type="button" class="tab"
          :class="{ active: tab === t.key, danger: t.key === 'danger' }"
          :aria-current="tab === t.key ? 'page' : undefined" @click="tab = t.key">
          {{ t.label }}
        </button>
      </nav>

      <!-- 概览：左连接右资源，下面是服务信息，三块都只放一眼要看的东西 -->
      <div v-if="tab === 'overview'" class="ov">
        <section v-if="isServer" class="card">
          <header class="card-head">
            <h2>连接信息</h2>
            <button type="button" class="btn-link" @click="connOpen = true">容器 / 外部地址 ›</button>
          </header>
          <p v-if="connectionError" class="notice notice-warn">{{ connectionError }}</p>
          <dl class="kv">
            <dt>地址</dt>
            <dd class="mono pwd">
              <span>{{ addr || '—' }}</span>
              <button type="button" class="btn btn-sm" :disabled="!conn" @click="copy(addr, '地址')">复制</button>
            </dd>
            <dt>用户</dt>
            <dd class="mono pwd">
              <span>{{ conn?.user ?? '—' }}</span>
              <button type="button" class="btn btn-sm" :disabled="!conn"
                @click="copy(conn?.user ?? '', '用户名')">复制</button>
            </dd>
            <dt>密码</dt>
            <dd class="mono pwd">
              <span>{{ showPwd ? conn?.password || '—' : '••••••••' }}</span>
              <button type="button" class="btn btn-sm" :disabled="!conn" @click="showPwd = !showPwd">
                {{ showPwd ? '隐藏' : '显示' }}
              </button>
              <button type="button" class="btn btn-sm" :disabled="!conn"
                @click="copy(conn?.password ?? '', '密码')">复制</button>
            </dd>
          </dl>
          <p v-if="copyHint" role="status">{{ copyHint }}</p>
          <p class="tip">
            root 用于建库、建账号；应用接入请在「数据库与账号」里按库建最小权限账号。
          </p>
        </section>

        <section class="card">
          <header class="card-head">
            <h2>运行资源</h2>
          </header>
          <div class="res-grid">
            <div class="field">
              <label for="q-cpu">CPU 上限</label>
              <select id="q-cpu" v-model.number="quotaForm.cpuLimit" class="select" :disabled="quotaBusy">
                <option v-for="c in CPU_CHOICES" :key="c.value" :value="c.value">{{ c.label }}</option>
              </select>
            </div>
            <div class="field">
              <label for="q-mem">内存上限</label>
              <select id="q-mem" v-model.number="quotaForm.memLimitMB" class="select" :disabled="quotaBusy">
                <option v-for="m in MEM_CHOICES" :key="m.value" :value="m.value">{{ m.label }}</option>
              </select>
            </div>
            <div class="field">
              <label>端口</label>
              <div class="row">
                <input v-model.number="quotaForm.port" class="input mono" inputmode="numeric" :disabled="portBusy" />
                <button type="button" class="btn btn-sm" :disabled="portBusy" @click="savePort">
                  {{ portBusy ? '…' : '保存' }}
                </button>
              </div>
            </div>
          </div>
          <div class="res-foot">
            <button type="button" class="btn btn-sm" :disabled="quotaBusy" @click="saveQuota">
              {{ quotaBusy ? '保存中…' : '保存 CPU / 内存' }}
            </button>
            <span class="field-hint">
              {{ quotaHint || portHint || '改资源限制与会重启服务；改端口会重建容器，数据不受影响。' }}
            </span>
          </div>
        </section>

        <section class="card ov-wide">
          <header class="card-head">
            <h2>服务信息</h2>
          </header>
          <dl class="kv kv-grid">
            <dt>引擎版本</dt><dd>{{ inst.engine }} {{ inst.engineVersion || '内置' }}</dd>
            <dt>运行方式</dt><dd>Docker 容器</dd>
            <dt>存储方式</dt>
            <dd>{{ inst.storageKind === 'volume' ? 'Docker 命名卷' : '宿主机目录' }}</dd>
            <dt>数据位置</dt><dd class="mono ellipsis" :title="inst.filePath">{{ inst.filePath }}</dd>
            <dt>创建时间</dt><dd>{{ formatTime(inst.createdAt) }}</dd>
            <dt v-if="inst.statusReason">状态说明</dt>
            <dd v-if="inst.statusReason" class="bad-text">{{ inst.statusReason }}</dd>
          </dl>
        </section>
      </div>

      <section v-if="tab === 'databases'" class="card">
        <div class="row field-head">
          <h2>数据库与账号（{{ dbs.length }} 个库）</h2>
          <div class="grow" />
          <ViewSwitch />
          <button type="button" class="btn btn-sm" @click="usersOpen = true">用户</button>
          <button type="button" class="btn btn-sm" :disabled="dbLoading" @click="loadDatabases()">
            {{ dbLoading ? '读取中' : '刷新' }}
          </button>
          <button
            type="button"
            class="btn btn-sm btn-primary"
            :disabled="inst.status !== 'running'"
            :title="inst.status === 'running' ? '' : '服务未运行，先启动再建库'"
            @click="openCreateDB"
          >
            新建数据库
          </button>
        </div>
        <p class="tip">
          这是端口 {{ inst.port || '—' }} 上的 MySQL 服务，里面可以建多个数据库。
          它们共用这个服务的连接地址和账号体系，各自有独立的表与数据。
          「授权用户」是这个库上能进得来的账号——点开看全部，改授权在右上角「用户」里。
        </p>
        <div v-if="dbError" class="notice notice-bad">
          <span>{{ dbError }}</span>
          <button type="button" class="btn btn-sm" @click="loadDatabases()">重试</button>
        </div>
        <p v-else-if="dbLoading && !dbs.length" class="muted">读取中…</p>

        <table v-else-if="dbs.length && !isCardView" class="data">
          <thead>
            <tr>
              <th>库名</th>
              <th>字符集</th>
              <th>表数</th>
              <th>大小</th>
              <th>授权用户</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <template v-for="d in pagedDbs" :key="d.name">
              <tr>
                <td class="mono">{{ d.name }}</td>
                <td>{{ d.charset }}</td>
                <td>{{ d.tableCount }}</td>
                <td>{{ formatBytes(d.sizeBytes) }}</td>
                <td>
                  <button type="button" class="btn-link" @click="openGrants(d)">
                    {{ (d.users && d.users.length) || 0 }} 个用户
                  </button>
                </td>
                <td class="row-actions">
                  <button type="button" class="btn btn-sm" @click="tab = 'backups'">备份列表</button>
                  <button type="button" class="btn btn-sm" @click="tab = 'transfer'">导入</button>
                  <button type="button" class="btn btn-sm btn-danger" :disabled="dbBusy" @click="openDropDB(d.name)">
                    删除
                  </button>
                </td>
              </tr>
            </template>
          </tbody>
        </table>

        <div v-else-if="dbs.length" class="db-cards">
          <article v-for="d in pagedDbs" :key="d.name" class="db-card">
            <header class="db-card-head">
              <span class="mono db-name">{{ d.name }}</span>
              <span class="muted small">{{ d.charset }}</span>
            </header>
            <dl class="db-card-meta">
              <div><dt>表数</dt><dd>{{ d.tableCount }}</dd></div>
              <div><dt>大小</dt><dd>{{ formatBytes(d.sizeBytes) }}</dd></div>
            </dl>
            <div class="users-cell">
              <span class="muted small">授权用户</span>
              <button type="button" class="btn-link" @click="openGrants(d)">
                {{ (d.users && d.users.length) || 0 }} 个用户
              </button>
            </div>
            <div class="db-card-actions">
              <button
                type="button"
                class="btn btn-sm"
                :disabled="dbBusy || inst.status !== 'running'"
                @click="openAddUser(d.name)"
              >
                建账号
              </button>
              <button type="button" class="btn btn-sm btn-danger" :disabled="dbBusy" @click="openDropDB(d.name)">
                删除
              </button>
            </div>
          </article>
        </div>

        <p v-else class="muted">这个服务里还没有数据库，点右上角「新建数据库」建第一个。</p>

        <Pager
          v-model:page="dbPage"
          v-model:page-size="dbPageSize"
          :total="dbTotal"
        />
      </section>

      <!-- 授权管理：这个库上有哪些账号、各自什么档位，可就地撤销 -->
      <Modal v-if="grantDB" :title="`授权管理 · ${grantDB.name}`" @close="grantDB = null">
        <div class="row field-head">
          <span class="muted small">下面这些账号能访问 {{ grantDB.name }}</span>
          <div class="grow" />
          <button type="button" class="btn btn-sm btn-primary" @click="grantDB = null; usersOpen = true">
            添加用户授权
          </button>
        </div>
        <p v-if="grantError" class="notice notice-bad">{{ grantError }}</p>
        <p v-else-if="grantLoading" class="muted">读取中…</p>
        <table v-else-if="grantUsers.length" class="data">
          <thead>
            <tr><th>用户</th><th>权限</th><th></th></tr>
          </thead>
          <tbody>
            <tr v-for="u in grantUsers" :key="u.name">
              <td class="mono">{{ u.name }}@{{ (u.hosts && u.hosts[0]) || '%' }}</td>
              <td>{{ grantLevelOf(u) }}</td>
              <td class="row-actions">
                <button type="button" class="btn-link" @click="revokeGrant(u)">撤销授权</button>
              </td>
            </tr>
          </tbody>
        </table>
        <p v-else class="muted">这个库还没有授权任何账号。</p>
      </Modal>

      <ConnDrawer v-if="isServer" v-model="connOpen" :instance-id="instanceId" @changed="load()" />
      <UserDrawer v-if="isServer" v-model="usersOpen" :instance-id="instanceId" @changed="loadDatabases()" />

      <ConfigPanel v-if="tab === 'config'" :instance-id="instanceId" :engine="inst.engine" @changed="load()" />
      <ConfigFilePanel v-if="tab === 'cnf'" :instance-id="instanceId" @changed="load()" />
      <TransferPanel v-if="tab === 'transfer'" :instance-id="instanceId" :engine="inst.engine" @changed="load()" />
      <BackupPanel
        v-if="tab === 'backups'"
        :instance-id="instanceId"
        :instance-name="title"
        :engine="inst?.engine ?? 'sqlite'"
        @changed="load()"
      />

      <div v-if="tab === 'danger'" class="danger">
        <section class="card">
          <h2>服务信息</h2>
          <dl class="kv">
            <dt>{{ isServer ? '服务 ID' : '数据库 ID' }}</dt>
            <dd class="mono">{{ inst?.id ?? '—' }}</dd>
            <dt>{{ isServer ? '数据目录' : '数据文件' }}</dt>
            <dd class="mono">{{ inst?.filePath ?? '—' }}</dd>
            <dt v-if="isServer">端口</dt>
            <dd v-if="isServer" class="mono">{{ inst?.port ?? '—' }}</dd>
            <dt>最后变更</dt>
            <dd>{{ inst ? formatTime(inst.updatedAt) : '—' }}</dd>
          </dl>
          <button type="button" class="btn" @click="openRename">修改名称</button>
        </section>

        <section class="card danger-card">
          <h2>{{ isServer ? '删除服务' : '删除数据库' }}</h2>
          <p class="tip">
            <template v-if="isServer">
              服务删除后，它里面建的所有数据库都会一起消失。选择「保留数据文件」可留下文件以便手工找回，
              不保留则移入回收站目录，卸载应用才会清理。
            </template>
            <template v-else>
              删除后数据库会从列表消失。选择「保留数据文件」会留下数据库文件以便手工找回，
              不保留则移入回收站目录，卸载应用才会清理。
            </template>
          </p>
          <button type="button" class="btn btn-danger" @click="openDelete">
            {{ isServer ? '删除服务' : '删除数据库' }}
          </button>
        </section>
      </div>
    </template>

    <!-- 新建数据库：库名 + 字符集 + 可选授权账号 -->
    <Modal v-if="creatingDB" title="新建数据库" :busy="dbBusy" @close="creatingDB = false">
      <div class="field">
        <label for="db-name">数据库名</label>
        <input
          id="db-name"
          v-model="dbForm.name"
          class="input mono"
          maxlength="64"
          placeholder="例如 blog"
          autocomplete="off"
          @keyup.enter="submitCreateDB"
        />
        <span class="field-hint">字母、数字、下划线，字母或下划线开头；建好后不建议改名。</span>
      </div>

      <div class="field">
        <label for="db-charset">字符集</label>
        <select id="db-charset" v-model="dbForm.charset" class="select" :disabled="dbBusy">
          <option v-for="c in charsets" :key="c.value" :value="c.value">{{ c.label }}</option>
        </select>
        <span class="field-hint">建好后可以改表，但改库的默认字符集不会转换已有数据。</span>
      </div>

      <div class="field">
        <label class="row">
          <input v-model="dbForm.withUser" type="checkbox" :disabled="dbBusy" />
          <span>同时创建一个只对这个库授权的账号</span>
        </label>
      </div>

      <template v-if="dbForm.withUser">
        <div class="field">
          <label for="db-user">账号名</label>
          <input id="db-user" v-model="dbForm.user" class="input mono" maxlength="32" placeholder="例如 blog_app" autocomplete="off" />
        </div>
        <div class="field">
          <label for="db-pwd">密码</label>
          <div class="row">
            <input id="db-pwd" v-model="dbForm.password" class="input mono grow" placeholder="至少 8 位" autocomplete="new-password" />
            <button type="button" class="btn btn-sm" @click="dbForm.password = randomPassword()">随机</button>
            <button type="button" class="btn btn-sm" @click="copy(dbForm.password, '密码')">复制</button>
          </div>
          <span class="field-hint">密码只在这里显示一次，请先复制保存。</span>
        </div>
        <div class="field">
          <label for="db-level">权限</label>
          <select id="db-level" v-model="dbForm.level" class="select" :disabled="dbBusy">
            <option v-for="lv in LEVELS" :key="lv.value" :value="lv.value">{{ lv.label }}</option>
          </select>
          <span class="field-hint">权限只作用在 {{ dbForm.name.trim() || '这个库' }} 上，不会影响同服务的其他库。</span>
        </div>
      </template>

      <div v-if="copyHint" class="notice notice-ok" role="status">{{ copyHint }}</div>
      <div v-if="dbFormError" class="notice notice-bad">{{ dbFormError }}</div>

      <template #foot>
        <button type="button" class="btn" :disabled="dbBusy" @click="creatingDB = false">取消</button>
        <button type="button" class="btn btn-primary" :disabled="dbBusy" @click="submitCreateDB">
          {{ dbBusy ? '创建中…' : '创建数据库' }}
        </button>
      </template>
    </Modal>

    <!-- 删库：和删服务一样要求输入名字 -->
    <Modal v-if="droppingDB" title="删除数据库" :busy="dbBusy" @close="droppingDB = null">
      <p class="tip">
        库 <strong class="mono">{{ droppingDB.name }}</strong> 里的所有表和数据都会被删除，且无法恢复。
      </p>
      <div class="field">
        <label for="db-confirm">输入库名以确认</label>
        <input
          id="db-confirm"
          v-model="droppingDB.confirm"
          class="input mono"
          :placeholder="droppingDB.name"
          autocomplete="off"
          @keyup.enter="submitDropDB"
        />
      </div>
      <div v-if="droppingDB.error" class="notice notice-bad">{{ droppingDB.error }}</div>
      <template #foot>
        <button type="button" class="btn" :disabled="dbBusy" @click="droppingDB = null">取消</button>
        <button
          type="button"
          class="btn btn-danger"
          :disabled="dbBusy || droppingDB.confirm !== droppingDB.name"
          @click="submitDropDB"
        >
          {{ dbBusy ? '删除中…' : '确认删除' }}
        </button>
      </template>
    </Modal>

    <Modal
      v-if="addingUser"
      :title="`给库 ${addingUser.db} 建账号`"
      :busy="dbBusy"
      @close="addingUser = null"
    >
      <p class="tip">
        账号只授 <strong class="mono">{{ addingUser.db }}</strong> 这一个库，同服务的其他库访问不到。
        密码只在创建时显示一次，请先复制保存。
      </p>
      <div class="field">
        <label for="u-name">账号名</label>
        <input id="u-name" v-model="addingUser.name" class="input mono" maxlength="32" placeholder="例如 blog_app" autocomplete="off" />
      </div>
      <div class="field">
        <label for="u-pwd">密码</label>
        <div class="row">
          <input id="u-pwd" v-model="addingUser.password" class="input mono grow" placeholder="至少 8 位" autocomplete="new-password" />
          <button type="button" class="btn btn-sm" @click="addingUser.password = randomPassword()">随机</button>
          <button type="button" class="btn btn-sm" @click="copy(addingUser.password, '密码')">复制</button>
        </div>
      </div>
      <div class="field">
        <label for="u-level">权限</label>
        <select id="u-level" v-model="addingUser.level" class="select" :disabled="dbBusy">
          <option v-for="lv in LEVELS" :key="lv.value" :value="lv.value">{{ lv.label }}</option>
        </select>
        <span class="field-hint">只读只能 SELECT；读写能增删改但不能改表结构；全权含建表、删表。</span>
      </div>
      <div v-if="copyHint" class="notice notice-ok" role="status">{{ copyHint }}</div>
      <div v-if="addingUser.error" class="notice notice-bad">{{ addingUser.error }}</div>
      <template #foot>
        <button type="button" class="btn" :disabled="dbBusy" @click="addingUser = null">取消</button>
        <button type="button" class="btn btn-primary" :disabled="dbBusy" @click="submitAddUser">
          {{ dbBusy ? '创建中…' : '创建账号' }}
        </button>
      </template>
    </Modal>

    <Modal v-if="renaming" :title="isServer ? '修改服务名称' : '修改数据库名称'" :busy="busy" @close="renaming = false">
      <div class="field">
        <label for="n-name">新名称</label>
        <input
          id="n-name"
          v-model="newName"
          class="input"
          maxlength="32"
          @keyup.enter="submitRename"
        />
      </div>
      <div v-if="renameError" class="notice notice-bad">{{ renameError }}</div>
      <template #foot>
        <button type="button" class="btn" :disabled="busy" @click="renaming = false">取消</button>
        <button type="button" class="btn btn-primary" :disabled="busy" @click="submitRename">
          {{ busy ? '保存中…' : '保存' }}
        </button>
      </template>
    </Modal>

    <Modal v-if="deleting" :title="isServer ? '删除服务' : '删除数据库'" :busy="busy" @close="deleting = false">
      <p class="tip">
        <strong>{{ title }}</strong> 将被删除，列表里再也看不到它。
      </p>
      <div class="field">
        <label for="d-confirm">{{ isServer ? '输入服务名以确认' : '输入数据库名以确认' }}</label>
        <input
          id="d-confirm"
          v-model="confirmName"
          class="input"
          :placeholder="title"
          @keyup.enter="submitDelete"
        />
      </div>
      <label class="check">
        <input v-model="keepData" type="checkbox" />
        <span>保留数据（docker 卷与宿主机目录都留着；不勾选则一并清理）</span>
      </label>
      <div v-if="deleteError" class="notice notice-bad">{{ deleteError }}</div>
      <template #foot>
        <button type="button" class="btn" :disabled="busy" @click="deleting = false">取消</button>
        <button
          type="button"
          class="btn btn-danger"
          :disabled="busy || confirmName !== title"
          @click="submitDelete"
        >
          {{ busy ? '删除中…' : '确认删除' }}
        </button>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
/* ---------- 服务头 ---------- */

.hero {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 16px 18px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--bg-elevated);
  box-shadow: var(--shadow);
}

.btn-back {
  flex: none;
  width: 30px;
  height: 30px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: transparent;
  color: var(--fg-muted);
  cursor: pointer;
  font-size: 14px;
  line-height: 1;
}

.btn-back:hover {
  color: var(--fg);
  border-color: var(--accent);
}

.hero-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.hero-title {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.hero-title h1 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  letter-spacing: 0.2px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.hero-meta {
  display: flex;
  gap: 14px;
  flex-wrap: wrap;
  font-size: 13px;
  color: var(--fg-muted);
}

.hero-actions {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.btn-ghost {
  min-width: 34px;
}

/* ---------- 概览布局：左连接右资源，服务信息通栏 ---------- */

.ov {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  align-items: start;
}

.ov-wide {
  grid-column: 1 / -1;
}

@media (max-width: 900px) {
  .ov {
    grid-template-columns: 1fr;
  }
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 10px;
}

.card-head h2 {
  margin: 0;
  font-size: 14px;
}

.res-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
  gap: 10px;
}

.res-foot {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-top: 12px;
  flex-wrap: wrap;
}

.kv-grid {
  display: grid;
  grid-template-columns: 88px 1fr 88px 1fr;
  column-gap: 14px;
}

@media (max-width: 760px) {
  .kv-grid {
    grid-template-columns: 88px 1fr;
  }
}

.ellipsis {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.bad-text {
  color: var(--bad);
}

.page {
  min-width: 0;
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 1400px;
  margin: 0 auto;
}

/* ---------- 库列表：授权用户与卡片视图 ---------- */

.users-cell {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  align-items: center;
}

/* 展开行是"这个库有哪些账号"，压暗背景与数据行区分开 */
.expand-row > td {
  background: var(--bg);
  border-top: 1px dashed var(--line);
  line-height: 2.2;
}

.expand-row .pill {
  margin: 0 4px 0 0;
}

.btn-link {
  border: 0;
  background: none;
  padding: 0 2px;
  color: var(--accent);
  font-size: 13.5px;
  cursor: pointer;
}

.btn-link:hover {
  text-decoration: underline;
}

.db-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(248px, 1fr));
  gap: 10px;
}

.db-card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px 14px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
}

.db-card-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}

.db-card-head .db-name {
  font-size: 14px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
}

.db-card-meta {
  display: flex;
  gap: 16px;
  margin: 0;
}

.db-card-meta > div {
  display: flex;
  align-items: baseline;
  gap: 5px;
}

.db-card-meta dt {
  color: var(--fg-muted);
  font-size: 12.5px;
}

.db-card-meta dd {
  margin: 0;
  font-size: 13.5px;
}

.db-card-actions {
  display: flex;
  gap: 6px;
  margin-top: auto;
  padding-top: 8px;
  border-top: 1px solid var(--line);
}

.head {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  flex-wrap: wrap;
}

.head h2 {
  margin: 0;
  font-size: 16px;
}

.meta {
  margin-top: 4px;
  font-size: 13px;
}

.tabs {
  display: flex;
  gap: 2px;
  padding: 3px;
  border: 1px solid var(--line);
  border-radius: 9px;
  background: var(--bg-elevated);
  align-self: flex-start;
  flex-wrap: wrap;
}

.tab {
  padding: 6px 12px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--fg-muted);
  font-size: 13px;
}

.tab:hover {
  color: var(--fg);
}

.tab.active {
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}

.tab.danger.active {
  background: var(--bad-bg);
  color: var(--bad);
}

.danger {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.danger-card {
  border-color: var(--bad);
}

.kv {
  display: grid;
  grid-template-columns: 88px minmax(0, 1fr);
  gap: 8px 12px;
  margin: 0 0 14px;
}

.kv dt {
  color: var(--fg-muted);
  font-size: 13px;
}

.kv dd {
  margin: 0;
  font-size: 13.5px;
  word-break: break-all;
}

/* 卡片标题行：标题靠左、操作按钮靠右 */
.field-head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.field-head h2 {
  margin: 0;
}

.row-actions {
  display: flex;
  gap: 6px;
  justify-content: flex-end;
}

/* 配额编辑：一行放下两个下拉 + 保存 */
.quota {
  display: flex;
  align-items: flex-end;
  gap: 10px;
  flex-wrap: wrap;
  padding: 10px 0 0;
  border-top: 1px dashed var(--line);
}

.quota .field {
  margin: 0;
}

.tip {
  margin: 0 0 12px;
  font-size: 13.5px;
  line-height: 1.6;
}

.check {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
}

.conn h2 {
  margin-bottom: 10px;
}

.conn .kv {
  margin-bottom: 8px;
}

.pwd {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.conn input[readonly] {
  min-width: 0;
}

@media (max-width: 720px) {
  .kv { grid-template-columns: 68px minmax(0, 1fr); }
  .tab { min-height: 38px; }
  .head h2 { overflow-wrap: anywhere; }
  .conn .row .input { flex-basis: 100%; }
}
</style>
