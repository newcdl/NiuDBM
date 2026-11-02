<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, type BackupRecord } from '../api'
import { errText, formatBytes, formatTime, relativeTime } from '../format'
import ViewSwitch from './ViewSwitch.vue'
import { isCardView } from '../view-mode'
import Pager from './Pager.vue'
import { usePager } from '../pager'
import BackupPolicyPanel from './BackupPolicyPanel.vue'
import Modal from './Modal.vue'

const props = defineProps<{ instanceId: string; instanceName: string; engine?: string }>()
const emit = defineEmits<{ changed: [] }>()

// 备份永远是逻辑导出：容器里跑 mariadb-dump/mysqldump，实例保持运行


const items = ref<BackupRecord[]>([])

// 备份会越攒越多，按页看；删除/恢复只影响当前页的这一条
const { page: pageNo, pageSize, total: pageTotal, items: pagedRecs } = usePager(items, 10)
const backupRoot = ref('')
const loading = ref(false)
const error = ref('')
const notice = ref('')

const note = ref('')
const busy = ref(false)

// 备份按库走：一个服务里几个库各自独立，整实例导出的 SQL 又大又难挑出一份来恢复
const databases = ref<{ name: string }[]>([])
const targetDB = ref('')
const dbError = ref('')

async function loadDatabases(): Promise<void> {
  try {
    const data = await api.listDatabases(props.instanceId)
    databases.value = data.items.map((d) => ({ name: d.name }))
    // 只有一个库时直接选中它；多个库时保留上次选的，没了就回退到第一个
    if (!targetDB.value || !databases.value.some((d) => d.name === targetDB.value)) {
      targetDB.value = databases.value[0]?.name ?? ''
    }
    dbError.value = ''
  } catch (e) {
    // 服务没跑时读不到库列表：备份按钮禁用，这里只留一句原因
    databases.value = []
    dbError.value = errText(e)
  }
}

const restoring = ref<BackupRecord | null>(null)
const confirmName = ref('')
const restoreError = ref('')

const deleting = ref<BackupRecord | null>(null)

const TYPE_TEXT: Record<string, string> = {
  manual: '手动',
  pre_restore: '恢复前自动',
  scheduled: '定时',
}

const STATUS_TEXT: Record<string, string> = {
  running: '进行中',
  succeeded: '成功',
  failed: '失败',
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const data = await api.listBackups(props.instanceId)
    items.value = data.items
    backupRoot.value = data.backupRoot
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}

async function create(): Promise<void> {
  if (busy.value) return
  if (!targetDB.value) {
    error.value = '先选一个要备份的库'
    return
  }
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    const rec = await api.createBackup(props.instanceId, note.value.trim(), targetDB.value)
    note.value = ''
    notice.value = `库 ${rec.database || targetDB.value} 备份完成：${rec.fileName}（${formatBytes(rec.sizeBytes)}）`
    await load()
    emit('changed')
  } catch (e) {
    error.value = errText(e)
  } finally {
    busy.value = false
  }
}

function openRestore(rec: BackupRecord): void {
  restoring.value = rec
  confirmName.value = ''
  restoreError.value = ''
}

async function submitRestore(): Promise<void> {
  const rec = restoring.value
  if (!rec || busy.value) return
  busy.value = true
  restoreError.value = ''
  try {
    await api.restoreBackup(props.instanceId, rec.id, confirmName.value)
    restoring.value = null
    notice.value = `已从 ${rec.fileName} 恢复，恢复前的数据已自动备份。`
    await load()
    emit('changed')
  } catch (e) {
    restoreError.value = errText(e)
  } finally {
    busy.value = false
  }
}

async function submitDelete(): Promise<void> {
  const rec = deleting.value
  if (!rec || busy.value) return
  busy.value = true
  restoreError.value = ''
  try {
    await api.deleteBackup(props.instanceId, rec.id)
    deleting.value = null
    notice.value = `已删除备份 ${rec.fileName}`
    await load()
  } catch (e) {
    error.value = errText(e)
    deleting.value = null
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  await Promise.all([load(), loadDatabases()])
})
</script>

<template>
  <div class="panel">
    <BackupPolicyPanel :instance-id="instanceId" @changed="emit('changed')" />

    <div class="bar card">
      <header class="bar-head">
        <h2>新建备份</h2>
        <ViewSwitch />
      </header>
      <div class="toolbar">
        <select v-model="targetDB" class="select db-pick" aria-label="要备份的库" :disabled="busy || !databases.length">
          <option v-if="!databases.length" value="">（服务未运行，读不到库）</option>
          <option v-for="d in databases" :key="d.name" :value="d.name">
            {{ d.name }}
          </option>
        </select>
        <input
          v-model="note"
          class="input note-input"
          maxlength="200"
          placeholder="备份说明（可选），例如 升级前的快照"
          @keyup.enter="create"
        />
        <button type="button" class="btn btn-primary" :disabled="busy || !targetDB" @click="create">
          {{ busy ? '处理中…' : '立即备份' }}
        </button>
      </div>
      <p v-if="dbError" class="field-hint">读不到库列表：{{ dbError }}</p>
      <p class="field-hint">
        只导出选中的那个库（逻辑导出，mariadb-dump / mysqldump），生成的是可直接导入的 .sql 文件，
        文件名带库名；导出期间实例保持运行，完成后做一次完整性校验。
      </p>
    </div>

    <div v-if="error" class="notice notice-bad">
      <span>{{ error }}</span>
      <button type="button" class="btn btn-sm" @click="load()">重试</button>
    </div>
    <div v-if="notice" class="notice notice-ok">
      <span>{{ notice }}</span>
      <button type="button" class="btn btn-sm" @click="notice = ''">知道了</button>
    </div>

    <p v-if="backupRoot" class="muted root">
      备份目录：<span class="mono">{{ backupRoot }}</span>
    </p>

    <div v-if="!loading && items.length === 0" class="empty">
      <strong>还没有备份</strong>
      <span>建议在调整配置或导入数据前先备一份。</span>
    </div>

    <div v-else-if="items.length > 0 && !isCardView" class="table-wrap">
      <table class="data">
        <thead>
          <tr>
            <th>时间</th>
            <th>库</th>
            <th>类型</th>
            <th>状态</th>
            <th class="num">大小</th>
            <th>说明</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="rec in pagedRecs" :key="rec.id">
            <td>
              <div>{{ formatTime(rec.startedAt) }}</div>
              <div class="muted small">{{ relativeTime(rec.startedAt) }}</div>
            </td>
            <td class="mono">{{ rec.database || '默认库' }}</td>
            <td class="muted">{{ TYPE_TEXT[rec.type] ?? rec.type }}</td>
            <td>
              <span
                class="pill"
                :class="rec.status === 'succeeded' ? 'pill-ok' : rec.status === 'failed' ? 'pill-bad' : 'pill-warn'"
              >
                {{ STATUS_TEXT[rec.status] ?? rec.status }}
              </span>
              <div v-if="rec.errorHint" class="muted small">{{ rec.errorHint }}</div>
            </td>
            <td class="num">{{ formatBytes(rec.sizeBytes) }}</td>
            <td class="muted small">{{ rec.note || '—' }}</td>
            <td class="ops">
              <a
                v-if="rec.status === 'succeeded'"
                class="btn btn-sm"
                :href="api.backupDownloadUrl(instanceId, rec.id)"
                :download="rec.fileName"
              >
                下载
              </a>
              <button
                v-if="rec.status === 'succeeded'"
                type="button"
                class="btn btn-sm"
                @click="openRestore(rec)"
              >
                恢复
              </button>
              <button type="button" class="btn btn-sm btn-danger" @click="deleting = rec">删除</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-else-if="items.length > 0" class="bak-cards">
      <article v-for="rec in pagedRecs" :key="rec.id" class="bak-card">
        <header class="bak-head">
          <span class="mono bak-db">{{ rec.database || '默认库' }}</span>
          <span
            class="pill"
            :class="rec.status === 'succeeded' ? 'pill-ok' : rec.status === 'failed' ? 'pill-bad' : 'pill-warn'"
          >
            {{ STATUS_TEXT[rec.status] ?? rec.status }}
          </span>
        </header>
        <div class="small">
          {{ formatTime(rec.startedAt) }}
          <span class="muted">· {{ relativeTime(rec.startedAt) }}</span>
        </div>
        <div class="small muted">
          {{ TYPE_TEXT[rec.type] ?? rec.type }} · {{ formatBytes(rec.sizeBytes) }}
        </div>
        <p v-if="rec.note" class="small note">{{ rec.note }}</p>
        <p v-if="rec.errorHint" class="small bad-note">{{ rec.errorHint }}</p>
        <div class="bak-actions">
          <a
            v-if="rec.status === 'succeeded'"
            class="btn btn-sm"
            :href="api.backupDownloadUrl(instanceId, rec.id)"
            :download="rec.fileName"
          >
            下载
          </a>
          <button v-if="rec.status === 'succeeded'" type="button" class="btn btn-sm" @click="openRestore(rec)">
            恢复
          </button>
          <button type="button" class="btn btn-sm btn-danger" @click="deleting = rec">删除</button>
        </div>
      </article>
    </div>

    <Pager v-model:page="pageNo" v-model:page-size="pageSize" :total="pageTotal" />

    <Modal v-if="restoring" title="恢复备份" :busy="busy" @close="restoring = null">
      <p class="tip">
        将用 <span class="mono">{{ restoring.fileName }}</span> 覆盖库
        <strong class="mono">{{ restoring.database || '默认库' }}</strong> 的数据（这个库会被整个重建）。
        恢复前系统会自动备份同一个库，所以这一步可以再回去。
      </p>
      <div class="field">
        <label for="r-confirm">输入实例名以确认</label>
        <input
          id="r-confirm"
          v-model="confirmName"
          class="input"
          :placeholder="instanceName"
          @keyup.enter="submitRestore"
        />
      </div>
      <div v-if="restoreError" class="notice notice-bad">{{ restoreError }}</div>
      <template #foot>
        <button type="button" class="btn" :disabled="busy" @click="restoring = null">取消</button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="busy || confirmName !== instanceName"
          @click="submitRestore"
        >
          {{ busy ? '恢复中…' : '确认恢复' }}
        </button>
      </template>
    </Modal>

    <Modal v-if="deleting" title="删除备份" :busy="busy" @close="deleting = null">
      <p class="tip">
        将永久删除 <span class="mono">{{ deleting.fileName }}</span>（{{ formatBytes(deleting.sizeBytes) }}），
        删除后无法恢复。
      </p>
      <template #foot>
        <button type="button" class="btn" :disabled="busy" @click="deleting = null">取消</button>
        <button type="button" class="btn btn-danger" :disabled="busy" @click="submitDelete">
          {{ busy ? '删除中…' : '确认删除' }}
        </button>
      </template>
    </Modal>
  </div>
</template>


/* ---------- 备份卡片 ---------- */

.bak-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(250px, 1fr));
  gap: 10px;
}

.bak-card {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 12px 14px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
}

.bak-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.bak-db {
  font-size: 13.5px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
}

.bak-card .note {
  margin: 0;
}

.bak-card .bad-note {
  margin: 0;
  color: var(--bad);
}

.bak-actions {
  display: flex;
  gap: 6px;
  margin-top: auto;
  padding-top: 8px;
  border-top: 1px solid var(--line);
}

<style scoped>
.panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.bar-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 10px;
}

.bar-head h2 {
  margin: 0;
}

.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.toolbar .btn {
  flex: none;
}

/* 库选择是"选哪个库"，给固定宽度；说明输入吃掉剩余空间 */
.db-pick {
  flex: 0 0 200px;
  width: 200px;
}

.note-input {
  flex: 1 1 240px;
  width: auto;
  min-width: 0;
}

@media (max-width: 720px) {
  .db-pick,
  .note-input {
    flex: 1 1 100%;
    width: 100%;
  }

  .toolbar .btn {
    flex: 1 1 auto;
  }
}

.root {
  margin: 0;
  font-size: 12px;
  word-break: break-all;
}

.small {
  font-size: 12px;
}

.ops {
  text-align: right;
  white-space: nowrap;
}

.ops > * {
  margin-left: 6px;
}

a.btn {
  display: inline-block;
  text-decoration: none;
  color: inherit;
}

.tip {
  margin: 0 0 12px;
  font-size: 13.5px;
  line-height: 1.6;
}

</style>
