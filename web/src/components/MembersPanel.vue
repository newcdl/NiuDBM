<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Delete } from '@element-plus/icons-vue'
import { api, type InstanceMember } from '../api'
import { errText, formatTime } from '../format'

const props = defineProps<{ instanceId: string; canManage?: boolean }>()
const items = ref<InstanceMember[]>([])
const loading = ref(false)
const error = ref('')
const saving = ref(false)
const form = ref({ userId: '', username: '', role: 'viewer' as InstanceMember['role'] })

const ROLE_TEXT = {
  viewer: '只读成员',
  operator: '运维者',
  admin: '服务管理员',
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    items.value = (await api.listMembers(props.instanceId)).items
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}

async function add(): Promise<void> {
  const userId = form.value.userId.trim()
  if (!userId) {
    error.value = '请填写飞牛用户 ID'
    return
  }
  saving.value = true
  error.value = ''
  try {
    await api.upsertMember(props.instanceId, userId, form.value.username.trim() || userId, form.value.role)
    form.value = { userId: '', username: '', role: 'viewer' }
    await load()
  } catch (e) {
    error.value = errText(e)
  } finally {
    saving.value = false
  }
}

async function remove(member: InstanceMember): Promise<void> {
  try {
    await ElMessageBox.confirm(`移除 ${member.username} 后，对方将无法再访问这个数据库服务。`, '移除成员', {
      type: 'warning',
      confirmButtonText: '移除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await api.deleteMember(props.instanceId, member.userId)
    ElMessage.success('成员已移除')
    await load()
  } catch (e) {
    error.value = errText(e)
  }
}

onMounted(load)
</script>

<template>
  <div class="members" v-loading="loading">
    <el-alert v-if="error" type="error" :closable="false" show-icon :title="error" />

    <section class="card">
      <div class="head">
        <div>
          <h2>服务成员</h2>
          <p class="tip">成员使用飞牛账号登录。只读成员只能查看，运维者可启停、建库、建账号和备份，服务管理员还能修改或删除服务。</p>
        </div>
        <span class="count">{{ items.length }} 人</span>
      </div>

      <div v-if="items.length" class="list">
        <div v-for="member in items" :key="member.userId" class="row-item">
          <span class="avatar">{{ (member.username || 'U').slice(0, 1).toUpperCase() }}</span>
          <span class="who">
            <strong>{{ member.username }}</strong>
            <span class="mono muted">UID {{ member.userId }}</span>
          </span>
          <span class="pill pill-accent">{{ ROLE_TEXT[member.role] }}</span>
          <span class="muted small">加入于 {{ formatTime(member.createdAt) }}</span>
          <el-button v-if="canManage" link type="danger" :icon="Delete" @click="remove(member)">移除</el-button>
        </div>
      </div>
      <p v-else-if="!loading" class="muted">还没有单独授权成员。NAS 管理员默认可以管理全部服务。</p>
    </section>

    <section v-if="canManage" class="card add-card">
      <h2>添加成员</h2>
      <div class="form-grid">
        <div class="field">
          <label for="member-uid">飞牛用户 ID</label>
          <input id="member-uid" v-model="form.userId" class="input mono" placeholder="例如 1001" />
        </div>
        <div class="field">
          <label for="member-name">显示名称</label>
          <input id="member-name" v-model="form.username" class="input" placeholder="例如 alice" />
        </div>
        <div class="field">
          <label for="member-role">角色</label>
          <select id="member-role" v-model="form.role" class="select">
            <option value="viewer">只读成员</option>
            <option value="operator">运维者</option>
            <option value="admin">服务管理员</option>
          </select>
        </div>
      </div>
      <button type="button" class="btn btn-primary" :disabled="saving" @click="add">
        <el-icon><Plus /></el-icon>{{ saving ? '保存中…' : '添加或更新成员' }}
      </button>
    </section>
  </div>
</template>

<style scoped>
.members {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
}

.head h2,
.add-card h2 {
  margin: 0 0 6px;
  font-size: 15px;
}

.tip {
  max-width: 760px;
  margin: 0;
  color: var(--fg-muted);
  font-size: 13px;
  line-height: 1.7;
}

.count {
  color: var(--fg-muted);
  font-size: 13px;
}

.list {
  display: flex;
  flex-direction: column;
  margin-top: 12px;
}

.row-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 11px 0;
  border-top: 1px solid var(--line);
}

.avatar {
  display: grid;
  place-items: center;
  flex: none;
  width: 30px;
  height: 30px;
  border-radius: 8px;
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
}

.who {
  display: flex;
  flex-direction: column;
  min-width: 120px;
}

.row-item .small {
  margin-left: auto;
}

.form-grid {
  display: grid;
  grid-template-columns: 1fr 1fr 180px;
  gap: 10px;
}

.small {
  font-size: 12px;
}

@media (max-width: 720px) {
  .row-item {
    flex-wrap: wrap;
  }

  .row-item .small {
    width: 100%;
    margin-left: 40px;
  }

  .form-grid {
    grid-template-columns: 1fr;
  }
}
</style>
