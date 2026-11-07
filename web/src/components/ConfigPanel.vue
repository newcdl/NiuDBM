<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, type ConfigItem, type StatusMetric, type TuningPreset } from '../api'
import { errText } from '../format'

const props = defineProps<{ instanceId: string; engine: string }>()
const emit = defineEmits<{ changed: [] }>()

// 配置项由后端给出：PostgreSQL 读 pg_settings，MySQL/MariaDB 读变量，
// 前端不预设清单，只负责按 kind 渲染控件、按 apply 说明生效方式。
const items = ref<ConfigItem[]>([])
const original = ref<Record<string, string>>({})
const draft = ref<Record<string, string>>({})

const loading = ref(false)
const busy = ref(false)
const error = ref('')
const notice = ref('')
// 保存后是否还有参数等着重启才生效：是的话给一个"立即重启"按钮，别让用户去别处找
const pendingRestart = ref(false)
const restarting = ref(false)

// 性能方案：按机器内存档次给一组算好的参数，选完只是填表，仍要自己确认保存
const presets = ref<TuningPreset[]>([])
const presetKey = ref('')

// 运行状态：判断"这台库现在健康吗、该调什么"
const statusItems = ref<StatusMetric[]>([])
const statusLoading = ref(false)
const statusError = ref('')

const APPLY_TEXT: Record<string, string> = {
  reload: '保存后立即生效',
  restart: '需重启实例才生效',
}

const BOOL_OPTIONS = ['on', 'off']

const changedKeys = computed(() =>
  items.value.filter((item) => (draft.value[item.key] ?? '') !== (original.value[item.key] ?? '')).map((item) => item.key),
)

const needRestart = computed(() =>
  changedKeys.value.some((key) => byKey(key)?.apply === 'restart'),
)

function byKey(key: string): ConfigItem | undefined {
  return items.value.find((item) => item.key === key)
}

function optionsFor(item: ConfigItem): string[] {
  if (item.options && item.options.length) return item.options
  return item.kind === 'bool' ? BOOL_OPTIONS : []
}

async function loadStatus(): Promise<void> {
  statusLoading.value = true
  statusError.value = ''
  try {
    const data = await api.instanceStatus(props.instanceId)
    statusItems.value = data.items
  } catch (e) {
    statusError.value = errText(e)
    statusItems.value = []
  } finally {
    statusLoading.value = false
  }
}

/** 把选中方案里"这个引擎上确实存在"的参数填进草稿，存在的才填，免得保存时报不支持 */
function applyPreset(): void {
  const preset = presets.value.find((item) => item.key === presetKey.value)
  if (!preset) return
  let filled = 0
  for (const [key, value] of Object.entries(preset.values)) {
    if (byKey(key)) {
      draft.value[key] = value
      filled++
    }
  }
  notice.value = `已按「${preset.label}」填入 ${filled} 项，确认后点保存`
}

async function restartNow(): Promise<void> {
  if (restarting.value) return
  try {
    await ElMessageBox.confirm(
      '重启期间连接会中断几秒，未保存的修改会丢失。',
      '重启服务使配置生效',
      { type: 'warning', confirmButtonText: '重启', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  restarting.value = true
  try {
    await api.restartInstance(props.instanceId)
    ElMessage.success('已重启，配置已生效')
    pendingRestart.value = false
    await Promise.all([load(), loadStatus()])
    emit('changed')
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    restarting.value = false
  }
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  notice.value = ''
  try {
    const [data, presetData] = await Promise.all([
      api.getConfig(props.instanceId),
      api.configPresets(props.instanceId).catch(() => ({ items: [] })),
    ])
    presets.value = presetData.items
    items.value = data.items
    const map: Record<string, string> = {}
    for (const item of data.items) map[item.key] = item.value
    original.value = map
    draft.value = { ...map }
  } catch (e) {
    error.value = errText(e)
    items.value = []
    original.value = {}
    draft.value = {}
  } finally {
    loading.value = false
  }
}

function reset(item: ConfigItem): void {
  draft.value[item.key] = item.default
}

function discard(): void {
  draft.value = { ...original.value }
  notice.value = ''
}

async function save(): Promise<void> {
  if (busy.value || changedKeys.value.length === 0) return
  busy.value = true
  error.value = ''
  try {
    const values: Record<string, string> = {}
    for (const key of changedKeys.value) values[key] = (draft.value[key] ?? '').trim()
    const res = await api.updateConfig(props.instanceId, values)
    pendingRestart.value = res.needRestart
    await load()
    notice.value = res.message
    if (res.needRestart) void loadStatus()
    emit('changed')
  } catch (e) {
    error.value = errText(e)
  } finally {
    busy.value = false
  }
}

watch(() => props.instanceId, () => {
  void load()
  void loadStatus()
})
onMounted(() => {
  void load()
  void loadStatus()
})
</script>

<template>
  <div class="panel">
    <div v-if="loading" class="empty"><strong>读取配置中…</strong></div>

    <div v-if="error" class="notice notice-warn">
      <span>{{ error }}</span>
      <button type="button" class="btn btn-sm" @click="load()">重试</button>
    </div>

    <template v-if="!loading && items.length > 0">
      <!-- 运行状态：先看现状再决定调什么，比直接丢一堆参数给人猜有用 -->
      <div class="card">
        <div class="row field-head">
          <h2>当前状态</h2>
          <div class="grow" />
          <button type="button" class="btn btn-sm" :disabled="statusLoading" @click="loadStatus()">
            {{ statusLoading ? '读取中' : '刷新' }}
          </button>
        </div>
        <p v-if="statusError" class="field-hint">{{ statusError }}</p>
        <div v-else-if="statusItems.length" class="status-grid">
          <div v-for="m in statusItems" :key="m.key" class="status-item">
            <span class="status-label">{{ m.label }}</span>
            <strong class="status-value mono">{{ m.value }}</strong>
            <span v-if="m.hint" class="status-hint">{{ m.hint }}</span>
          </div>
        </div>
        <p v-else class="muted">启动服务后可以看运行状态。</p>
      </div>

      <div class="card">
        <h2>可调参数</h2>

        <!-- 性能方案：按机器内存给一组算好的值，填完仍要自己保存 -->
        <div v-if="presets.length" class="preset">
          <div class="preset-row">
            <label class="preset-label" for="cfg-preset">优化方案</label>
            <select id="cfg-preset" v-model="presetKey" class="select preset-select">
              <option value="">按这台机器的内存选一档…</option>
              <option v-for="p in presets" :key="p.key" :value="p.key">{{ p.label }}</option>
            </select>
            <button type="button" class="btn" :disabled="!presetKey || busy" @click="applyPreset">
              应用方案
            </button>
          </div>
          <p class="preset-hint">
            选一档会把下面这些参数的推荐值填进输入框，<strong>仍要自己点「保存修改」</strong>才写入；
            不确定就选和这台 NAS 内存最接近的那档。
          </p>
        </div>
        <p class="tip">
          <template v-if="engine === 'postgres'">PostgreSQL 参数写入实例配置，标记为需重启的项目要停止再启动实例。</template>
          <template v-else>MySQL 参数写入实例的 my.cnf，标记为需重启的项目要停止再启动实例。</template>
          修改前请确认内存与持久化风险，未保存不会写入。
        </p>

        <div v-for="item in items" :key="item.key" class="item" :class="{ dirty: changedKeys.includes(item.key) }">
          <div class="label">
            <label class="name" :for="`cfg-${item.key}`">{{ item.label }}</label>
            <span class="pill">{{ APPLY_TEXT[item.apply] ?? item.apply }}</span>
            <span v-if="changedKeys.includes(item.key)" class="pill pill-accent">未保存</span>
            <div class="key mono muted">{{ item.key }}</div>
            <div v-if="item.hint" class="hint muted">{{ item.hint }}</div>
          </div>

          <div class="control">
            <select v-if="optionsFor(item).length" :id="`cfg-${item.key}`" v-model="draft[item.key]" class="select" :disabled="busy">
              <option v-for="opt in optionsFor(item)" :key="opt" :value="opt">{{ opt }}</option>
            </select>
            <input
              v-else
              :id="`cfg-${item.key}`"
              v-model="draft[item.key]"
              :disabled="busy"
              class="input mono"
              :placeholder="item.default"
              :inputmode="item.kind === 'int' ? 'numeric' : 'text'"
            />
            <div class="defaults muted">
              <span>当前 {{ item.value || '—' }}</span>
              <span>默认 {{ item.default || '—' }}</span>
              <button
                v-if="item.value !== item.default"
                type="button"
                class="link"
                :disabled="busy"
                @click="reset(item)"
              >
                填默认值
              </button>
            </div>
          </div>
        </div>
      </div>

      <div v-if="notice" class="notice notice-ok">
        <span>{{ notice }}</span>
        <button type="button" class="btn btn-sm" @click="notice = ''">知道了</button>
      </div>

      <div class="foot card">
        <div class="grow muted">
          <template v-if="changedKeys.length">
            已改 {{ changedKeys.length }} 项<template v-if="needRestart">，其中含必须重启实例才生效的参数</template>
          </template>
          <template v-else>没有待保存的修改</template>
        </div>
        <button type="button" class="btn" :disabled="busy || !changedKeys.length" @click="discard()">
          放弃修改
        </button>
        <button
          v-if="pendingRestart"
          type="button"
          class="btn btn-primary"
          :disabled="restarting"
          @click="restartNow"
        >
          {{ restarting ? '重启中…' : '立即重启生效' }}
        </button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="busy || !changedKeys.length"
          @click="save"
        >
          {{ busy ? '保存中…' : '保存修改' }}
        </button>
      </div>
    </template>
  </div>
</template>

<style scoped>
.status-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(190px, 1fr));
  gap: 10px 14px;
}

.status-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.status-label {
  color: var(--fg-muted);
  font-size: 12.5px;
}

.status-value {
  font-size: 15px;
  font-weight: 600;
}

.status-hint {
  color: var(--fg-muted);
  font-size: 12px;
  line-height: 1.5;
}

/* 优化方案：原生 select 的宽度不看 option 文本，不给死宽度就会被压成一条缝 */
.preset {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin: 10px 0 16px;
  padding: 12px 14px;
  border: 1px dashed var(--line);
  border-radius: var(--radius);
  background: var(--bg);
}

.preset-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.preset-label {
  flex: none;
  color: var(--fg);
  font-size: 13.5px;
  font-weight: 500;
  white-space: nowrap;
}

.preset-select {
  flex: 0 0 240px;
  width: 240px;
  max-width: 100%;
}

.preset-row .btn {
  flex: none;
}

.preset-hint {
  margin: 0;
  color: var(--fg-muted);
  font-size: 12.5px;
  line-height: 1.6;
}

@media (max-width: 720px) {
  .preset-select {
    flex: 1 1 100%;
    width: 100%;
  }

  .preset-row .btn {
    flex: 1 1 auto;
  }
}

.panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.tip {
  margin: 0 0 6px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--fg-muted);
}

.item {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 240px;
  gap: 12px;
  padding: 12px 0;
  border-top: 1px solid var(--line);
}

.item:first-of-type {
  border-top: 0;
}

.item.dirty {
  background: var(--accent-soft);
  border-radius: 8px;
}

.label {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 0;
}

.name {
  font-size: 13.5px;
  font-weight: 600;
}

.key,
.hint {
  flex-basis: 100%;
  font-size: 12px;
  line-height: 1.5;
}

.control {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.control .input,
.control .select {
  width: 100%;
}

.defaults {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  flex-wrap: wrap;
}

.link {
  border: 0;
  background: transparent;
  color: var(--accent);
  font-size: 12px;
  padding: 0;
}

.foot {
  display: flex;
  align-items: center;
  gap: 8px;
  position: sticky;
  bottom: 0;
}

@media (max-width: 720px) {
  .item {
    grid-template-columns: minmax(0, 1fr);
    gap: 8px;
  }

  .control {
    width: 100%;
  }

  .foot {
    flex-wrap: wrap;
  }
}
</style>
