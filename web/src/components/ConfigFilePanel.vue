<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { api, type ConfigFileMeta } from '../api'
import { errText } from '../format'

/**
 * 配置文件原文编辑。两个引擎共用这一套界面，但语义不同：
 *   - MySQL：conf.d 里的一段（重启生效），白名单外的参数（如 innodb_page_size）只能从这里写；
 *   - PostgreSQL：PGDATA 里的 postgresql.conf / pg_hba.conf 可以直接改，
 *     保存后立即 reload；写坏的行走 pg_file_settings 报回来，不会静默失效。
 */
const props = defineProps<{ instanceId: string; engine?: string; engineVersion?: string }>()
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
// PG 的文件清单与当前文件；MySQL 只有一份，留着同一套结构避免两套界面
const files = ref<ConfigFileMeta[]>([])
const selected = ref('')
const warnings = ref<string[]>([])
const readError = ref('')

const isPostgres = computed(() => props.engine === 'postgres')
const isMongo = computed(() => props.engine === 'mongodb')
const currentFile = computed(() => files.value.find((f) => f.name === selected.value))
const readOnly = computed(() => currentFile.value?.writable === false)

const dirty = computed(() => content.value !== original.value)
const lines = computed(() => (content.value ? content.value.split('\n').length : 0))

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const data = await api.getConfigFile(props.instanceId, selected.value || undefined)
    path.value = data.path
    content.value = data.content ?? ''
    original.value = content.value
    backupExists.value = data.backupExists
    suggested.value = data.suggested ?? ''
    missing.value = data.missing ?? []
    files.value = data.files ?? []
    selected.value = data.selected || files.value[0]?.name || ''
    readError.value = data.readError ?? ''
    warnings.value = []
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
    const res = await api.putConfigFile(props.instanceId, content.value, selected.value || undefined)
    warnings.value = res.warnings ?? []
    await load()
    needRestart.value = res.needRestart
    if (warnings.value.length) {
      ElMessage.warning(res.message)
    } else {
      ElMessage.success(res.message)
    }
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
    const res = await api.restoreConfigFile(props.instanceId, selected.value || undefined)
    warnings.value = res.warnings ?? []
    await load()
    needRestart.value = res.needRestart
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

/** 切到另一个配置文件：先丢弃草稿（草稿只属于当前文件） */
async function pick(name: string): Promise<void> {
  if (busy.value || name === selected.value) return
  if (dirty.value) {
    try {
      await ElMessageBox.confirm('切换文件会丢弃当前未保存的改动。', '切换配置文件', {
        type: 'warning',
        confirmButtonText: '切换',
        cancelButtonText: '取消',
      })
    } catch {
      return
    }
  }
  selected.value = name
  await load()
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
        <button
          type="button"
          class="btn btn-sm"
          :disabled="busy || !backupExists"
          @click="restore"
        >
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

      <!-- PG 有三个文件：主配置、访问控制、ALTER SYSTEM 的落点 -->
      <div v-if="files.length > 1" class="row file-tabs">
        <button
          v-for="f in files"
          :key="f.name"
          type="button"
          class="file-tab"
          :class="{ active: f.name === selected }"
          @click="pick(f.name)"
        >
          <span class="mono">{{ f.label }}</span>
          <span v-if="!f.writable" class="pill">只读</span>
        </button>
      </div>
      <p v-if="currentFile?.note" class="file-note">{{ currentFile.note }}</p>

      <p v-if="readError" class="notice notice-warn">
        <span>{{ readError }}</span>
        <button type="button" class="btn btn-sm" @click="load()">重试</button>
      </p>

      <div v-if="warnings.length" class="notice notice-warn warn-list">
        <div>
          <strong>这几行没通过解析，仍在用改动前的值：</strong>
          <ul>
            <li v-for="w in warnings" :key="w" class="mono">{{ w }}</li>
          </ul>
        </div>
      </div>

      <p v-if="!loading && missing.length" class="notice notice-warn">
        <span>
          这个文件缺 {{ missing.length }} 项常用参数（<span class="mono">{{ missing.slice(0, 4).join('、') }}</span><template v-if="missing.length > 4"> 等</template>）。
          点「载入推荐模板」补全，确认后保存。
        </span>
      </p>

      <template v-if="isPostgres">
        <p class="field-hint">
          PostgreSQL {{ engineVersion || '' }} · 这里是容器内 <code>{{ path || 'postgresql.conf' }}</code> 的原文，改完点保存会立刻
          <code>reload</code>，不用重启；写了 <code>restart</code> 类参数（如
          <code>shared_buffers</code>、<code>max_connections</code>）才需要重启服务。
        </p>
        <p class="field-hint">
          写错的行不会被静默丢弃：保存后会用 <code>pg_file_settings</code> 把出错的行号与参数名报在这里，
          实例继续跑旧配置。保存前会自动留一份上一版，可用「恢复上一版」退回。
        </p>
      </template>
      <template v-else-if="isMongo">
        <p class="field-hint">
          MongoDB {{ engineVersion || '' }} · 这里是容器内 <code>/etc/mongodb/mongod.conf</code> 的原文。
          <code>storage.dbPath</code> 与 <code>net.port</code> 必须保持固定，<code>security.authorization</code>
          必须为 <code>enabled</code>；保存后需要重启服务。
        </p>
      </template>
      <template v-else>
        <p class="field-hint">
          这里是 <code>{{ path || 'conf/niudbm.cnf' }}</code> 的原文，会挂进容器的 conf.d。
          「参数」页没暴露的参数（如 <code>innodb_page_size</code>）只能从这里写；保存前会自动留一份上一版。
        </p>
        <p class="field-hint">
          反向也要注意：回「参数」页保存会<strong>整份重写</strong>这个文件，手写的注释和
          <code>[client]</code> 之类的段会丢。
        </p>
      </template>

      <textarea
        v-model="content"
        class="input mono editor"
        spellcheck="false"
        :disabled="busy || loading || readOnly"
        :placeholder="
          isPostgres
            ? '# postgresql.conf\nshared_buffers = 512MB\nwork_mem = 8MB'
            : isMongo
              ? 'storage:\n  dbPath: /data/db\nnet:\n  bindIp: 0.0.0.0\n  port: 27017\nsecurity:\n  authorization: enabled'
              : '[mysqld]\ninnodb_page_size = 16K'
        "
      />

      <p class="field-hint">
        {{ lines }} 行<template v-if="dirty"> · 有未保存的改动</template><template v-if="backupExists"> · 本机已存有一版备份</template>；
        <template v-if="readOnly">当前文件由 ALTER SYSTEM 维护，只能查看。</template>
        <template v-else-if="isPostgres">保存后立即 reload 生效，restart 类参数需要重启。</template>
        <template v-else-if="isMongo">保存后需要重启 MongoDB 服务才生效。</template>
        <template v-else>保存后需要重启服务才生效。</template>
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

.file-tabs {
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 2px;
}

.file-tab {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 5px 10px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--bg-elevated);
  color: var(--fg-muted);
  font-size: 12.5px;
}

.file-tab.active {
  border-color: var(--el-color-primary-light-5);
  background: var(--accent-soft);
  color: var(--fg);
}

.file-note {
  margin: -4px 0 6px;
  font-size: 12px;
  color: var(--fg-muted);
}

.warn-list ul {
  margin: 6px 0 0;
  padding-left: 18px;
}

.warn-list li {
  font-size: 12px;
  line-height: 1.7;
  word-break: break-all;
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
