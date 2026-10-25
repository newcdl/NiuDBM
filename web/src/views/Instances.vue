<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, type Instance } from '../api'
import { errText, formatBytes, relativeTime } from '../format'
import Modal from '../components/Modal.vue'
import InstanceDetail from './InstanceDetail.vue'

const items = ref<Instance[]>([])
const loading = ref(false)
const error = ref('')
const keyword = ref('')
const selectedID = ref('')

const creating = ref(false)
const busy = ref(false)
const formError = ref('')
const form = ref({ name: '', profile: 'low', seedSql: '', note: '' })

const PROFILE_TEXT: Record<string, string> = {
  low: '轻量',
  standard: '标准',
  performance: '高性能',
}

const STATUS_TEXT: Record<string, string> = {
  running: '运行中',
  stopped: '已停止',
  error: '异常',
  restoring: '恢复中',
}

const seedPlaceholder = `-- 可选：创建时执行的初始化语句
CREATE TABLE notes (
  id INTEGER PRIMARY KEY,
  title TEXT NOT NULL,
  created_at TEXT DEFAULT CURRENT_TIMESTAMP
);`

const total = computed(() => items.value.length)
const usedBytes = computed(() => items.value.reduce((sum, it) => sum + it.sizeBytes, 0))

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
}

function openCreate(): void {
  form.value = { name: '', profile: 'low', seedSql: '', note: '' }
  formError.value = ''
  creating.value = true
}

async function submit(): Promise<void> {
  if (busy.value) return
  if (!form.value.name.trim()) {
    formError.value = '请填写实例名'
    return
  }
  busy.value = true
  formError.value = ''
  try {
    const created = await api.createInstance({
      name: form.value.name.trim(),
      profile: form.value.profile,
      seedSql: form.value.seedSql.trim(),
      note: form.value.note.trim(),
    })
    creating.value = false
    await load()
    selectedID.value = created.id
  } catch (e) {
    formError.value = errText(e)
  } finally {
    busy.value = false
  }
}

function statusClass(status: string): string {
  if (status === 'running') return 'pill-ok'
  if (status === 'error') return 'pill-bad'
  return 'pill-warn'
}

onMounted(() => {
  void load()
})
</script>

<template>
  <InstanceDetail
    v-if="selectedID"
    :instance-id="selectedID"
    @back="selectedID = ''"
    @changed="load()"
  />

  <div v-else class="page">
    <div class="toolbar">
      <div class="grow">
        <p class="lead">
          每个实例是一个独立的 SQLite 数据库文件，备份默认落在共享目录，卸载应用也不会丢。
        </p>
        <p class="meta muted">
          共 {{ total }} 个实例，占用 {{ formatBytes(usedBytes) }}
        </p>
      </div>
      <div class="row">
        <input
          v-model="keyword"
          class="input search"
          type="search"
          placeholder="搜索实例名"
          @keyup.enter="load()"
        />
        <button type="button" class="btn" :disabled="loading" @click="load()">
          {{ loading ? '读取中' : '刷新' }}
        </button>
        <button type="button" class="btn btn-primary" @click="openCreate">新建实例</button>
      </div>
    </div>

    <div v-if="error" class="notice notice-bad">
      <span>{{ error }}</span>
      <button type="button" class="btn btn-sm" @click="load()">重试</button>
    </div>

    <div v-if="!loading && !error && items.length === 0" class="empty">
      <strong>还没有数据库</strong>
      <span>新建一个实例即可开始使用，无需下载任何数据库引擎。</span>
      <button type="button" class="btn btn-primary" @click="openCreate">新建第一个实例</button>
    </div>

    <div v-else-if="items.length > 0" class="table-wrap">
      <table class="data">
        <thead>
          <tr>
            <th>名称</th>
            <th>引擎</th>
            <th>状态</th>
            <th class="num">表</th>
            <th class="num">大小</th>
            <th>创建</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="it in items" :key="it.id">
            <td>
              <button type="button" class="link" @click="selectedID = it.id">{{ it.name }}</button>
            </td>
            <td class="muted">{{ it.engine }} · {{ PROFILE_TEXT[it.profile] ?? it.profile }}</td>
            <td>
              <span class="pill" :class="statusClass(it.status)">
                {{ STATUS_TEXT[it.status] ?? it.status }}
              </span>
            </td>
            <td class="num">{{ it.tableCount ?? '—' }}</td>
            <td class="num">{{ formatBytes(it.sizeBytes) }}</td>
            <td class="muted">{{ relativeTime(it.createdAt) }}</td>
            <td class="ops">
              <button type="button" class="btn btn-sm" @click="selectedID = it.id">管理</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Modal v-if="creating" title="新建实例" :busy="busy" @close="creating = false">
      <div class="field">
        <label for="f-name">实例名</label>
        <input
          id="f-name"
          v-model="form.name"
          class="input"
          maxlength="32"
          placeholder="例如 博客库"
          @keyup.enter="submit"
        />
        <span class="field-hint">最多 32 个字符，不能包含 / \ : * ? " &lt; &gt; |</span>
      </div>

      <div class="field">
        <label for="f-profile">资源档位</label>
        <select id="f-profile" v-model="form.profile" class="select">
          <option value="low">轻量 · 适合个人小库与笔记</option>
          <option value="standard">标准 · 适合常规业务库</option>
          <option value="performance">高性能 · 适合高频写入</option>
        </select>
        <span class="field-hint">档位决定 SQLite 的缓存与同步策略，建库后暂不支持修改。</span>
      </div>

      <div class="field">
        <label for="f-seed">初始化 SQL（可选）</label>
        <textarea
          id="f-seed"
          v-model="form.seedSql"
          class="textarea mono"
          rows="6"
          :placeholder="seedPlaceholder"
        />
        <span class="field-hint">可以写多条语句，按顺序执行；出错时整个实例会回退。</span>
      </div>

      <div class="field">
        <label for="f-note">备注（可选）</label>
        <input id="f-note" v-model="form.note" class="input" maxlength="200" placeholder="用途说明" />
      </div>

      <div v-if="formError" class="notice notice-bad">{{ formError }}</div>

      <template #foot>
        <button type="button" class="btn" :disabled="busy" @click="creating = false">取消</button>
        <button type="button" class="btn btn-primary" :disabled="busy" @click="submit">
          {{ busy ? '创建中…' : '创建实例' }}
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

.toolbar {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  flex-wrap: wrap;
}

.lead {
  margin: 0;
  font-size: 13px;
}

.meta {
  margin: 4px 0 0;
  font-size: 12px;
}

.search {
  width: 180px;
}

.link {
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--accent);
  font-size: 13px;
  font-weight: 500;
}

.link:hover {
  text-decoration: underline;
}

.ops {
  text-align: right;
}
</style>
