<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api } from '../api'
import { errText } from '../format'

/**
 * 配置文件原文编辑。白名单之外的参数（如 innodb_page_size）只有这一个入口，
 * 写坏了实例会起不来，所以每一步都要留退路：保存前自动 .bak、可一键恢复、保存后提示重启。
 */
const props = defineProps<{ instanceId: string }>()
const emit = defineEmits<{ changed: [] }>()

const path = ref('')
const content = ref('')
const original = ref('')
const backupExists = ref(false)
// 后端渲染好的"推荐默认配置"：老实例的文件可能只有几行，用它一键补全
const suggested = ref('')
// 当前文件里缺少的可配置项：这是判断"文件是不是太贫"的准确依据，不看行数
const missing = ref<string[]>([])
const loading = ref(false)
const busy = ref(false)
const restarting = ref(false)
const needRestart = ref(false)
const error = ref('')

const dirty = computed(() => content.value !== original.value)
const lines = computed(() => (content.value ? content.value.split('\n').length : 0))

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const data = await api.getConfigFile(props.instanceId)
    path.value = data.path
    content.value = data.content
    original.value = data.content
    backupExists.value = data.backupExists
    suggested.value = data.suggested
    missing.value = data.missing ?? []
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  if (busy.value || !dirty.value) return
  try {
    await ElMessageBox.confirm(
      '会覆盖当前配置文件，覆盖前自动留一份上一版。写错参数可能让实例起不来，到时候用「恢复上一版」退回。',
      '写入配置文件',
      { type: 'warning', confirmButtonText: '保存', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  busy.value = true
  try {
    const res = await api.putConfigFile(props.instanceId, content.value)
    await load()
    needRestart.value = true
    ElMessage.success(res.message)
    emit('changed')
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

/** 用推荐默认值填满编辑器（只改草稿，仍要自己点保存） */
async function fillTemplate(): Promise<void> {
  if (!suggested.value || busy.value) return
  try {
    await ElMessageBox.confirm(
      '会用一份带默认值的完整配置替换编辑器内容（这一步不写盘，仍要点「保存配置文件」）。手写的内容会被替换掉。',
      '载入推荐模板',
      { type: 'warning', confirmButtonText: '载入', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  content.value = suggested.value
}

async function restore(): Promise<void> {
  if (busy.value) return
  try {
    await ElMessageBox.confirm(
      '会用上一版覆盖当前配置文件，当前改动会丢失。恢复后需要重启才生效。',
      '恢复上一版配置',
      { type: 'warning', confirmButtonText: '恢复', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  busy.value = true
  try {
    const res = await api.restoreConfigFile(props.instanceId)
    await load()
    needRestart.value = true
    ElMessage.success(res.message)
    emit('changed')
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    busy.value = false
  }
}

async function restartNow(): Promise<void> {
  if (restarting.value) return
  try {
    await ElMessageBox.confirm(
      '重启期间连接会中断几秒。',
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
    needRestart.value = false
    await load()
    emit('changed')
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    restarting.value = false
  }
}

watch(() => props.instanceId, () => void load())
onMounted(() => void load())
</script>

<template>
  <div class="panel">
    <div v-if="error" class="notice notice-warn">
      <span>{{ error }}</span>
      <button type="button" class="btn btn-sm" @click="load()">重试</button>
    </div>

    <div class="card">
      <div class="row field-head">
        <h2>配置文件原文</h2>
        <div class="grow" />
        <span v-if="dirty" class="pill pill-warn">未保存</span>
        <button type="button" class="btn btn-sm" :disabled="loading || busy" @click="load()">
          {{ loading ? '读取中' : '重新载入' }}
        </button>
        <button type="button" class="btn btn-sm" :disabled="busy || !suggested" @click="fillTemplate">
          载入推荐模板
        </button>
        <button type="button" class="btn btn-sm" :disabled="busy || !backupExists" @click="restore">
          恢复上一版
        </button>
        <button
          v-if="needRestart"
          type="button"
          class="btn btn-sm btn-primary"
          :disabled="restarting"
          @click="restartNow"
        >
          {{ restarting ? '重启中…' : '重启生效' }}
        </button>
        <button type="button" class="btn btn-sm btn-primary" :disabled="busy || !dirty" @click="save">
          {{ busy ? '保存中…' : '保存配置文件' }}
        </button>
      </div>

      <p v-if="!loading && missing.length" class="notice notice-warn">
        <span>
          这个文件缺 {{ missing.length }} 项常用参数（<span class="mono">{{ missing.slice(0, 4).join('、') }}</span><template v-if="missing.length > 4"> 等</template>）。
          点「载入推荐模板」补全，确认后保存。
        </span>
      </p>

      <p class="field-hint">
        这里是 <code>{{ path || 'conf/niudbm.cnf' }}</code> 的原文，会挂进容器的 conf.d。
        「参数」页没暴露的参数（如 <code>innodb_page_size</code>）只能从这里写；保存前会自动留一份上一版。
      </p>
      <p class="field-hint">
        反向也要注意：回「参数」页保存会<strong>整份重写</strong>这个文件，手写的注释和
        <code>[client]</code> 之类的段会丢。
      </p>

      <textarea
        v-model="content"
        class="input mono editor"
        spellcheck="false"
        :disabled="busy || loading"
        placeholder="[mysqld]&#10;innodb_page_size = 16K"
      />

      <p class="field-hint">
        {{ lines }} 行<template v-if="dirty"> · 有未保存的改动</template><template v-if="backupExists"> · 本机已存有一版备份</template>；保存后需要重启服务才生效。
      </p>
    </div>
  </div>
</template>

<style scoped>
.panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.field-head h2 {
  margin: 0;
}

/* 等宽 + 可纵向拉伸，长配置不会把卡片撑破 */
.editor {
  width: 100%;
  min-height: 380px;
  padding: 12px 14px;
  line-height: 1.7;
  font-size: 12.5px;
  tab-size: 2;
  white-space: pre;
  overflow: auto;
  resize: vertical;
}

@media (max-width: 720px) {
  .editor {
    min-height: 240px;
    font-size: 12px;
  }
}
</style>
