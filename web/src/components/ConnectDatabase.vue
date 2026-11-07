<script setup lang="ts">
import { computed, ref } from 'vue'
import { Connection, Lock, View, Hide } from '@element-plus/icons-vue'
import Modal from './Modal.vue'
import { api, type ConnectInstanceInput, type Instance } from '../api'
import { errText } from '../format'
import type { Engine } from '../engine'

const props = defineProps<{ initialEngine?: Engine }>()
const emit = defineEmits<{ close: []; connected: [instance: Instance] }>()

const engine = ref<'mysql' | 'postgres' | 'mongodb'>(
  props.initialEngine === 'postgresql' ? 'postgres' : props.initialEngine === 'mongodb' ? 'mongodb' : 'mysql',
)
const form = ref({
  name: '',
  host: '127.0.0.1',
  port: 3306,
  username: '',
  password: '',
  version: '',
  tlsMode: 'disable' as 'disable' | 'require',
})
const showPassword = ref(false)
const busy = ref(false)
const error = ref('')

const portPlaceholder = computed(() => ({ postgres: 5432, mongodb: 27017, mysql: 3306 })[engine.value])

function chooseEngine(next: 'mysql' | 'postgres' | 'mongodb'): void {
  if (engine.value === next) return
  const wasDefault = form.value.port === portPlaceholder.value
  engine.value = next
  if (wasDefault) form.value.port = { postgres: 5432, mongodb: 27017, mysql: 3306 }[next]
  error.value = ''
}

function validate(): string {
  if (!form.value.name.trim()) return '请填写服务名称'
  if (!form.value.host.trim()) return '请填写数据库地址'
  if (!Number.isInteger(Number(form.value.port)) || Number(form.value.port) < 1 || Number(form.value.port) > 65535) {
    return '端口必须在 1-65535 之间'
  }
  if (!form.value.username.trim()) return '请填写数据库账号'
  if (!form.value.password) return '请填写数据库密码'
  return ''
}

async function submit(): Promise<void> {
  if (busy.value) return
  const issue = validate()
  if (issue) {
    error.value = issue
    return
  }
  busy.value = true
  error.value = ''
  const input: ConnectInstanceInput = {
    name: form.value.name.trim(),
    engine: engine.value,
    engineVersion: form.value.version.trim() || undefined,
    host: form.value.host.trim(),
    port: Number(form.value.port),
    username: form.value.username.trim(),
    password: form.value.password,
    tlsMode: form.value.tlsMode,
  }
  try {
    const instance = await api.connectInstance(input)
    emit('connected', instance)
  } catch (e) {
    error.value = errText(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal title="连接已有数据库" :busy="busy" @close="emit('close')">
    <div class="engine-switch">
      <button
        v-for="item in [{ key: 'mysql', label: 'MySQL' }, { key: 'postgres', label: 'PostgreSQL' }, { key: 'mongodb', label: 'MongoDB' }] as const"
        :key="item.key"
        type="button"
        :class="{ active: engine === item.key }"
        @click="chooseEngine(item.key)"
      >
        {{ item.label }}
      </button>
    </div>

    <p class="intro">
      只读取和管理这台数据库，不会启动、停止或改动它所在的主机。连接通过后才会保存凭据。
    </p>

    <div class="grid">
      <div class="field span-2">
        <label for="connect-name">服务名称</label>
        <input id="connect-name" v-model="form.name" class="input" maxlength="32" placeholder="例如：生产 MySQL、家庭 NAS 数据库" />
      </div>
      <div class="field">
        <label for="connect-host">地址</label>
        <input id="connect-host" v-model="form.host" class="input mono" spellcheck="false" placeholder="192.168.1.20" />
      </div>
      <div class="field">
        <label for="connect-port">端口</label>
        <input
          id="connect-port"
          v-model.number="form.port"
          class="input mono"
          inputmode="numeric"
          :placeholder="String(portPlaceholder)"
        />
      </div>
      <div class="field">
        <label for="connect-user">管理账号</label>
        <input id="connect-user" v-model="form.username" class="input mono" autocomplete="username" :placeholder="engine === 'postgres' ? 'postgres' : 'root'" />
      </div>
      <div class="field">
        <label for="connect-version">版本（可选）</label>
        <input id="connect-version" v-model="form.version" class="input mono" placeholder="例如 8.4 / 17" />
      </div>
      <div class="field span-2">
        <label for="connect-password">密码</label>
        <div class="password-row">
          <input
            id="connect-password"
            v-model="form.password"
            class="input mono"
            :type="showPassword ? 'text' : 'password'"
            autocomplete="current-password"
            placeholder="数据库账号密码"
          />
          <button type="button" class="btn btn-sm" :title="showPassword ? '隐藏密码' : '显示密码'" @click="showPassword = !showPassword">
            <el-icon><Hide v-if="showPassword" /><View v-else /></el-icon>
          </button>
        </div>
      </div>
      <div class="field span-2">
        <label>TLS</label>
        <div class="tls-choice">
          <label>
            <input v-model="form.tlsMode" type="radio" value="disable" />
            <span><el-icon><Lock /></el-icon>不加密，适合本机或可信局域网</span>
          </label>
          <label>
            <input v-model="form.tlsMode" type="radio" value="require" />
            <span><el-icon><Connection /></el-icon>加密连接，暂不校验证书</span>
          </label>
        </div>
      </div>
    </div>

    <p v-if="error" class="notice notice-bad">{{ error }}</p>

    <template #foot>
      <button type="button" class="btn" :disabled="busy" @click="emit('close')">取消</button>
      <button type="button" class="btn btn-primary" :disabled="busy" @click="submit">
        {{ busy ? '正在测试连接…' : '测试并连接' }}
      </button>
    </template>
  </Modal>
</template>

<style scoped>
.engine-switch {
  display: flex;
  gap: 4px;
  padding: 4px;
  margin-bottom: 12px;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--bg);
}

.engine-switch button {
  flex: 1;
  padding: 7px 10px;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--fg-muted);
}

.engine-switch button.active {
  background: var(--bg-elevated);
  color: var(--fg);
  font-weight: 600;
  box-shadow: 0 1px 2px rgb(0 0 0 / 8%);
}

.intro {
  margin: 0 0 14px;
  color: var(--fg-muted);
  font-size: 13px;
  line-height: 1.65;
}

.grid {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
  gap: 0 12px;
}

.span-2 {
  grid-column: 1 / -1;
}

.password-row {
  display: flex;
  gap: 8px;
}

.password-row .input {
  flex: 1;
}

.tls-choice {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.tls-choice label {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 11px;
  border: 1px solid var(--line);
  border-radius: 8px;
  font-size: 13px;
}

.tls-choice span {
  display: inline-flex;
  align-items: center;
  gap: 5px;
}

@media (max-width: 720px) {
  .grid {
    grid-template-columns: 1fr;
  }

  .span-2 {
    grid-column: auto;
  }
}
</style>
