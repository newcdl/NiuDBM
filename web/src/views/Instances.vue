<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api, engineLabel, type Instance } from '../api'
import { errText, formatBytes, formatTime } from '../format'
import InstanceDetail from './InstanceDetail.vue'
import Modal from '../components/Modal.vue'
import ViewSwitch from '../components/ViewSwitch.vue'
import Pager from '../components/Pager.vue'
import { usePager } from '../pager'
import { isCardView } from '../view-mode'
import EngineTabs from '../components/EngineTabs.vue'
import ConnectDatabase from '../components/ConnectDatabase.vue'
import { tabEngine, type Engine } from '../engine'
import { useAuth } from '../auth'

// 账号并进了「数据库与账号」页签，这里不再有单独的 users
type TabKey = 'overview' | 'databases' | 'config' | 'cnf' | 'backups' | 'transfer' | 'members' | 'danger'

const props = defineProps<{
  notice?: { id: string; name: string; port: number } | null
  // 部署完成后要落到对应引擎的页签：否则刚建好的 PG 服务会被"只显示 MySQL"
  // 的过滤挡在外面，看起来就像什么都没发生
  initialEngine?: Engine
  initialId?: string
  initialTab?: string
}>()
const emit = defineEmits<{
  navigate: [view: 'jobs' | 'deploy' | 'images', version?: string, engine?: string]
  open: [id: string, tab?: string]
  'tab-change': [id: string, tab: string]
  back: []
}>()

// 引擎页签：三个引擎都走统一的数据源模型。
const engine = ref<Engine>(props.initialEngine ?? 'mysql')
const engineKey = computed(() => tabEngine(engine.value))

const PROFILE_TEXT: Record<string, string> = {
  low: '轻量',
  standard: '标准',
  performance: '性能',
}

const STATUS_TEXT: Record<string, string> = {
  running: '运行中',
  // 部署是异步的：创建中就落台账，用户回列表能立刻看到这一行
  creating: '创建中',
  // 部署失败不再清场，失败记录留在列表里，可看日志、可删掉重来
  failed: '部署失败',
  stopped: '已停止',
  error: '异常',
  restoring: '恢复中',
}

const items = ref<Instance[]>([])

// 按引擎过滤：接口一次返回全部服务，页签只决定"现在看哪种"
const shown = computed(() => items.value.filter((it) => (it.engine || 'mysql') === engineKey.value))

// 服务列表分页：NAS 上装了十几个服务之后，一屏铺下来就找不到重点了
const {
  page: pageNo,
  pageSize,
  total: pageTotal,
  items: pagedItems,
} = usePager(shown, 10)
const loading = ref(false)
const error = ref('')
const keyword = ref('')
const connectOpen = ref(false)
const { state: authState } = useAuth()
const canCreate = computed(() => authState.value?.user?.isAdmin === true)

const selectedID = ref(props.initialId ?? '')
const detailTab = ref<TabKey>((props.initialTab as TabKey) || 'overview')

const toggling = ref('')
const justDeployed = computed(() => props.notice ?? null)

function statusClass(status: string): string {
  if (status === 'running') return 'pill-ok'
  if (status === 'error' || status === 'failed') return 'pill-bad'
  return 'pill-warn'
}

function displayStatus(it: Instance): string {
  if (it.mode === 'external') {
    if (it.status === 'running') return '已连接'
    if (it.status === 'error') return '连接失败'
    return '待连接'
  }
  return STATUS_TEXT[it.status] ?? it.status
}

function statusClassOf(it: Instance): string {
  if (it.mode === 'external') return it.status === 'running' ? 'pill-ok' : 'pill-bad'
  return statusClass(it.status)
}

function canOperate(it: Instance): boolean {
  return it.accessRole === 'operator' || it.accessRole === 'admin'
}

function canAdmin(it: Instance): boolean {
  return it.accessRole === 'admin'
}

// 创建中/部署失败的实例没有可用服务：点进详情只会看到一堆取不到数据的报错，
// 这类行改成展示原因 + 日志入口，别让用户白点。
function isUnusable(it: Instance): boolean {
  return it.status === 'creating' || it.status === 'failed'
}

const running = computed(() => shown.value.filter((it) => it.status === 'running'))
const totalSize = computed(() => shown.value.reduce((sum, it) => sum + (it.sizeBytes || 0), 0))
const runningPorts = computed(() => running.value.map((it) => it.port).filter((p): p is number => !!p))

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const data = await api.listInstances(keyword.value.trim())
    items.value = data.items
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
  // 有创建中的行就自己轮询，部署完成或失败后列表自动变过来
  void pollCreating()
}

/* ---------------- 部署现场（创建中 / 部署失败） ---------------- */

// 失败现场由后端固化在实例目录里：这是用户唯一能看到"为什么没起来"的地方，
// 之前容器被删干净，只剩一句"没就绪"，根本没法排查。
const deployLog = ref<{
  inst: Instance
  loading: boolean
  error: string
  text: string
  path: string
  available: boolean
} | null>(null)

async function openDeployLog(it: Instance): Promise<void> {
  deployLog.value = { inst: it, loading: true, error: '', text: '', path: '', available: false }
  try {
    const d = await api.deployLog(it.id)
    if (!deployLog.value) return
    deployLog.value.text = d.log
    deployLog.value.path = d.path
    deployLog.value.available = d.available
    if (!d.available) deployLog.value.error = d.message ?? '还没有部署日志'
  } catch (e) {
    if (deployLog.value) deployLog.value.error = errText(e)
  } finally {
    if (deployLog.value) deployLog.value.loading = false
  }
}

// 从日志弹窗直接去删：失败的实例只有删掉才能用同名重新部署
function deleteFromLog(): void {
  const inst = deployLog.value?.inst
  deployLog.value = null
  if (inst) askRemove(inst)
}

let creatingTimer: number | undefined

async function pollCreating(): Promise<void> {
  if (creatingTimer !== undefined) window.clearTimeout(creatingTimer)
  creatingTimer = undefined
  if (!items.value.some((it) => it.status === 'creating')) return
  creatingTimer = window.setTimeout(() => {
    void (async () => {
      await load()
      await pollCreating()
    })()
  }, 3000)
}

/* ---------------- 删除（二次确认 + 是否删数据） ---------------- */

const removing = ref<{
  inst: Instance
  name: string
  purge: boolean
  busy: boolean
  error: string
} | null>(null)

function askRemove(it: Instance): void {
  removing.value = { inst: it, name: '', purge: false, busy: false, error: '' }
}

async function confirmRemove(): Promise<void> {
  const job = removing.value
  if (!job || job.busy) return
  job.busy = true
  job.error = ''
  try {
    await api.deleteInstance(job.inst.id, job.name.trim(), !job.purge)
    removing.value = null
    await load()
  } catch (e) {
    job.error = errText(e)
  } finally {
    if (removing.value === job) job.busy = false
  }
}

/* ---------------- 行操作 ---------------- */

function openDetail(id: string, tab: TabKey = 'overview'): void {
  selectedID.value = id
  detailTab.value = tab
  emit('open', id, tab)
}

function onDetailTab(tab: TabKey): void {
  detailTab.value = tab
  if (selectedID.value) emit('tab-change', selectedID.value, tab)
}

async function toggleRun(it: Instance): Promise<void> {
  if (toggling.value) return
  toggling.value = it.id
  try {
    if (it.status === 'running') {
      await api.stopInstance(it.id)
    } else {
      await api.startInstance(it.id)
    }
    await load()
  } catch (e) {
    error.value = errText(e)
  } finally {
    toggling.value = ''
  }
}

function openDeploy(version?: string): void {
  emit('navigate', 'deploy', version, engineKey.value)
}

function onConnected(instance: Instance): void {
  connectOpen.value = false
  void load().then(() => openDetail(instance.id))
}

// 再进页面时跟随外部指定（从部署页回来、从概览跳转），避免停在旧页签
watch(
  () => props.initialEngine,
  (next) => {
    if (next) engine.value = next
  },
)

watch(
  () => [props.initialId, props.initialTab] as const,
  ([id, tab]) => {
    selectedID.value = id ?? ''
    if (tab) detailTab.value = tab as TabKey
  },
)

onMounted(load)
onBeforeUnmount(() => {
  if (creatingTimer) window.clearTimeout(creatingTimer)
})
</script>

<template>
  <InstanceDetail
    v-if="selectedID"
    :instance-id="selectedID"
    :initial-tab="detailTab"
    @back="selectedID = ''; load(); emit('back')"
    @route="onDetailTab"
    @changed="load()"
  />

  <div v-else class="page">
    <EngineTabs v-model="engine" />

      <header class="head">
      <div>
        <h2>数据库服务</h2>
        <p class="muted">管理本应用创建的 {{ engineLabel(engineKey) }} 服务，也可以接入已有数据库。</p>
      </div>
      <div class="row">
        <input
          v-model="keyword"
          class="input search"
          type="search"
          placeholder="搜索服务名"
          @keyup.enter="load()"
        />
        <ViewSwitch />
        <button type="button" class="btn" :disabled="loading" @click="load()">
          {{ loading ? '读取中' : '刷新' }}
        </button>
        <button v-if="canCreate" type="button" class="btn" @click="connectOpen = true">连接已有</button>
        <button v-if="canCreate" type="button" class="btn btn-primary" @click="openDeploy()">创建托管服务</button>
      </div>
    </header>

    <div v-if="error" class="notice notice-bad">
      <span>{{ error }}</span>
      <button type="button" class="btn btn-sm" @click="load()">重试</button>
    </div>

    <section class="facts">
      <span>服务 <b>{{ items.length }}</b> 个</span>
      <span>运行中 <b>{{ running.length }}</b></span>
      <span>共占用 <b>{{ formatBytes(totalSize) }}</b></span>
      <span v-if="runningPorts.length">端口 {{ runningPorts.join('、') }}</span>
    </section>

    <!-- 卡片：一眼看清状态、端口、占用，操作就在卡片里；表格留给习惯横向对比的人 -->
    <section v-if="isCardView" class="cards">
      <article
        v-for="it in pagedItems"
        :key="it.id"
        class="svc"
        :class="{ fresh: justDeployed?.id === it.id, broken: isUnusable(it) }"
      >
        <header class="svc-head">
          <div class="svc-title">
            <button v-if="!isUnusable(it)" type="button" class="link name" @click="openDetail(it.id)">
              {{ it.name }}
            </button>
            <span v-else class="name strong">{{ it.name }}</span>
            <span class="sub">
              {{ engineLabel(it.engine) }} {{ it.engineVersion }} ·
              {{ it.mode === 'external' ? '外部连接' : (PROFILE_TEXT[it.profile] ?? it.profile) }}
              <template v-if="it.mode === 'external'"> · {{ it.host }}:{{ it.port }}</template>
            </span>
          </div>
          <span class="pill" :class="statusClassOf(it)" :title="it.statusReason || ''">
            {{ displayStatus(it) }}
          </span>
        </header>

        <p v-if="it.status === 'failed' && it.statusReason" class="sub reason" :title="it.statusReason">
          {{ it.statusReason }}
        </p>

        <dl class="svc-meta">
          <div>
            <dt>端口</dt>
            <dd class="mono">{{ it.port || '—' }}</dd>
          </div>
          <div>
            <dt>表数</dt>
            <dd>{{ it.tableCount ?? '—' }}</dd>
          </div>
          <div>
            <dt>体积</dt>
            <dd>
              <span v-if="it.mode === 'external'" class="muted" title="外部数据库的容量由数据库自身统计">外部库</span>
              <span v-else-if="it.sizeUnknown" class="muted" title="数据在 docker 卷里，宿主读不到体积">卷内</span>
              <span v-else>{{ formatBytes(it.sizeBytes || 0) }}</span>
            </dd>
          </div>
          <div>
            <dt>备份</dt>
            <dd>
              <span v-if="it.backupCount" :title="it.lastBackupAt ? formatTime(it.lastBackupAt) : ''">
                {{ it.backupCount }} 份
              </span>
              <span v-else class="muted">—</span>
            </dd>
          </div>
        </dl>

        <div class="svc-actions">
          <!-- 创建中/部署失败：没有可用服务，只给日志与清理两个动作 -->
          <template v-if="it.status === 'creating'">
            <span class="muted small">部署中，稍候…</span>
          </template>
          <template v-else-if="it.status === 'failed'">
            <button type="button" class="btn btn-sm" @click="openDeployLog(it)">部署日志</button>
            <button v-if="canAdmin(it)" type="button" class="btn btn-sm" @click="askRemove(it)">删除</button>
          </template>
          <template v-else>
            <button type="button" class="btn btn-sm" @click="openDetail(it.id)">管理</button>
            <button type="button" class="btn btn-sm" @click="openDetail(it.id, 'databases')">库与账号</button>
            <button v-if="it.capabilities.config && canOperate(it)" type="button" class="btn btn-sm" @click="openDetail(it.id, 'config')">配置</button>
            <button v-if="it.capabilities.startStop && canOperate(it)" type="button" class="btn btn-sm" :disabled="toggling === it.id" @click="toggleRun(it)">
              {{ toggling === it.id ? '…' : it.status === 'running' ? '停止' : '启动' }}
            </button>
            <button v-if="canAdmin(it)" type="button" class="btn btn-sm" @click="askRemove(it)">删除</button>
          </template>
        </div>
      </article>
    </section>
    <div v-if="!isCardView" class="table-wrap">
      <table class="data">
        <thead>
          <tr>
            <th>服务</th>
            <th>版本</th>
            <th>状态</th>
            <th>端口</th>
            <th>表数</th>
            <th>体积</th>
            <th>备份</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="it in pagedItems" :key="it.id" :class="{ fresh: justDeployed?.id === it.id }">
            <td>
              <button v-if="!isUnusable(it)" type="button" class="link" @click="openDetail(it.id)">
                {{ it.name }}
              </button>
              <span v-else class="strong">{{ it.name }}</span>
              <div class="sub">{{ it.mode === 'external' ? `外部连接 · ${it.host}:${it.port}` : (PROFILE_TEXT[it.profile] ?? it.profile) }}</div>
            </td>
            <td class="mono">{{ it.engineVersion }}</td>
            <td>
              <span class="pill" :class="statusClassOf(it)" :title="it.statusReason || ''">
                {{ displayStatus(it) }}
              </span>
              <div v-if="it.status === 'failed' && it.statusReason" class="sub reason" :title="it.statusReason">
                {{ it.statusReason }}
              </div>
            </td>
            <td class="mono">{{ it.port || '—' }}</td>
            <td>{{ it.tableCount ?? '—' }}</td>
            <td>
              <span v-if="it.mode === 'external'" class="muted">外部库</span>
              <span v-else-if="it.sizeUnknown" class="muted" title="数据在 docker 卷里，宿主读不到体积">卷内</span>
              <span v-else>{{ formatBytes(it.sizeBytes || 0) }}</span>
            </td>
            <td>
              <span v-if="it.backupCount" :title="it.lastBackupAt ? formatTime(it.lastBackupAt) : ''">
                {{ it.backupCount }} 份
              </span>
              <span v-else class="muted">—</span>
            </td>
            <td class="row-actions">
              <template v-if="it.status === 'creating'">
                <span class="muted small">部署中…</span>
              </template>
              <template v-else-if="it.status === 'failed'">
                <button type="button" class="btn btn-sm" @click="openDeployLog(it)">部署日志</button>
                <button v-if="canAdmin(it)" type="button" class="btn btn-sm" @click="askRemove(it)">删除</button>
              </template>
              <template v-else>
                <button type="button" class="btn btn-sm" @click="openDetail(it.id)">管理</button>
                <button type="button" class="btn btn-sm" @click="openDetail(it.id, 'databases')">库与账号</button>
                <button v-if="it.capabilities.config && canOperate(it)" type="button" class="btn btn-sm" @click="openDetail(it.id, 'config')">配置</button>
                <button v-if="it.capabilities.startStop && canOperate(it)" type="button" class="btn btn-sm" :disabled="toggling === it.id" @click="toggleRun(it)">
                  {{ toggling === it.id ? '…' : it.status === 'running' ? '停止' : '启动' }}
                </button>
                <button v-if="canAdmin(it)" type="button" class="btn btn-sm" @click="askRemove(it)">删除</button>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Pager v-model:page="pageNo" v-model:page-size="pageSize" :total="pageTotal" />

    <p v-if="!items.length && !loading" class="card empty">
      {{
        canCreate
          ? `还没有数据库服务。可以连接已有 ${engineLabel(engineKey)}，或创建一个由本应用托管的服务。`
          : '还没有被授权访问的数据库服务，请联系 NAS 管理员添加成员权限。'
      }}
    </p>

    <ConnectDatabase
      v-if="connectOpen"
      :initial-engine="engine"
      @close="connectOpen = false"
      @connected="onConnected"
    />

    <!-- 删除确认：容器一定会被删，数据是否一起清掉由用户决定 -->
    <Modal v-if="removing" :title="`删除服务 · ${removing.inst.name}`" :busy="removing.busy" @close="removing = null">
      <p class="tip">
        <template v-if="removing.inst.mode === 'external'">
          只会移除本应用中的连接记录与保存的凭据，不会删除或修改外部数据库。
        </template>
        <template v-else>
          容器会被删除，端口随之释放。数据默认保留，之后仍可在宿主机上找到。
        </template>
      </p>
      <dl class="facts-list">
        <div>
          <dt>数据位置</dt>
          <dd class="mono">{{ removing.inst.filePath }}</dd>
        </div>
        <div>
          <dt>占用</dt>
          <dd>{{ removing.inst.sizeUnknown ? '未知（docker 卷）' : formatBytes(removing.inst.sizeBytes || 0) }}</dd>
        </div>
      </dl>
      <label v-if="removing.inst.mode !== 'external'" class="check">
        <input v-model="removing.purge" type="checkbox" />
        <span>同时删除数据（不可恢复；docker 卷会被移除，应用自己管理的目录会移入回收站）</span>
      </label>
      <div class="field">
        <label for="d-remove">输入服务名以确认</label>
        <input
          id="d-remove"
          v-model="removing.name"
          class="input"
          :placeholder="removing.inst.name"
          @keyup.enter="confirmRemove"
        />
      </div>
      <div v-if="removing.error" class="notice notice-bad">{{ removing.error }}</div>
      <template #foot>
        <button type="button" class="btn" :disabled="removing.busy" @click="removing = null">取消</button>
        <button
          type="button"
          class="btn btn-danger"
          :disabled="removing.busy || removing.name.trim() !== removing.inst.name"
          @click="confirmRemove"
        >
          {{ removing.busy ? '删除中…' : '确认删除' }}
        </button>
      </template>
    </Modal>

    <!-- 部署现场：失败时容器日志被固化到实例目录，这里是唯一能看到原因的地方 -->
    <Modal v-if="deployLog" :title="`部署日志 · ${deployLog.inst.name}`" @close="deployLog = null">
      <p class="tip">
        {{
          deployLog.inst.status === 'failed'
            ? '部署没有成功。下面这段是失败当时的容器日志，可以用来判断是权限、端口，还是初始化太慢。'
            : '这个服务的部署日志。'
        }}
      </p>
      <div v-if="deployLog.path" class="muted small log-path">{{ deployLog.path }}</div>
      <div v-if="deployLog.error" class="notice notice-bad">{{ deployLog.error }}</div>
      <pre v-if="deployLog.text" class="log">{{ deployLog.text }}</pre>
      <p v-else-if="deployLog.loading" class="muted small">读取中…</p>
      <template #foot>
        <button type="button" class="btn" @click="deployLog = null">关闭</button>
        <button v-if="deployLog.inst.status === 'failed'" type="button" class="btn" @click="deleteFromLog">
          删除后重来
        </button>
      </template>
    </Modal>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.head h2 {
  margin: 0 0 4px;
  font-size: 17px;
}

.head p {
  margin: 0;
  font-size: 13px;
}

.search {
  width: 180px;
}

.facts {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 18px;
  color: var(--fg-muted);
  font-size: 13.5px;
}

.facts b {
  color: var(--fg);
  font-weight: 600;
}

.panel {
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
  overflow: hidden;
}

/* ---------- 服务卡片 ---------- */

.cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(272px, 1fr));
  gap: 12px;
}

.svc {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 13px 15px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
  box-shadow: var(--shadow);
}

/* 刚部署完的那张卡高亮一下，从向导回来能立刻找到 */
.svc.fresh {
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent);
}

/* 创建中/部署失败：卡片整体降饱和，不抢运行中服务的注意力 */
.svc.broken {
  border-style: dashed;
}

.svc-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
}

.svc-title {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.svc-title .name {
  font-size: 15.5px;
  font-weight: 600;
  text-align: left;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.svc-meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 6px 12px;
  margin: 0;
}

.svc-meta > div {
  display: flex;
  align-items: baseline;
  gap: 7px;
  min-width: 0;
}

.svc-meta dt {
  color: var(--fg-muted);
  font-size: 13px;
  white-space: nowrap;
}

.svc-meta dd {
  margin: 0;
  font-size: 14.5px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.svc-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: auto;
  padding-top: 10px;
  border-top: 1px solid var(--line);
}

.table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13.5px;
}

.table th,
.table td {
  padding: 9px 12px;
  text-align: left;
  border-bottom: 1px solid var(--line);
  vertical-align: middle;
}

.table th {
  color: var(--fg-muted);
  font-weight: 500;
  font-size: 12.5px;
  background: var(--bg);
}

.table tbody tr:last-child td {
  border-bottom: none;
}

.table tbody tr:hover {
  background: var(--bg-hover, rgba(0, 0, 0, 0.02));
}

.fresh {
  background: rgba(64, 158, 255, 0.08);
}

.link {
  padding: 0;
  border: none;
  background: none;
  color: var(--accent);
  font: inherit;
  font-weight: 600;
  cursor: pointer;
}

.link:hover {
  text-decoration: underline;
}

.strong {
  font-weight: 600;
}

.sub {
  color: var(--fg-muted);
  font-size: 13px;
  font-weight: 400;
}

/* 部署失败的原因放在名字下面：一行看不到头就省略，完整内容在 title 里 */
.reason {
  max-width: 340px;
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.ops {
  white-space: nowrap;
}

.ops .btn + .btn {
  margin-left: 6px;
}

.ops-head {
  text-align: right;
}

.empty {
  padding: 28px 12px;
  text-align: center;
  color: var(--fg-muted);
}

.log-path {
  margin-bottom: 8px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  word-break: break-all;
}

.log {
  max-height: 46vh;
  margin: 0;
  padding: 10px 12px;
  overflow: auto;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg);
  color: var(--fg);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12.5px;
  line-height: 1.55;
  white-space: pre-wrap;
  word-break: break-all;
}

.tip {
  margin: 0 0 10px;
  font-size: 13px;
  color: var(--fg-muted);
}

.facts-list {
  margin: 0 0 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.facts-list div {
  display: flex;
  gap: 10px;
  font-size: 13px;
}

.facts-list dt {
  min-width: 64px;
  color: var(--fg-muted);
}

.facts-list dd {
  margin: 0;
  word-break: break-all;
}

.check {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-bottom: 12px;
  font-size: 13px;
  color: var(--fg);
}

.check input {
  margin-top: 2px;
}

/* 手机：标题与操作各自一行，搜索框吃掉剩余宽度，服务卡片单列 */
@media (max-width: 720px) {
  .head {
    gap: 10px;
  }

  .head .row {
    width: 100%;
  }

  .search {
    flex: 1 1 140px;
    width: auto;
    min-width: 0;
  }

  .facts {
    gap: 6px 14px;
  }

  .cards {
    grid-template-columns: minmax(0, 1fr);
    gap: 8px;
  }
}
</style>
