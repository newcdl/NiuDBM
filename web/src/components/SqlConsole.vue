<script setup lang="ts">
import { computed, ref } from 'vue'
import { api, type QueryResult } from '../api'
import { cellText, errText } from '../format'

const props = defineProps<{ instanceId: string }>()
const emit = defineEmits<{ executed: [] }>()

const sql = ref('SELECT name FROM sqlite_master WHERE type = \'table\';')
const busy = ref(false)
const error = ref('')
const result = ref<QueryResult | null>(null)

const summary = computed<string>(() => {
  const r = result.value
  if (!r) return ''
  if (r.kind === 'exec') return `执行成功，影响 ${r.rowsAffected} 行 · ${r.elapsedMs} ms`
  const base = `返回 ${r.rowCount} 行 · ${r.elapsedMs} ms`
  return r.truncated ? `${base}（已截断，只显示前 ${r.rowCount} 行）` : base
})

async function run(): Promise<void> {
  if (busy.value || !sql.value.trim()) return
  busy.value = true
  error.value = ''
  try {
    result.value = await api.query(props.instanceId, sql.value, 200)
    // 写操作可能改变表结构，让外面的结构页跟着刷新
    if (result.value.kind === 'exec') emit('executed')
  } catch (e) {
    error.value = errText(e)
    result.value = null
  } finally {
    busy.value = false
  }
}

/** Ctrl/Cmd + Enter 执行，和常见数据库客户端保持一致 */
function onKeydown(e: KeyboardEvent): void {
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
    e.preventDefault()
    void run()
  }
}
</script>

<template>
  <div class="console">
    <div class="editor">
      <textarea
        v-model="sql"
        class="textarea mono"
        rows="6"
        spellcheck="false"
        placeholder="输入单条 SQL，例如 SELECT * FROM notes LIMIT 20;"
        @keydown="onKeydown"
      />
      <div class="row editor-foot">
        <span class="muted hint">
          一次只执行一条语句 · 支持 SELECT / INSERT / UPDATE / DELETE / CREATE / ALTER / DROP
        </span>
        <button type="button" class="btn btn-primary" :disabled="busy || !sql.trim()" @click="run">
          {{ busy ? '执行中…' : '执行（Ctrl+Enter）' }}
        </button>
      </div>
    </div>

    <div v-if="error" class="notice notice-bad">{{ error }}</div>

    <template v-if="result">
      <div class="row result-head">
        <span class="pill" :class="result.kind === 'exec' ? 'pill-warn' : 'pill-accent'">
          {{ result.kind === 'exec' ? '写入' : '查询' }}
        </span>
        <span class="muted">{{ summary }}</span>
      </div>

      <div v-if="result.kind === 'query'" class="table-wrap">
        <table class="data">
          <thead>
            <tr>
              <th class="idx">#</th>
              <th v-for="c in result.columns" :key="c">{{ c }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in result.rows" :key="i">
              <td class="idx muted">{{ i + 1 }}</td>
              <td v-for="(cell, j) in row" :key="j" class="mono" :class="{ null: cell === null }">
                {{ cellText(cell) }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-else class="notice notice-ok">语句已执行，影响 {{ result.rowsAffected }} 行。</div>
    </template>
  </div>
</template>

<style scoped>
.console {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.editor {
  padding: 12px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
}

.editor textarea {
  background: var(--bg);
}

.editor-foot {
  margin-top: 10px;
  justify-content: space-between;
}

.hint {
  font-size: 11px;
}

.result-head {
  gap: 8px;
  font-size: 12px;
}

.idx {
  width: 44px;
  color: var(--fg-muted);
}

td.null {
  color: var(--fg-muted);
  font-style: italic;
}
</style>
