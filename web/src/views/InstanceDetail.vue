<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api, type Instance } from '../api'
import { errText, formatBytes, formatTime } from '../format'
import Modal from '../components/Modal.vue'
import SchemaPanel from '../components/SchemaPanel.vue'
import SqlConsole from '../components/SqlConsole.vue'
import BackupPanel from '../components/BackupPanel.vue'

const props = defineProps<{ instanceId: string }>()
const emit = defineEmits<{ back: []; changed: [] }>()

type TabKey = 'schema' | 'sql' | 'backups' | 'danger'

const TABS: { key: TabKey; label: string }[] = [
  { key: 'schema', label: '表结构' },
  { key: 'sql', label: 'SQL 控制台' },
  { key: 'backups', label: '备份与恢复' },
  { key: 'danger', label: '设置与删除' },
]

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
const tab = ref<TabKey>('schema')

const renaming = ref(false)
const newName = ref('')
const renameError = ref('')

const deleting = ref(false)
const confirmName = ref('')
const keepData = ref(false)
const deleteError = ref('')

const busy = ref(false)

const title = computed(() => inst.value?.name ?? '实例')
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
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
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

// 备份/恢复会改变体积与状态，回到详情时刷新一次头部信息
watch(tab, (next) => {
  if (next !== 'danger') void load()
})

onMounted(load)
</script>

<template>
  <div class="page">
    <div class="head">
      <button type="button" class="btn btn-sm" @click="emit('back')">返回列表</button>
      <div class="grow">
        <h2>{{ title }}</h2>
        <div class="row meta muted">
          <span v-if="inst" class="pill" :class="statusClass">
            {{ STATUS_TEXT[inst.status] ?? inst.status }}
          </span>
          <span v-if="inst">{{ inst.engine }} · {{ PROFILE_TEXT[inst.profile] ?? inst.profile }}</span>
          <span v-if="inst">{{ formatBytes(inst.sizeBytes) }}</span>
          <span v-if="inst && inst.tableCount !== undefined">{{ inst.tableCount }} 张表</span>
          <span v-if="inst">创建于 {{ formatTime(inst.createdAt) }}</span>
        </div>
      </div>
      <button type="button" class="btn btn-sm" :disabled="loading" @click="load()">
        {{ loading ? '刷新中' : '刷新' }}
      </button>
    </div>

    <div v-if="error" class="notice notice-bad">
      <span>{{ error }}</span>
      <button type="button" class="btn btn-sm" @click="load()">重试</button>
    </div>

    <template v-else>
      <nav class="tabs">
        <button
          v-for="t in TABS"
          :key="t.key"
          type="button"
          class="tab"
          :class="{ active: tab === t.key, danger: t.key === 'danger' }"
          @click="tab = t.key"
        >
          {{ t.label }}
        </button>
      </nav>

      <SchemaPanel v-if="tab === 'schema'" :instance-id="instanceId" />
      <!-- SQL 控制台常驻（v-show）：切到别的 tab 再回来，已输入的语句与结果都还在 -->
      <SqlConsole v-show="tab === 'sql'" :instance-id="instanceId" @executed="load()" />
      <BackupPanel
        v-if="tab === 'backups'"
        :instance-id="instanceId"
        :instance-name="title"
        @changed="load()"
      />

      <div v-if="tab === 'danger'" class="danger">
        <section class="card">
          <h2>实例信息</h2>
          <dl class="kv">
            <dt>实例 ID</dt>
            <dd class="mono">{{ inst?.id ?? '—' }}</dd>
            <dt>数据文件</dt>
            <dd class="mono">{{ inst?.filePath ?? '—' }}</dd>
            <dt>最后变更</dt>
            <dd>{{ inst ? formatTime(inst.updatedAt) : '—' }}</dd>
          </dl>
          <button type="button" class="btn" @click="openRename">修改名称</button>
        </section>

        <section class="card danger-card">
          <h2>删除实例</h2>
          <p class="tip">
            删除后实例会从列表消失。选择「保留数据文件」会留下数据库文件以便手工找回，
            不保留则移入回收站目录，卸载应用才会清理。
          </p>
          <button type="button" class="btn btn-danger" @click="openDelete">删除实例</button>
        </section>
      </div>
    </template>

    <Modal v-if="renaming" title="修改实例名" :busy="busy" @close="renaming = false">
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

    <Modal v-if="deleting" title="删除实例" :busy="busy" @close="deleting = false">
      <p class="tip">
        <strong>{{ title }}</strong> 将被删除，列表里再也看不到它。
      </p>
      <div class="field">
        <label for="d-confirm">输入实例名以确认</label>
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
        <span>保留数据文件（不勾选则移入回收站，可人工找回）</span>
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
.page {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 1020px;
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
  font-size: 12px;
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
  font-size: 12px;
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
  font-size: 12px;
}

.kv dd {
  margin: 0;
  font-size: 13px;
  word-break: break-all;
}

.tip {
  margin: 0 0 12px;
  font-size: 13px;
  line-height: 1.6;
}

.check {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
}
</style>
