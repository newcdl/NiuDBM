<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { api, type BackupPolicy } from '../api'
import { errText, formatTime } from '../format'

const props = defineProps<{ instanceId: string }>()
const emit = defineEmits<{ changed: [] }>()

const WEEKDAYS = [
  { value: 0, label: '周日' },
  { value: 1, label: '周一' },
  { value: 2, label: '周二' },
  { value: 3, label: '周三' },
  { value: 4, label: '周四' },
  { value: 5, label: '周五' },
  { value: 6, label: '周六' },
]

const policy = ref<BackupPolicy | null>(null)
const loading = ref(false)
const saving = ref(false)
const error = ref('')

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    policy.value = await api.getBackupPolicy(props.instanceId)
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  if (!policy.value || saving.value) return
  saving.value = true
  error.value = ''
  try {
    policy.value = await api.updateBackupPolicy(props.instanceId, policy.value)
    ElMessage.success('备份策略已保存')
    emit('changed')
  } catch (e) {
    error.value = errText(e)
  } finally {
    saving.value = false
  }
}

function scheduleText(): string {
  const p = policy.value
  if (!p) return ''
  if (p.schedule === 'daily') return `每天 ${p.time}`
  return `每周${WEEKDAYS.find((w) => w.value === p.weekday)?.label ?? ''} ${p.time}`
}

onMounted(load)
</script>

<template>
  <div class="panel card" v-loading="loading">
    <div class="head">
      <h2>定时备份</h2>
      <el-switch v-if="policy" v-model="policy.enabled" :disabled="saving" />
    </div>

    <p class="intro muted">
      按时自动备份，超出保留份数自动清理最旧的。NAS 关机错过的时间点会在开机后补一次。
    </p>

    <div v-if="error" class="notice notice-bad">
      <span>{{ error }}</span>
      <button type="button" class="btn btn-sm" @click="load()">重试</button>
    </div>

    <template v-if="policy">
      <div class="grid">
        <div class="field">
          <label>周期</label>
          <el-select v-model="policy.schedule" :disabled="saving || !policy.enabled">
            <el-option label="每天" value="daily" />
            <el-option label="每周" value="weekly" />
          </el-select>
        </div>

        <div v-if="policy.schedule === 'weekly'" class="field">
          <label>星期几</label>
          <el-select v-model="policy.weekday" :disabled="saving || !policy.enabled">
            <el-option v-for="w in WEEKDAYS" :key="w.value" :label="w.label" :value="w.value" />
          </el-select>
        </div>

        <div class="field">
          <label>执行时刻</label>
          <el-time-picker
            v-model="policy.time"
            format="HH:mm"
            value-format="HH:mm"
            :disabled="saving || !policy.enabled"
            placeholder="例如 02:00"
          />
        </div>

        <div class="field">
          <label>保留份数</label>
          <el-input-number
            v-model="policy.retentionCount"
            :min="1"
            :max="99"
            :step="1"
            :disabled="saving || !policy.enabled"
          />
        </div>

        <div class="field">
          <label>加密备份</label>
          <el-switch v-model="policy.encrypt" :disabled="saving || !policy.enabled" />
        </div>
      </div>

      <div class="meta">
        <span class="muted">备份位置：<span class="mono">{{ policy.targetRef }}</span></span>
        <template v-if="policy.lastRunAt">
          <span class="sep">·</span>
          <span class="muted">
            上次：
            <span :class="policy.lastResult === 'succeeded' ? 'ok' : 'bad'">
              {{ policy.lastResult === 'succeeded' ? '成功' : '失败' }}
            </span>
            {{ formatTime(policy.lastRunAt) }}
          </span>
        </template>
        <template v-if="policy.nextRunAt && policy.enabled">
          <span class="sep">·</span>
          <span class="muted">下次：{{ formatTime(policy.nextRunAt) }}（{{ scheduleText() }}）</span>
        </template>
      </div>

      <div class="actions">
        <el-button type="primary" :loading="saving" @click="save">保存策略</el-button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.panel {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.head h2 {
  margin: 0;
  font-size: 13.5px;
  font-weight: 600;
  color: var(--fg-muted);
}

.intro {
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: 12px 16px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 5px;
}

.field > label {
  color: var(--fg-muted);
  font-size: 13px;
}

.field :deep(.el-select),
.field :deep(.el-time-picker),
.field :deep(.el-input-number) {
  width: 100%;
}

.meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 6px;
  font-size: 12.5px;
}

.meta .mono {
  word-break: break-all;
}

.sep {
  opacity: 0.5;
}

.ok {
  color: var(--ok);
}

.bad {
  color: var(--bad);
}

.actions {
  display: flex;
  gap: 8px;
}

.actions .el-button + .el-button {
  margin-left: 0;
}
</style>
