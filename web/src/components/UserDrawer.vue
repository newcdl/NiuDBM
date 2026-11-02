<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search, View, Hide, CopyDocument } from '@element-plus/icons-vue'
import { api, type DatabaseItem, type DbUser, type RoleGrant } from '../api'
import { errText } from '../format'
import { copyText, randomPassword } from '../clip'
import Pager from './Pager.vue'
import { usePager } from '../pager'
import {
  HOST_KIND_TEXT,
  displayHost,
  hostHint,
  parseHost,
  ruleOfHost,
  uniqueHosts,
  type HostRule,
} from '../hostrule'

const props = defineProps<{ instanceId: string; modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; changed: [] }>()

const LEVEL_TEXT: Record<string, string> = {
  none: '无权限',
  read: '只读',
  write: '读写',
  all: '全权',
}

// 访问来源：所有人（%）还是只允许指定 IP
const HOST_ALL = 'all'
const HOST_IP = 'ip'

const users = ref<DbUser[]>([])
const databases = ref<DatabaseItem[]>([])
const loading = ref(false)
const error = ref('')
const keyword = ref('')
const expanded = ref<Record<string, boolean>>({})

const editing = ref<DbUser | null>(null)
const creating = ref(false)
// 新增与编辑都在弹窗里做：抽屉留给列表，表单不再挤在列表上方
const formOpen = ref(false)
const form = ref<{ name: string; password: string; hostMode: string; hosts: HostRule[]; grants: RoleGrant[] }>({
  name: '',
  password: '',
  hostMode: HOST_ALL,
  hosts: [],
  grants: [],
})
// 来源逐条添加：输入框 + 标签列表，比让人自己记逗号分隔好核对
const hostInput = ref('')
const hostError = ref('')
const formError = ref('')
const busy = ref(false)
const copied = ref('')
const pick = ref<{ database: string; level: string }>({ database: '', level: 'read' })

// 密码回显：MySQL 里只有密文，回显的是建号时留在本机的那一份
const revealed = ref<Record<string, string>>({})
const revealing = ref('')

const open = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit('update:modelValue', v),
})

const filtered = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return users.value
  return users.value.filter((u) => {
    if (u.name.toLowerCase().includes(kw)) return true
    if ((u.hosts ?? []).some((h) => h.toLowerCase().includes(kw))) return true
    return (u.grants ?? []).some((g) => g.database.toLowerCase().includes(kw))
  })
})

// 账号多了以后抽屉里要滚很久，按页看更稳
const { page: pageNo, pageSize, total: pageTotal, items: pagedUsers } = usePager(filtered, 10)

const freeDatabases = computed(() =>
  databases.value.filter((d) => !form.value.grants.some((g) => g.database === d.name)),
)

function grantsOf(user: DbUser): RoleGrant[] {
  return user.grants ?? []
}

function hostsOf(user: DbUser): string[] {
  return user.hosts && user.hosts.length ? user.hosts : ['%']
}

function hostLabel(user: DbUser): string {
  const hosts = hostsOf(user)
  if (hosts.length === 1) return `${user.name}@${displayHost(hosts[0])}`
  return `${user.name}@${hosts.length} 个来源`
}

/** 多个来源时的明细：库里存的是点分掩码，展示还原成 /24 */
function hostsText(user: DbUser): string {
  return hostsOf(user).map(displayHost).join('、')
}

function locked(user: DbUser): boolean {
  return user.super || user.system
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    const [u, d] = await Promise.all([
      api.listUsers(props.instanceId),
      api.listDatabases(props.instanceId).catch(() => ({ items: [], total: 0 })),
    ])
    users.value = u.items
    databases.value = d.items
    revealed.value = {}
  } catch (e) {
    error.value = errText(e)
    users.value = []
  } finally {
    loading.value = false
  }
}

function openCreate(): void {
  creating.value = true
  editing.value = null
  form.value = { name: '', password: randomPassword(), hostMode: HOST_ALL, hosts: [], grants: [] }
  pick.value = { database: freeDatabases.value[0]?.name ?? '', level: 'read' }
  formError.value = ''
  hostInput.value = ''
  hostError.value = ''
  copied.value = ''
  formOpen.value = true
}

function openEdit(user: DbUser): void {
  creating.value = false
  editing.value = user
  const hosts = hostsOf(user)
  const all = hosts.includes('%')
  form.value = {
    name: user.name,
    password: '',
    hostMode: all ? HOST_ALL : HOST_IP,
    hosts: all ? [] : uniqueHosts(hosts.filter((h) => h !== '%').map(ruleOfHost).filter((r): r is HostRule => !!r)),
    grants: grantsOf(user).map((g) => ({ ...g })),
  }
  pick.value = { database: '', level: 'read' }
  formError.value = ''
  hostInput.value = ''
  hostError.value = ''
  copied.value = ''
  formOpen.value = true
}

function closeForm(): void {
  formOpen.value = false
  creating.value = false
  editing.value = null
  formError.value = ''
  hostInput.value = ''
  hostError.value = ''
}

/** 回车或点「添加」：解析一条来源，成功进标签、失败给贴合的错误 */
function addHost(): void {
  const text = hostInput.value.trim()
  if (!text) return
  const rule = parseHost(text)
  if (!rule) {
    hostError.value = hostHint(text)
    return
  }
  if (form.value.hosts.some((h) => h.value === rule.value)) {
    hostError.value = `${rule.label} 已经在列表里了`
    return
  }
  form.value.hosts = [...form.value.hosts, rule]
  hostInput.value = ''
  hostError.value = ''
}

function removeHost(value: string): void {
  form.value.hosts = form.value.hosts.filter((h) => h.value !== value)
  hostError.value = ''
}

/** 表单里的来源 → 接口要的数组 */
function formHosts(): string[] {
  if (form.value.hostMode === HOST_ALL) return ['%']
  return form.value.hosts.map((h) => h.value)
}

function addGrant(): void {
  const db = pick.value.database
  if (!db || form.value.grants.some((g) => g.database === db)) return
  form.value.grants.push({ database: db, level: pick.value.level })
  pick.value.database = freeDatabases.value[0]?.name ?? ''
}

function removeGrant(db: string): void {
  form.value.grants = form.value.grants.filter((g) => g.database !== db)
  if (!pick.value.database) pick.value.database = freeDatabases.value[0]?.name ?? ''
}

async function copyPwd(): Promise<void> {
  if (!form.value.password) return
  copied.value = (await copyText(form.value.password))
    ? '已复制，请立刻保存到别处'
    : '浏览器不允许复制，请手动选中复制'
}

async function revealPassword(user: DbUser): Promise<void> {
  if (revealed.value[user.name]) {
    const next = { ...revealed.value }
    delete next[user.name]
    revealed.value = next
    return
  }
  revealing.value = user.name
  try {
    const res = await api.revealUserPassword(props.instanceId, user.name)
    revealed.value = { ...revealed.value, [user.name]: res.password }
  } catch (e) {
    ElMessage.warning(errText(e))
  } finally {
    revealing.value = ''
  }
}

async function copyUserPassword(user: DbUser): Promise<void> {
  let pwd = revealed.value[user.name]
  if (!pwd) {
    try {
      pwd = (await api.revealUserPassword(props.instanceId, user.name)).password
      revealed.value = { ...revealed.value, [user.name]: pwd }
    } catch (e) {
      ElMessage.warning(errText(e))
      return
    }
  }
  ElMessage.success((await copyText(pwd)) ? '密码已复制' : '浏览器不允许复制')
}

async function submit(): Promise<void> {
  if (busy.value) return
  const name = form.value.name.trim()
  if (creating.value) {
    if (!name) {
      formError.value = '请填写账号名'
      return
    }
    if (form.value.password.length < 8) {
      formError.value = '密码至少 8 位'
      return
    }
  } else if (form.value.password && form.value.password.length < 8) {
    formError.value = '密码至少 8 位'
    return
  }
  if (form.value.hostMode === HOST_IP && !form.value.hosts.length) {
    formError.value = '指定地址时至少要添加一个 IP 或网段'
    return
  }
  if (form.value.hostMode === HOST_IP && hostInput.value.trim()) {
    formError.value = '输入框里还有没添加的地址，先点「添加」'
    return
  }
  const hosts = formHosts()

  busy.value = true
  formError.value = ''
  try {
    if (creating.value) {
      await api.createUser(props.instanceId, {
        name,
        password: form.value.password,
        hosts,
        grants: form.value.grants,
      })
      ElMessage.success(`账号 ${name} 已创建`)
    } else if (editing.value) {
      await api.updateUser(props.instanceId, editing.value.name, {
        password: form.value.password || undefined,
        hosts,
        grants: form.value.grants,
      })
      ElMessage.success(`账号 ${editing.value.name} 已更新`)
    }
    closeForm()
    await load()
    emit('changed')
  } catch (e) {
    formError.value = errText(e)
  } finally {
    busy.value = false
  }
}

async function remove(user: DbUser): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `删除账号 ${user.name} 后，用它连接的应用会立刻认证失败。`,
      '删除账号',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await api.deleteUser(props.instanceId, user.name)
    ElMessage.success(`账号 ${user.name} 已删除`)
    await load()
    emit('changed')
  } catch (e) {
    ElMessage.error(errText(e))
  }
}

function toggle(name: string): void {
  expanded.value = { ...expanded.value, [name]: !expanded.value[name] }
}

watch(
  () => props.modelValue,
  (v) => {
    if (v) void load()
  },
)
watch(
  () => props.instanceId,
  () => {
    if (props.modelValue) void load()
  },
)
</script>

<template>
  <el-drawer v-model="open" title="用户" direction="rtl" size="min(720px, 94vw)" :z-index="2600">
    <div class="drawer">
      <div class="tools">
        <el-input v-model="keyword" :prefix-icon="Search" placeholder="搜索账号名、IP 或库名" clearable />
        <el-button type="primary" @click="openCreate">新建用户</el-button>
      </div>

      <el-alert v-if="error" type="error" :closable="false" show-icon :title="error" />
      <el-table v-loading="loading" :data="pagedUsers" row-key="name">
        <el-table-column label="用户" min-width="150">
          <template #default="{ row }">
            <div class="user-cell">
              <span class="mono">{{ hostLabel(row) }}</span>
              <span v-if="locked(row)" class="hint">系统账号</span>
              <span v-else-if="hostsOf(row).length > 1" class="hint mono">{{ hostsText(row) }}</span>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="密码" width="200">
          <template #default="{ row }">
            <div class="pwd-cell">
              <span
                class="pwd-text mono"
                :class="{ plain: !!revealed[row.name] }"
                :title="revealed[row.name]"
              >
                {{ revealed[row.name] ?? '••••••••' }}
              </span>
              <div class="pwd-ops">
                <el-button
                  link
                  :icon="revealed[row.name] ? Hide : View"
                  :loading="revealing === row.name"
                  title="查看密码"
                  @click="revealPassword(row)"
                />
                <el-button link :icon="CopyDocument" title="复制密码" @click="copyUserPassword(row)" />
              </div>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="数据库" min-width="170">
          <template #default="{ row }">
            <div class="db-cell">
              <template v-if="grantsOf(row).length">
                <el-tag
                  v-for="g in grantsOf(row).slice(0, expanded[row.name] ? undefined : 1)"
                  :key="g.database"
                  size="small"
                >
                  {{ g.database }}
                </el-tag>
                <el-button
                  v-if="grantsOf(row).length > 1"
                  link
                  type="primary"
                  size="small"
                  @click="toggle(row.name)"
                >
                  {{ expanded[row.name] ? '收起' : `+${grantsOf(row).length - 1}` }}
                </el-button>
              </template>
              <span v-else class="hint">未授权</span>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="操作" width="112" align="right">
          <template #default="{ row }">
            <el-button link type="primary" size="small" :disabled="locked(row)" @click="openEdit(row)">
              编辑
            </el-button>
            <el-button link type="danger" size="small" :disabled="locked(row)" @click="remove(row)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <Pager v-model:page="pageNo" v-model:page-size="pageSize" :total="pageTotal" />

      <el-empty v-if="!loading && !filtered.length" :image-size="64" description="没有匹配的用户" />
      <p class="foot-hint">
        密码存在 MySQL 里是密文；这里能查看/复制的是建号或重设时留在本机的那一份。
        用外部工具改过密码的账号查不到，只能在这里重设。
      </p>
    </div>
  </el-drawer>

  <el-dialog
    v-model="formOpen"
    :title="creating ? '新建用户' : `编辑 ${editing?.name ?? ''}`"
    width="min(720px, 94vw)"
    :z-index="2700"
    :close-on-click-modal="false"
    append-to-body
    @closed="closeForm"
  >
    <div class="form-body">
      <p class="hint form-tip">
        账号名 + 访问来源合起来才是一条 MySQL 账号；加一个来源就是多一条账号，去掉就是删掉那条。
      </p>

      <div class="form-grid">
        <label class="field">
          <span>用户名</span>
          <el-input v-model="form.name" :disabled="!creating" maxlength="32" placeholder="例如 blog_app" />
          <em class="hint">建好后不能改名，要换名字只能新建一个</em>
        </label>

        <label class="field">
          <span>密码{{ creating ? '' : '（留空表示不改）' }}</span>
          <div class="row tight">
            <el-input v-model="form.password" placeholder="至少 8 位" show-password />
            <el-button @click="form.password = randomPassword()">随机</el-button>
            <el-button :disabled="!form.password" @click="copyPwd">复制</el-button>
          </div>
          <em v-if="copied" class="hint">{{ copied }}</em>
          <em v-else class="hint">这里能查看/复制的是本机存的那一份，只对新建或重设的密码有效</em>
        </label>
      </div>

      <div class="field">
        <span>谁能连（访问来源）</span>
        <el-radio-group v-model="form.hostMode" class="host-mode">
          <el-radio-button :value="HOST_ALL">所有人</el-radio-button>
          <el-radio-button :value="HOST_IP">指定地址</el-radio-button>
        </el-radio-group>

        <template v-if="form.hostMode === HOST_IP">
          <div class="row tight host-add">
            <el-input
              v-model="hostInput"
              class="mono"
              placeholder="172.16.10.111 或 172.16.10.0/24"
              @keyup.enter="addHost"
            />
            <el-button type="primary" plain :disabled="!hostInput.trim()" @click="addHost">添加</el-button>
          </div>
          <em v-if="hostError" class="hint hint-bad">{{ hostError }}</em>
          <div class="host-chips">
            <el-tag
              v-for="r in form.hosts"
              :key="r.value"
              closable
              size="large"
              @close="removeHost(r.value)"
            >
              <span class="mono">{{ r.label }}</span>
              <em class="kind">{{ HOST_KIND_TEXT[r.kind] }}</em>
            </el-tag>
            <span v-if="!form.hosts.length" class="hint">还没有添加，至少加一个 IP 或网段</span>
          </div>
          <em class="hint">
            单个 IP 只有那一台机器能连；网段（如 <span class="mono">172.16.10.0/24</span>）整个网段都能连。
            回车也能添加。
          </em>
        </template>
        <em v-else class="hint">任何地址都能连（MySQL 里存成 <span class="mono">%</span>），适合内网单机或容器互访。</em>
      </div>

      <div class="field">
        <span>能用哪些库（授权）</span>
        <div class="grants">
          <el-tag
            v-for="g in form.grants"
            :key="g.database"
            closable
            size="large"
            @close="removeGrant(g.database)"
          >
            {{ g.database }}
            <em class="kind">{{ LEVEL_TEXT[g.level] ?? g.level }}</em>
          </el-tag>
          <span v-if="!form.grants.length" class="hint">还没有授权任何库，账号可以连上但看不到数据</span>
        </div>
        <div class="row tight add-grant">
          <el-select v-model="pick.database" placeholder="选择库" :disabled="!freeDatabases.length" class="db-select">
            <el-option v-for="d in freeDatabases" :key="d.name" :label="d.name" :value="d.name" />
          </el-select>
          <el-select v-model="pick.level" class="lv-select">
            <el-option v-for="(text, key) in LEVEL_TEXT" :key="key" :label="text" :value="key" />
          </el-select>
          <el-button :disabled="!pick.database" @click="addGrant">添加</el-button>
        </div>
        <em v-if="!databases.length" class="hint">这个服务里还没有库，先去「数据库与账号」建一个</em>
      </div>

      <el-alert v-if="formError" type="error" :closable="false" show-icon :title="formError" />
    </div>

    <template #footer>
      <el-button :disabled="busy" @click="closeForm">取消</el-button>
      <el-button type="primary" :loading="busy" @click="submit">
        {{ creating ? '创建' : '保存' }}
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.drawer {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.tools {
  display: flex;
  gap: 8px;
}

/* 弹窗内容：弹窗本身已有边框与内边距，这里只管间距 */
.form-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.form-tip {
  margin: 0;
  line-height: 1.6;
}

.form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
  font-size: 13.5px;
}

.field > span {
  color: var(--fg);
  font-weight: 500;
}

.row {
  display: flex;
  gap: 6px;
  align-items: center;
}

.row.tight {
  flex-wrap: nowrap;
  gap: 8px;
}

.row.tight > .el-input {
  flex: 1;
  min-width: 0;
}

.host-mode {
  align-self: flex-start;
}

.host-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  align-items: center;
  min-height: 34px;
}

/* 标签里的类型徽标：IP / 网段 / 所有人，比只给一串数字好认 */
.kind {
  margin-left: 6px;
  color: var(--fg-muted);
  font-size: 11.5px;
  font-style: normal;
  font-weight: 400;
}

.hint-bad {
  color: var(--el-color-danger);
}

.grants {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.add-grant .db-select {
  width: 150px;
}

.add-grant .lv-select {
  width: 100px;
}

.user-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

/* 密码列：文本可以收缩/折行，操作按钮固定不被挤走 */
.pwd-cell {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.pwd-text {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 明文密码多为 20 位，不断行就一定会把后面的按钮顶出去 */
.pwd-text.plain {
  line-height: 1.35;
  white-space: normal;
  word-break: break-all;
}

.pwd-ops {
  display: flex;
  flex: none;
  align-items: center;
  gap: 2px;
}

.pwd-ops :deep(.el-button + .el-button) {
  margin-left: 0;
}

.db-cell {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  align-items: center;
}

.hint {
  color: var(--fg-muted);
  font-size: 12.5px;
  font-style: normal;
}

.foot-hint {
  margin: 0;
  color: var(--fg-muted);
  font-size: 12.5px;
  line-height: 1.6;
}

/* 窄屏：两列字段叠成一列，输入行允许换行，别把输入框挤成一条缝 */
@media (max-width: 760px) {
  .form-body {
    gap: 14px;
  }

  .form-grid {
    grid-template-columns: minmax(0, 1fr);
    gap: 12px;
  }

  .row.tight {
    flex-wrap: wrap;
  }

  .add-grant .db-select,
  .add-grant .lv-select {
    width: 100%;
  }
}
</style>
