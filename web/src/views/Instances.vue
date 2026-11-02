<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api, type Instance } from '../api'
import { errText, formatBytes, formatTime } from '../format'
import InstanceDetail from './InstanceDetail.vue'
import Modal from '../components/Modal.vue'
import ViewSwitch from '../components/ViewSwitch.vue'
import Pager from '../components/Pager.vue'
import { usePager } from '../pager'
import { isCardView } from '../view-mode'
import EngineTabs from '../components/EngineTabs.vue'
import type { Engine } from '../components/EngineTabs.vue'

// 账号并进了「数据库与账号」页签，这里不再有单独的 users
type TabKey = 'overview' | 'databases' | 'config' | 'backups' | 'transfer' | 'danger'

const props = defineProps<{ notice?: { id: string; name: string; port: number } | null }>()
const emit = defineEmits<{ navigate: [view: 'jobs' | 'deploy' | 'images', version?: string] }>()

// 引擎页签：只有 MySQL 是通的，另外两个先占位，让"以后会支持什么"有落点
const engine = ref<Engine>('mysql')

const WIP: Record<string, { title: string; intro: string; plans: string[] }> = {
  postgresql: {
    title: 'PostgreSQL 还在做',
    intro: '会复用同一套部署、备份、导入导出与账号管理流程，底层换成 psql / pg_dump。',
    plans: ['部署与版本管理（含 ARM 架构的官方镜像）', '库表浏览与 SQL 查询', '账号与库级授权', '逻辑备份恢复与 CSV 导入导出'],
  },
  mongodb: {
    title: 'MongoDB 还在做',
    intro: 'MongoDB 不是关系型数据库，账号、备份、导入导出都会单独做一套，不会硬套 MySQL 的模型。',
    plans: ['单机与副本集部署', '集合浏览与文档查询', '基于角色的用户管理', 'mongodump / mongorestore 备份恢复'],
  },
}

const wip = computed(() => WIP[engine.value] ?? WIP.postgresql)

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

// 服务列表分页：NAS 上装了十几个服务之后，一屏铺下来就找不到重点了
const {
  page: pageNo,
  pageSize,
  total: pageTotal,
  items: pagedItems,
} = usePager(items, 10)
const loading = ref(false)
const error = ref('')
const keyword = ref('')

const selectedID = ref('')
const detailTab = ref<TabKey>('overview')

const toggling = ref('')
const justDeployed = computed(() => props.notice ?? null)

function statusClass(status: string): string {
  if (status === 'running') return 'pill-ok'
  if (status === 'error' || status === 'failed') return 'pill-bad'
  return 'pill-warn'
}

// 创建中/部署失败的实例没有可用服务：点进详情只会看到一堆取不到数据的报错，
// 这类行改成展示原因 + 日志入口，别让用户白点。
function isUnusable(it: Instance): boolean {
  return it.status === 'creating' || it.status === 'failed'
}

const running = computed(() => items.value.filter((it) => it.status === 'running'))
const totalSize = computed(() => items.value.reduce((sum, it) => sum + (it.sizeBytes || 0), 0))
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
  emit('navigate', 'deploy', version)
}

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
    @back="selectedID = ''; load()"
    @changed="load()"
  />

  <div v-else class="page">
    <EngineTabs v-model="engine" />

    <template v-if="engine === 'mysql'">
      <header class="head">
      <div>
        <h2>数据库服务</h2>
        <p class="muted">
          一个服务就是一个 MySQL 容器：独立端口、独立数据。点进去才能建数据库、发账号。
        </p>
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
        <button type="button" class="btn btn-primary" @click="openDeploy()">部署服务</button>
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
            <span class="sub">MySQL {{ it.engineVersion }} · {{ PROFILE_TEXT[it.profile] ?? it.profile }}</span>
          </div>
          <span class="pill" :class="statusClass(it.status)" :title="it.statusReason || ''">
            {{ STATUS_TEXT[it.status] ?? it.status }}
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
              <span v-if="it.sizeUnknown" class="muted" title="数据在 docker 卷里，宿主读不到体积">卷内</span>
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
            <button type="button" class="btn btn-sm" @click="askRemove(it)">删除</button>
          </template>
          <template v-else>
            <button type="button" class="btn btn-sm" @click="openDetail(it.id)">管理</button>
            <button type="button" class="btn btn-sm" @click="openDetail(it.id, 'databases')">库与账号</button>
            <button type="button" class="btn btn-sm" @click="openDetail(it.id, 'config')">配置</button>
            <button type="button" class="btn btn-sm" :disabled="toggling === it.id" @click="toggleRun(it)">
              {{ toggling === it.id ? '…' : it.status === 'running' ? '停止' : '启动' }}
            </button>
            <button type="button" class="btn btn-sm" @click="askRemove(it)">删除</button>
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
              <div class="sub">{{ PROFILE_TEXT[it.profile] ?? it.profile }}</div>
            </td>
            <td class="mono">{{ it.engineVersion }}</td>
            <td>
              <span class="pill" :class="statusClass(it.status)" :title="it.statusReason || ''">
                {{ STATUS_TEXT[it.status] ?? it.status }}
              </span>
              <div v-if="it.status === 'failed' && it.statusReason" class="sub reason" :title="it.statusReason">
                {{ it.statusReason }}
              </div>
            </td>
            <td class="mono">{{ it.port || '—' }}</td>
            <td>{{ it.tableCount ?? '—' }}</td>
            <td>
              <span v-if="it.sizeUnknown" class="muted" title="数据在 docker 卷里，宿主读不到体积">卷内</span>
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
                <button type="button" class="btn btn-sm" @click="askRemove(it)">删除</button>
              </template>
              <template v-else>
                <button type="button" class="btn btn-sm" @click="openDetail(it.id)">管理</button>
                <button type="button" class="btn btn-sm" @click="openDetail(it.id, 'databases')">库与账号</button>
                <button type="button" class="btn btn-sm" @click="openDetail(it.id, 'config')">配置</button>
                <button type="button" class="btn btn-sm" :disabled="toggling === it.id" @click="toggleRun(it)">
                  {{ toggling === it.id ? '…' : it.status === 'running' ? '停止' : '启动' }}
                </button>
                <button type="button" class="btn btn-sm" @click="askRemove(it)">删除</button>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Pager v-model:page="pageNo" v-model:page-size="pageSize" :total="pageTotal" />

    <p v-if="!items.length && !loading" class="card empty">
      还没有服务。点右上角「部署服务」建第一个 MySQL。
    </p>

    <!-- 删除确认：容器一定会被删，数据是否一起清掉由用户决定 -->
    <Modal v-if="removing" :title="`删除服务 · ${removing.inst.name}`" :busy="removing.busy" @close="removing = null">
      <p class="tip">
        容器会被删除，端口随之释放。数据默认保留，之后仍可在宿主机上找到。
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
      <label class="check">
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
    </template>

    <section v-else class="wip-panel">
      <div class="wip-head">
        <h3>{{ wip.title }}</h3>
        <span class="pill pill-warn">开发中</span>
      </div>
      <p class="muted">{{ wip.intro }}</p>
      <ul class="wip-list">
        <li v-for="p in wip.plans" :key="p">{{ p }}</li>
      </ul>
      <p class="muted small">现在先用 MySQL：部署、备份、导入导出、账号管理都是通的。</p>
    </section>
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

/* 引擎占位：PG / MongoDB 还没做，把计划写清楚，不要只留一片空白 */
.wip-panel {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 20px 22px;
  border: 1px dashed var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
}

.wip-head {
  display: flex;
  align-items: center;
  gap: 10px;
}

.wip-head h3 {
  margin: 0;
  font-size: 16px;
}

.wip-panel p {
  margin: 0;
  line-height: 1.7;
}

.wip-list {
  margin: 0;
  padding-left: 20px;
  color: var(--fg-muted);
  font-size: 13.5px;
  line-height: 1.9;
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
