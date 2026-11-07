<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import { errText } from '../format'

const props = defineProps<{ instanceId: string; engine: string }>()
const emit = defineEmits<{ changed: [] }>()

// 导入导出必须落在某个库上：服务里可能有好几个库，先选库再谈表
const databases = ref<{ name: string }[]>([])
const targetDB = ref('')
// 表名清单是导入导出的共同前提：先拿到它，两处选择框才不会出现"能选但不能用"的表
const tables = ref<string[]>([])
const loading = ref(false)
const error = ref('')

const exportTable = ref('')
const exportLimit = ref('')

const importMode = ref<'csv' | 'sql'>('csv')
const csvTable = ref('')
const csvText = ref('')
const sqlText = ref('')

const busy = ref(false)
const notice = ref('')
const formError = ref('')

const MAX_BYTES = 20 * 1024 * 1024

const LIMIT_OPTIONS = [
  { value: '', label: '全部（上限 50 万行）' },
  { value: '1000', label: '前 1000 行' },
  { value: '10000', label: '前 1 万行' },
  { value: '100000', label: '前 10 万行' },
]

const canExport = computed(() => exportTable.value !== '')

const downloadHref = computed(() =>
  exportTable.value && targetDB.value
    ? api.exportUrl(
        props.instanceId,
        targetDB.value,
        exportTable.value,
        exportLimit.value ? Number(exportLimit.value) : undefined,
      )
    : '#',
)

async function loadDatabases(): Promise<void> {
  const data = await api.listDatabases(props.instanceId)
  databases.value = data.items.map((d) => ({ name: d.name }))
  if (!targetDB.value || !databases.value.some((d) => d.name === targetDB.value)) {
    targetDB.value = databases.value[0]?.name ?? ''
  }
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    await loadDatabases()
    if (!targetDB.value) {
      tables.value = []
      return
    }
    const data = await api.listTables(props.instanceId, targetDB.value)
    tables.value = data.items
    if (!tables.value.includes(exportTable.value)) exportTable.value = tables.value[0] ?? ''
    if (!tables.value.includes(csvTable.value)) csvTable.value = ''
  } catch (e) {
    error.value = errText(e)
    tables.value = []
  } finally {
    loading.value = false
  }
}

/** 读本地文件内容进输入框：让用户能直接选 .csv / .sql，而不是先开编辑器复制粘贴。 */
function pickFile(target: 'csv' | 'sql'): void {
  const input = document.createElement('input')
  input.type = 'file'
  input.accept = target === 'csv' ? '.csv,text/csv' : '.sql,text/plain'
  input.onchange = () => {
    const file = input.files?.[0]
    if (!file) return
    if (file.size > MAX_BYTES) {
      formError.value = `文件超过 20MB（当前 ${(file.size / 1024 / 1024).toFixed(1)}MB），请先拆分`
      return
    }
    const reader = new FileReader()
    reader.onload = () => {
      const text = String(reader.result ?? '')
      if (target === 'csv') {
        csvText.value = text
        // 文件名往往就是表名，能对上时顺手选中，省一次点击
        const stem = file.name.replace(/\.[^.]+$/, '')
        if (tables.value.includes(stem)) csvTable.value = stem
      } else {
        sqlText.value = text
      }
      formError.value = ''
    }
    reader.onerror = () => {
      formError.value = '读取文件失败，请确认文件没有被其他程序占用'
    }
    reader.readAsText(file)
  }
  input.click()
}

async function submitImport(): Promise<void> {
  if (busy.value) return
  formError.value = ''
  notice.value = ''
  const body = importMode.value === 'csv'
    ? JSON.stringify({ table: csvTable.value, csv: csvText.value })
    : sqlText.value
  if (new TextEncoder().encode(body).length > MAX_BYTES) {
    formError.value = '提交内容超过 20MB，请先拆分文件'
    return
  }
  busy.value = true
  try {
    if (importMode.value === 'csv') {
      if (!csvTable.value) throw new Error('请选择要导入的表')
      if (!csvText.value.trim()) throw new Error('CSV 内容为空')
      const res = await api.importCSV(props.instanceId, targetDB.value, csvTable.value, csvText.value)
      notice.value = `已写入 ${res.table} ${res.rows} 行`
    } else {
      if (!sqlText.value.trim()) throw new Error('脚本内容为空')
      const res = await api.importSQL(props.instanceId, targetDB.value, sqlText.value)
      notice.value = `执行成功 ${res.statements} 条语句，用时 ${res.elapsedMs} 毫秒`
    }
    csvText.value = ''
    sqlText.value = ''
    await load()
    emit('changed')
  } catch (e) {
    formError.value = errText(e)
  } finally {
    busy.value = false
  }
}

watch(() => props.instanceId, load)
onMounted(load)
// 换库要重新读表：表名是库内独立的
watch(targetDB, (next, prev) => {
  if (next && prev !== undefined && next !== prev) void load()
})
</script>

<template>
  <div class="panel">
    <div class="lib-bar">
      <label for="t-db">库</label>
      <select id="t-db" v-model="targetDB" class="select lib-select mono" :disabled="!databases.length">
        <option v-if="!databases.length" value="">（还没有库）</option>
        <option v-for="d in databases" :key="d.name" :value="d.name">{{ d.name }}</option>
      </select>
      <span class="muted small">导出、CSV 导入、SQL 脚本都作用在这个库上。</span>
    </div>

    <div v-if="loading" class="empty"><strong>读取表清单中…</strong></div>

    <div v-if="error" class="notice notice-warn">
      <span>{{ error }}</span>
      <button type="button" class="btn btn-sm" @click="load()">重试</button>
    </div>

    <template v-if="!loading && !error">
      <section class="card">
        <h2>导出为 CSV</h2>
        <p class="tip">
          一次导一张表，第一行是列名。空值写成 <span class="mono">\N</span>，
          文件带 UTF-8 字节序标记，Excel 直接双击打开中文不会乱码。
        </p>
        <div class="toolbar">
          <select v-model="exportTable" class="select table-select" aria-label="导出表">
            <option value="">请选择表</option>
            <option v-for="t in tables" :key="t" :value="t">{{ t }}</option>
          </select>
          <select v-model="exportLimit" class="select limit-select" aria-label="导出行数上限">
            <option v-for="opt in LIMIT_OPTIONS" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
          </select>
          <a
            v-if="canExport"
            class="btn btn-primary"
            :href="downloadHref"
            :download="`${exportTable}.csv`"
          >
            下载
          </a>
          <button v-else type="button" class="btn btn-primary" disabled>下载</button>
        </div>
        <p v-if="!databases.length" class="muted small">
          这个服务里还没有库，先去「数据库与账号」建一个。
        </p>
        <p v-else-if="tables.length === 0" class="muted small">
          库 {{ targetDB }} 里还没有表，导入数据或执行脚本之后再来导出。
        </p>
      </section>

      <section class="card">
        <h2>导入数据</h2>
        <nav class="switch">
          <button type="button" :class="{ active: importMode === 'csv' }" @click="importMode = 'csv'">
            CSV 写入表
          </button>
          <button type="button" :class="{ active: importMode === 'sql' }" @click="importMode = 'sql'">
            SQL 脚本
          </button>
        </nav>

        <template v-if="importMode === 'csv'">
          <p class="tip">
            追加写入已存在的表，第一行必须是列名；NULL 写 <span class="mono">\N</span>，留空表示空字符串。
            需要新建表请改用 SQL 脚本。
          </p>
          <div class="toolbar">
            <label class="inline-label" for="i-table">目标表</label>
            <select id="i-table" v-model="csvTable" class="select table-select">
              <option value="">请选择表</option>
              <option v-for="t in tables" :key="t" :value="t">{{ t }}</option>
            </select>
          </div>
          <div class="field">
            <div class="row head">
              <label for="i-csv">CSV 内容</label>
              <button type="button" class="btn btn-sm" @click="pickFile('csv')">选择文件…</button>
            </div>
            <textarea
              id="i-csv"
              v-model="csvText"
              class="textarea mono"
              rows="8"
              placeholder="id,title&#10;1,第一篇"
            />
          </div>
        </template>

        <template v-else>
          <p class="tip">
            支持适配当前引擎的普通 SQL 脚本。语句按顺序执行；失败时先前成功的语句不保证整体回滚，请先备份。
            <template v-if="engine === 'postgres'">不支持 psql 元命令、COPY FROM stdin 或自定义格式备份文件。</template>
            <template v-else>不支持 mysql 客户端元命令（source、delimiter 等）。</template>
          </p>
          <div class="field">
            <div class="row head">
              <label for="i-sql">SQL 脚本</label>
              <button type="button" class="btn btn-sm" @click="pickFile('sql')">选择文件…</button>
            </div>
            <textarea
              id="i-sql"
              v-model="sqlText"
              class="textarea mono"
              rows="10"
              placeholder="CREATE TABLE ...;&#10;INSERT INTO ...;"
            />
          </div>
        </template>

        <div v-if="notice" class="notice notice-ok">
          <span>{{ notice }}</span>
          <button type="button" class="btn btn-sm" @click="notice = ''">知道了</button>
        </div>
        <div v-if="formError" class="notice notice-bad">{{ formError }}</div>

        <div class="row submit">
          <span class="muted small">导入会直接写进当前数据库，建议先导一份备份。</span>
          <button
            type="button"
            class="btn btn-primary"
            :disabled="busy || (importMode === 'csv' && !csvTable)"
            @click="submitImport"
          >
            {{ busy ? '导入中…' : '开始导入' }}
          </button>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

section.card {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

/* 卡片内用 gap 控制间距，不再叠加 .field 的 margin */
.field {
  margin-bottom: 0;
}

/* 顶部"当前库"一行：标签 + 定宽下拉 + 说明 */
.lib-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.lib-bar > label {
  flex: none;
  color: var(--fg-muted);
  font-size: 13.5px;
  white-space: nowrap;
}

.lib-select {
  flex: 0 0 220px;
  width: 220px;
}

/* 工具行：下拉与按钮同一行，宽度按语义分配 */
.toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 10px;
}

.toolbar .btn,
.toolbar a.btn {
  flex: none;
}

.table-select {
  flex: 1 1 240px;
  width: auto;
  min-width: 0;
}

/* 行数上限只有几个选项，不需要跟着表名一起变宽 */
.limit-select {
  flex: 0 0 150px;
  width: 150px;
}

.inline-label {
  flex: none;
  color: var(--fg-muted);
  font-size: 13.5px;
  white-space: nowrap;
}

@media (max-width: 720px) {
  .lib-select,
  .table-select,
  .limit-select {
    flex: 1 1 100%;
    width: 100%;
  }

  .toolbar .btn,
  .toolbar a.btn {
    flex: 1 1 auto;
    text-align: center;
  }
}

.tip {
  margin: 0;
  font-size: 13px;
  line-height: 1.6;
  color: var(--fg-muted);
}

.small {
  font-size: 12px;
}

.head {
  justify-content: space-between;
}

.switch {
  display: flex;
  gap: 2px;
  align-self: flex-start;
  padding: 3px;
  border: 1px solid var(--line);
  border-radius: 9px;
  background: var(--bg);
}

.switch button {
  padding: 5px 12px;
  border: 0;
  border-radius: 7px;
  background: transparent;
  color: var(--fg-muted);
  font-size: 13px;
}

.switch button.active {
  background: var(--bg-elevated);
  color: var(--accent);
  font-weight: 600;
}

.submit {
  justify-content: flex-end;
}

a.btn {
  display: inline-block;
  text-decoration: none;
  color: inherit;
}
</style>
