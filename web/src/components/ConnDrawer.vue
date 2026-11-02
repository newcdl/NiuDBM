<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { CopyDocument, View, Hide } from '@element-plus/icons-vue'
import { api, type ConnectionInfo } from '../api'
import { errText } from '../format'
import { copyText, randomPassword } from '../clip'

const props = defineProps<{ instanceId: string; modelValue: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: boolean]; changed: [] }>()

const conn = ref<ConnectionInfo | null>(null)
const remote = ref(false)
// 停机时后端不返回远程状态（409），单独记下来：否则开关会显示成"已关闭"，
// 实际上可能是开着的，用户照着去连又被拒，排查方向就歪了。
const remoteError = ref('')
const remoteLoading = ref(false)
const loading = ref(false)
const error = ref('')

const showPwd = ref(false)
const draftPwd = ref('')
const savingPwd = ref(false)

const open = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit('update:modelValue', v),
})

const extUrl = computed(() => {
  const c = conn.value
  if (!c) return ''
  return `mysql://root:${encodeURIComponent(c.password)}@${c.host}:${c.port}/`
})

const innerUrl = computed(() => {
  const c = conn.value
  if (!c) return ''
  return `mysql://root:${encodeURIComponent(c.password)}@${c.containerHost ?? ''}:${c.containerPort ?? 3306}/`
})

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  remoteError.value = ''
  try {
    const c = await api.connection(props.instanceId)
    conn.value = c
    draftPwd.value = c.password
    showPwd.value = false
    try {
      const access = await api.getRootAccess(props.instanceId)
      remote.value = access.remote
    } catch (e) {
      remoteError.value = errText(e)
    }
  } catch (e) {
    error.value = errText(e)
    conn.value = null
  } finally {
    loading.value = false
  }
}

async function copy(text: string, what: string): Promise<void> {
  ElMessage.success((await copyText(text)) ? `${what}已复制` : '浏览器不允许复制，请手动选中')
}

/** 关闭远程访问会让外部连不上 root，属于"会把人挡在门外"的操作，必须二次确认 */
async function toggleRemote(next: boolean): Promise<void> {
  if (remoteLoading.value) return
  if (!next) {
    try {
      await ElMessageBox.confirm(
        '关闭后只有容器内部能连 root，外部客户端要用业务账号连接。确定关闭？',
        '关闭 root 远程访问',
        { type: 'warning', confirmButtonText: '关闭', cancelButtonText: '取消' },
      )
    } catch {
      return
    }
  }
  remoteLoading.value = true
  try {
    const res = await api.setRootAccess(props.instanceId, next)
    remote.value = res.remote
    ElMessage.success(res.message)
    emit('changed')
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    remoteLoading.value = false
  }
}

function randomPwd(): void {
  draftPwd.value = randomPassword(20)
  showPwd.value = true
}

async function savePassword(): Promise<void> {
  if (savingPwd.value) return
  if (draftPwd.value.length < 8) {
    ElMessage.warning('密码至少 8 位')
    return
  }
  try {
    await ElMessageBox.confirm(
      '改完 root 密码后，正在用旧密码连接的应用会立刻断开。',
      '重设 root 密码',
      { type: 'warning', confirmButtonText: '确认修改', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  savingPwd.value = true
  try {
    await api.setRootPassword(props.instanceId, draftPwd.value)
    ElMessage.success('root 密码已更新')
    await load()
    emit('changed')
  } catch (e) {
    ElMessage.error(errText(e))
  } finally {
    savingPwd.value = false
  }
}

watch(
  () => props.modelValue,
  (v) => {
    if (v) void load()
  },
)
</script>

<template>
  <el-drawer v-model="open" title="连接信息" direction="rtl" size="min(560px, 94vw)" :z-index="2600">
    <div v-loading="loading" class="drawer">
      <el-alert v-if="error" type="error" :closable="false" show-icon :title="error" />

      <template v-if="conn">
        <section class="block">
          <header class="block-head">
            <h3>容器连接</h3>
            <el-button link :icon="CopyDocument" @click="copy(innerUrl, '容器连接 URL')">
              复制连接 URL
            </el-button>
          </header>
          <div class="rows">
            <div class="row-item">
              <span class="k">地址</span>
              <span class="v mono">{{ conn.containerHost }}</span>
              <el-button link :icon="CopyDocument" @click="copy(conn.containerHost ?? '', '地址')" />
            </div>
            <div class="row-item">
              <span class="k">端口</span>
              <span class="v mono">{{ conn.containerPort ?? 3306 }}</span>
              <el-button link :icon="CopyDocument" @click="copy(String(conn.containerPort ?? 3306), '端口')" />
            </div>
          </div>
          <p class="hint">同一 Docker 网络里的应用（PHP 运行环境、其他容器）用这个地址，不用绕到宿主机。</p>
        </section>

        <section class="block">
          <header class="block-head">
            <h3>外部连接</h3>
            <el-button link :icon="CopyDocument" @click="copy(extUrl, '外部连接 URL')">
              复制连接 URL
            </el-button>
          </header>
          <div class="rows">
            <div class="row-item">
              <span class="k">地址</span>
              <span class="v mono">{{ conn.host }}</span>
              <el-button link :icon="CopyDocument" @click="copy(conn.host, '地址')" />
            </div>
            <div class="row-item">
              <span class="k">端口</span>
              <span class="v mono">{{ conn.port }}</span>
              <el-button link :icon="CopyDocument" @click="copy(String(conn.port), '端口')" />
            </div>
            <div class="row-item">
              <span class="k">用户</span>
              <span class="v mono">{{ conn.user }}</span>
              <el-button link :icon="CopyDocument" @click="copy(conn.user, '用户名')" />
            </div>
          </div>
          <p class="hint">
            {{ conn.running ? '局域网里的客户端（DBeaver / Navicat / 手机）用这个地址。' : '服务当前未运行，端口暂时连不上。' }}
          </p>
        </section>

        <section class="block">
          <header class="block-head">
            <h3>远程访问</h3>
            <el-switch
              :model-value="remote"
              :loading="remoteLoading"
              :disabled="!!remoteError"
              @update:model-value="toggleRemote"
            />
          </header>
          <el-alert v-if="remoteError" type="warning" :closable="false" show-icon :title="remoteError" />
          <p class="hint">
            关闭后只有容器内部能连 root；外部连接请改用「数据库与账号」里建的业务账号。
          </p>
        </section>

        <section class="block">
          <header class="block-head">
            <h3>root 密码</h3>
          </header>
          <div class="pwd-row">
            <el-input
              v-model="draftPwd"
              :type="showPwd ? 'text' : 'password'"
              class="mono"
              aria-label="root 密码"
            />
            <el-button :icon="showPwd ? Hide : View" title="显示/隐藏" @click="showPwd = !showPwd" />
            <el-button :icon="CopyDocument" title="复制" @click="copy(draftPwd, '密码')" />
            <el-button @click="randomPwd">随机</el-button>
          </div>
          <div class="pwd-foot">
            <span class="hint">密码不能包含 &amp; ; $ ' ` \" ( ) &lt; &gt; | 这些字符。</span>
            <el-button
              type="primary"
              size="small"
              :loading="savingPwd"
              :disabled="draftPwd === conn.password"
              @click="savePassword"
            >
              保存密码
            </el-button>
          </div>
        </section>
      </template>
    </div>
  </el-drawer>
</template>

<style scoped>
.drawer {
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.block {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-bottom: 16px;
  border-bottom: 1px dashed var(--line);
}

.block:last-child {
  border-bottom: 0;
  padding-bottom: 0;
}

.block-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.block-head h3 {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
}

.rows {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 8px 12px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg);
}

.row-item {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 34px;
}

.row-item .k {
  width: 44px;
  color: var(--fg-muted);
  font-size: 13.5px;
}

.row-item .v {
  flex: 1;
  font-size: 14px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.pwd-row {
  display: flex;
  gap: 6px;
  align-items: center;
}

.pwd-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.hint {
  margin: 0;
  color: var(--fg-muted);
  font-size: 12.5px;
  line-height: 1.6;
}
</style>
