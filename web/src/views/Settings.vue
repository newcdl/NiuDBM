<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Promotion, Refresh } from '@element-plus/icons-vue'
import { api, type AppSettings } from '../api'
import { errText } from '../format'

const tab = ref('backup')

const settings = ref<AppSettings | null>(null)
const loading = ref(false)
const error = ref('')
const saving = ref(false)

const notifyResult = ref<{ ok: boolean; message: string } | null>(null)
const testing = ref(false)

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    settings.value = await api.getSettings()
  } catch (e) {
    error.value = errText(e)
  } finally {
    loading.value = false
  }
}

async function save(): Promise<void> {
  if (!settings.value || saving.value) return
  saving.value = true
  error.value = ''
  try {
    settings.value = await api.updateSettings(settings.value)
    ElMessage.success('设置已保存')
  } catch (e) {
    error.value = errText(e)
  } finally {
    saving.value = false
  }
}

async function sendTest(): Promise<void> {
  if (testing.value) return
  testing.value = true
  notifyResult.value = null
  try {
    notifyResult.value = await api.testNotify()
  } catch (e) {
    notifyResult.value = { ok: false, message: errText(e) }
  } finally {
    testing.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="page">
    <el-alert v-if="error" type="error" :closable="false" show-icon :title="error">
      <template #default>
        <el-button size="small" text type="primary" @click="load()">重试</el-button>
      </template>
    </el-alert>

    <el-tabs v-model="tab" v-loading="loading" class="tabs">
      <!-- 备份与存储 -->
      <el-tab-pane label="备份与存储" name="backup">
        <el-alert type="info" :closable="false" show-icon class="hint">
          <template #title>
            这里设置全局默认值。备份目录来自共享目录授权，实例的备份会优先写到那里，写不进时回退到应用私有目录。
          </template>
        </el-alert>

        <div class="card" v-if="settings">
          <el-form label-position="top" class="form">
            <el-form-item label="默认保留份数">
              <el-input-number v-model="settings.backupRetentionCount" :min="1" :max="99" :step="1" />
              <div class="tip">超过这个份数后，最旧的备份会自动清理。</div>
            </el-form-item>

            <el-form-item label="审计日志保留天数">
              <el-input-number v-model="settings.auditRetentionDays" :min="30" :max="730" :step="30" />
              <div class="tip">到期后按时间分批清理，清理动作本身也会留痕。</div>
            </el-form-item>

            <el-form-item label="备份目录">
              <div class="mono dir">{{ settings.backupDir }}</div>
              <div class="tip">目录由共享目录授权决定，应用内不可修改，以免写进没有权限的位置。</div>
            </el-form-item>

            <el-button type="primary" :loading="saving" @click="save">保存</el-button>
          </el-form>
        </div>
      </el-tab-pane>

      <!-- 通知 -->
      <el-tab-pane label="通知" name="notify">
        <el-alert type="info" :closable="false" show-icon class="hint">
          <template #title>
            填一个 Webhook 地址就能收到告警：备份失败、实例异常等会推到这里。留空则完全不推送。
          </template>
        </el-alert>

        <div class="card" v-if="settings">
          <el-form label-position="top" class="form">
            <el-form-item label="Webhook 地址">
              <el-input
                v-model="settings.notifyWebhook"
                placeholder="留空则不推送，例如 https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx"
                clearable
              />
            </el-form-item>

            <el-form-item label="推送哪些事件">
              <div class="switches">
                <div class="switch-row">
                  <el-switch v-model="settings.notifyBackupFailed" />
                  <div>
                    <strong>备份失败</strong>
                    <div class="tip">定时备份或手动备份失败时提醒，避免数据裸奔很久才发现。</div>
                  </div>
                </div>
                <div class="switch-row">
                  <el-switch v-model="settings.notifyInstanceDown" />
                  <div>
                    <strong>实例异常</strong>
                    <div class="tip">常驻引擎（PostgreSQL）意外退出时提醒。</div>
                  </div>
                </div>
                <div class="switch-row">
                  <el-switch v-model="settings.notifyBackupSucceeded" />
                  <div>
                    <strong>备份成功</strong>
                    <div class="tip">默认关：成功是常态，只在需要确认备份在跑时打开。</div>
                  </div>
                </div>
              </div>
            </el-form-item>

            <div class="actions">
              <el-button type="primary" :loading="saving" @click="save">保存</el-button>
              <el-button :icon="Promotion" :loading="testing" :disabled="!settings.notifyWebhook" @click="sendTest">
                发送测试通知
              </el-button>
            </div>

            <el-alert
              v-if="notifyResult"
              :type="notifyResult.ok ? 'success' : 'error'"
              :closable="false"
              show-icon
              :title="notifyResult.message"
              class="notify-result"
            />
          </el-form>
        </div>
      </el-tab-pane>

      <!-- 关于 -->
      <el-tab-pane label="关于" name="about">
        <div class="card" v-if="settings">
          <el-descriptions :column="1" border size="small" class="desc">
            <el-descriptions-item label="应用">牛库管家</el-descriptions-item>
            <el-descriptions-item label="版本">v{{ settings.version }}</el-descriptions-item>
          </el-descriptions>

          <p class="tip license">
            本应用负责 MySQL 容器的部署、配置、账号权限、备份与导入导出，不提供表结构浏览与 SQL 控制台。
            数据库以 Docker 官方镜像运行，镜像遵循其自身许可证。
          </p>
        </div>
      </el-tab-pane>
    </el-tabs>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  gap: 12px;
  max-width: 960px;
  margin: 0 auto;
}

.hint {
  margin-bottom: 12px;
}

.card {
  padding: 16px 18px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--bg-elevated);
}

.form {
  max-width: 560px;
}

.tip {
  margin: 4px 0 0;
  color: var(--fg-muted);
  font-size: 13px;
  line-height: 1.6;
}

.dir {
  color: var(--fg);
  word-break: break-all;
}

.switches {
  display: flex;
  flex-direction: column;
  gap: 14px;
  width: 100%;
}

.switch-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.switch-row strong {
  display: block;
  font-size: 13.5px;
  font-weight: 500;
}

.actions {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
}

.actions .el-button + .el-button {
  margin-left: 0;
}

.notify-result {
  margin-top: 4px;
}

.subtitle {
  margin: 18px 0 8px;
  font-size: 13.5px;
  font-weight: 600;
  color: var(--fg-muted);
}

.engines {
  margin-bottom: 12px;
}

.desc {
  margin-bottom: 4px;
}

.license {
  max-width: 560px;
}
</style>
