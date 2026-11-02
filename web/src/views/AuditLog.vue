<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Refresh } from '@element-plus/icons-vue'
import ViewSwitch from '../components/ViewSwitch.vue'
import { api, type AuditRecord } from '../api'
import { errText, formatTime } from '../format'
import { isMobile } from '../media'
import { isCardView } from '../view-mode'
import Pager from '../components/Pager.vue'
import { usePager } from '../pager'

const items = ref<AuditRecord[]>([])

// 操作记录一次拉最近 N 条，前端切片翻页，翻页不再请求
const { page: pageNo, pageSize, total: pageTotal, items: pagedItems } = usePager(items, 20)
const loading = ref(false)
const error = ref('')
const limit = ref(100)

const limitOptions = [
  { label: '最近 50 条', value: 50 },
  { label: '最近 100 条', value: 100 },
  { label: '最近 300 条', value: 300 },
]

const ACTION_TEXT: Record<string, string> = {
  'instance.create': '创建实例',
  'instance.rename': '重命名实例',
  'instance.delete': '删除实例',
  'instance.query.exec': '历史写操作',
  'instance.start': '启动实例',
  'instance.stop': '停止实例',
  'instance.config.update': '修改配置',
  'instance.import': '导入数据',
  'instance.export': '导出数据',
  'dbuser.create': '创建数据库用户',
  'dbuser.update': '修改数据库用户',
  'dbuser.delete': '删除数据库用户',
  'engine.install': '安装引擎',
  'engine.repair': '修复引擎',
  'engine.uninstall': '卸载引擎',
  'backup.create': '创建备份',
  'backup.restore': '恢复备份',
  'backup.delete': '删除备份',
}

const RESULT_TEXT: Record<string, string> = {
  ok: '成功',
  failed: '失败',
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const data = await api.audit(limit.value)
    items.value = data.items
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}

function actionText(action: string): string {
  return ACTION_TEXT[action] ?? action
}

onMounted(load)
</script>

<template>
  <div class="page">
    <div class="toolbar">
      <div class="grow">
        <p class="lead">所有会改变数据的操作都会留痕，便于回溯「谁在什么时候动了什么」。</p>
        <p class="meta muted">共 {{ items.length }} 条记录</p>
      </div>
      <div class="tools">
        <ViewSwitch />
        <el-select v-model="limit" class="amount" @change="load()">
          <el-option v-for="opt in limitOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
        </el-select>
        <el-button :icon="Refresh" :loading="loading" @click="load()">{{ loading ? '读取中' : '刷新' }}</el-button>
      </div>
    </div>

    <el-alert v-if="error" type="error" :closable="false" show-icon :title="error">
      <template #default>
        <el-button size="small" text type="primary" @click="load()">重试</el-button>
      </template>
    </el-alert>

    <div v-if="!loading && !items.length" class="empty-state">
      <el-empty description="还没有操作记录" :image-size="72">
        <span class="muted">建库、备份、恢复、删库都会记在这里。</span>
      </el-empty>
    </div>

    <el-table v-else-if="!isCardView" v-loading="loading" :data="pagedItems" row-key="id" class="table">
      <el-table-column label="时间" :width="isMobile ? 118 : 160">
        <template #default="{ row }">
          <span class="mono small nowrap">{{ formatTime(row.ts) }}</span>
        </template>
      </el-table-column>

      <el-table-column label="操作者" :width="isMobile ? 90 : 120" show-overflow-tooltip>
        <template #default="{ row }">{{ row.username || row.userId || '系统' }}</template>
      </el-table-column>

      <el-table-column label="动作" :min-width="isMobile ? 130 : 180">
        <template #default="{ row }">
          <div>{{ actionText(row.action) }}</div>
          <div v-if="row.targetId" class="mono small muted">{{ row.targetId }}</div>
        </template>
      </el-table-column>

      <el-table-column label="结果" width="84">
        <template #default="{ row }">
          <el-tag size="small" :type="row.result === 'ok' ? 'success' : 'danger'">
            {{ RESULT_TEXT[row.result] ?? row.result }}
          </el-tag>
        </template>
      </el-table-column>

      <el-table-column v-if="!isMobile" label="详情" min-width="220">
        <template #default="{ row }">
          <span class="small detail">{{ row.detail || '—' }}</span>
        </template>
      </el-table-column>
    </el-table>

    <div v-else v-loading="loading" class="list-cards">
      <div v-for="item in pagedItems" :key="item.id" class="list-card">
        <div class="list-card-head">
          <span class="card-action">{{ actionText(item.action) }}</span>
          <el-tag size="small" :type="item.result === 'ok' ? 'success' : 'danger'">
            {{ RESULT_TEXT[item.result] ?? item.result }}
          </el-tag>
        </div>
        <div v-if="item.targetId" class="mono card-target">{{ item.targetId }}</div>
        <div class="list-card-meta">
          <span>{{ formatTime(item.ts) }}</span>
          <span>{{ item.username || item.userId || '系统' }}</span>
        </div>
        <p v-if="item.detail" class="card-detail">{{ item.detail }}</p>
      </div>
    </div>
    <Pager v-model:page="pageNo" v-model:page-size="pageSize" :total="pageTotal" />
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-width: 1400px;
  margin: 0 auto;
}

.toolbar {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  flex-wrap: wrap;
}

.lead {
  margin: 0;
  font-size: 13.5px;
}

.meta {
  margin: 4px 0 0;
  font-size: 13px;
}

.tools {
  display: flex;
  align-items: center;
  gap: 8px;
}

.tools .el-button + .el-button {
  margin-left: 0;
}

.amount {
  width: 132px;
}

.small {
  font-size: 12px;
}

.nowrap {
  white-space: nowrap;
}

.detail {
  /* 断行优先在空格与分隔符处，避免把 ULID 从中间切断 */
  overflow-wrap: break-word;
}

.table {
  border: 1px solid var(--line);
  border-radius: var(--radius);
  overflow: hidden;
}

.empty-state {
  border: 1px dashed var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
}

.card-action {
  min-width: 0;
  overflow: hidden;
  font-size: 13.5px;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-target {
  overflow-wrap: anywhere;
  color: var(--fg-muted);
  font-size: 12px;
}

.card-detail {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  margin: 0;
  overflow: hidden;
  color: var(--fg-muted);
  font-size: 13px;
  line-height: 1.6;
}

@media (max-width: 720px) {
  .tools {
    width: 100%;
  }

  .amount {
    flex: 1;
    width: auto;
  }
}
</style>
