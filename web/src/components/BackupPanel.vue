<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api, type BackupRecord } from '../api'
import { errText, formatBytes, formatTime, relativeTime } from '../format'
import Modal from './Modal.vue'

const props = defineProps<{ instanceId: string; instanceName: string }>()
const emit = defineEmits<{ changed: [] }>()

const items = ref<BackupRecord[]>([])
const backupRoot = ref('')
const loading = ref(false)
const error = ref('')
const notice = ref('')

const note = ref('')
const busy = ref(false)

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
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    const rec = await api.createBackup(props.instanceId, note.value.trim())
    note.value = ''
    notice.value = `备份完成：${rec.fileName}（${formatBytes(rec.sizeBytes)}）`
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

onMounted(load)
</script>

<template>
  <div class="panel">
    <div class="bar card">
      <div class="grow">
        <h2>新建备份</h2>
        <div class="row">
          <input
            v-model="note"
            class="input"
            maxlength="200"
            placeholder="备份说明（可选），例如 升级前的快照"
            @keyup.enter="create"
          />
          <button type="button" class="btn btn-primary" :disabled="busy" @click="create">
            {{ busy ? '处理中…' : '立即备份' }}
          </button>
        </div>
        <p class="field-hint">
          备份使用 SQLite 一致性快照，备份过程中不影响读写；生成后会做一次完整性校验。
        </p>
      </div>
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
      <span>建议在改表结构或批量写入前先备一份。</span>
    </div>

    <div v-else-if="items.length > 0" class="table-wrap">
      <table class="data">
        <thead>
          <tr>
            <th>时间</th>
            <th>类型</th>
            <th>状态</th>
            <th class="num">大小</th>
            <th>说明</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="rec in items" :key="rec.id">
            <td>
              <div>{{ formatTime(rec.startedAt) }}</div>
              <div class="muted small">{{ relativeTime(rec.startedAt) }}</div>
            </td>
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

    <Modal v-if="restoring" title="恢复备份" :busy="busy" @close="restoring = null">
      <p class="tip">
        将用 <span class="mono">{{ restoring.fileName }}</span> 覆盖当前数据库。恢复前系统会自动备份当前数据，
        所以这一步可以再回去。
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

<style scoped>
.panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.bar h2 {
  margin-bottom: 8px;
}

.root {
  margin: 0;
  font-size: 11px;
  word-break: break-all;
}

.small {
  font-size: 11px;
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
  font-size: 13px;
  line-height: 1.6;
}
</style>
