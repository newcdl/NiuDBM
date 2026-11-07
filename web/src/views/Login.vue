<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { CircleCheck, Lock, Monitor, UserFilled, Warning } from '@element-plus/icons-vue'
import type { AuthState } from '../api'
import { useAuth } from '../auth'
import { assetUrl } from '../assets'

/*
 * 登录页只有一件事可做：用当前打开这个页面的飞牛身份，换一份牛库管家会话。
 *
 * 应用不自建账号，也没有「注册」「忘记密码」——身份与密码都在 NAS 上，
 * 这里多一套只会多一处能泄露的凭据。因此页面上没有输入框，只有一个说明清楚
 * 「你现在是谁」的按钮，和三种情况的处理办法。
 */
const props = defineProps<{ state: AuthState | null; error?: string }>()
const emit = defineEmits<{ (e: 'done'): void }>()

const auth = useAuth()
const busy = ref(false)

const logoUrl = assetUrl('icon.svg')

/** 两种情形：有飞牛身份，或压根没有身份。 */
const mode = computed<'ready' | 'no-identity'>(() => {
  if (props.state?.gateway.available) return 'ready'
  return 'no-identity'
})

const who = computed<string>(() => props.state?.gateway.username || '')

const tip = computed<string>(() => {
  if (props.state?.dev) return '开发模式：没有飞牛网关，直接以 dev 管理员身份进入。'
  if (mode.value === 'no-identity') {
    return (
      props.state?.gateway.reason ||
      '这次请求没有带上飞牛身份。从飞牛桌面点击「牛库管家」图标进入即可。'
    )
  }
  return ''
})

async function submit(): Promise<void> {
  if (mode.value !== 'ready') return
  busy.value = true
  try {
    await auth.login()
    emit('done')
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : '登录失败')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="login-page">
    <div class="login-bg" aria-hidden="true">
      <div class="glow glow-a" />
      <div class="glow glow-b" />
    </div>

    <div class="login-shell">
      <!-- 左：品牌栏（窄屏隐藏，品牌信息挪到右侧顶部） -->
      <aside class="login-brand">
        <div class="brand-head">
          <img :src="logoUrl" alt="牛库管家" class="brand-logo" />
          <div class="brand-name">牛库管家</div>
        </div>

        <div class="brand-slogan">把 MySQL / PostgreSQL 装进 NAS，建库、备份、看状态都在一页里</div>

        <ul class="brand-points">
          <li>
            <span class="point-icon"><el-icon><CircleCheck /></el-icon></span>
            <div>
              <div class="point-title">数据库像应用一样安装</div>
              <div class="point-desc">选引擎与版本、等几分钟，实例直接可用，不用登 SSH</div>
            </div>
          </li>
          <li>
            <span class="point-icon"><el-icon><Lock /></el-icon></span>
            <div>
              <div class="point-title">复用 NAS 账号</div>
              <div class="point-desc">管理员和成员都使用飞牛登录态，应用里不再存一份密码</div>
            </div>
          </li>
          <li>
            <span class="point-icon"><el-icon><Monitor /></el-icon></span>
            <div>
              <div class="point-title">数据留在自己家里</div>
              <div class="point-desc">实例、备份、凭据都在 NAS 上，不上传任何地方</div>
            </div>
          </li>
        </ul>

        <div class="brand-foot">运行在你自己的飞牛 NAS 上</div>
      </aside>

      <!-- 右：登录操作 -->
      <main class="login-main">
        <div class="login-mobile-head">
          <img :src="logoUrl" alt="" class="brand-logo-sm" />
          <div>
            <div class="mobile-name">牛库管家</div>
            <div class="mobile-sub">NAS 数据库管理</div>
          </div>
        </div>

        <div class="login-form">
          <div class="login-title">登录牛库管家</div>
          <div class="login-sub">使用你当前登录飞牛 NAS 的账号进入</div>

          <div v-if="props.error" class="note bad">
            无法连接牛库管家服务：{{ props.error }}。请确认应用正在运行，随后刷新页面。
          </div>

          <template v-else>
            <!-- 身份卡片：让用户确认「我现在以谁的身份登录」 -->
            <div v-if="props.state?.gateway.available" class="identity">
              <el-icon class="identity-icon"><UserFilled /></el-icon>
              <div class="identity-text">
                <div class="identity-name">{{ who }}</div>
              <div class="identity-role">
                {{ props.state?.gateway.isAdmin ? 'NAS 管理员' : '数据库成员' }}
              </div>
              </div>
              <el-icon v-if="mode === 'ready'" class="identity-ok"><CircleCheck /></el-icon>
            </div>

            <div v-if="tip" class="note" :class="{ warn: mode !== 'no-identity' }">
              <el-icon class="note-icon"><Warning /></el-icon>
              <span>{{ tip }}</span>
            </div>

            <el-button
              type="primary"
              size="large"
              class="login-submit"
              :loading="busy"
              :disabled="mode !== 'ready'"
              @click="submit"
            >
              {{ mode === 'ready' ? `以 ${who} 登录` : '无法登录' }}
            </el-button>

            <div class="login-hint">
              <template v-if="mode === 'no-identity'">
                用 IP 或端口直接打开本应用时拿不到飞牛身份，这是飞牛的安全设计，不是功能坏了。
                回到飞牛桌面重新点击图标即可。
              </template>
              <template v-else>
                {{ props.state?.gateway.isAdmin ? '管理员可以管理全部数据库服务。' : '登录后只会显示已经被授权的数据库服务。' }}
                登录状态保留 7 天；换浏览器、清了 Cookie 或主动退出后需要重新点一次。
              </template>
            </div>
          </template>
        </div>
      </main>
    </div>
  </div>
</template>

<style scoped>
.login-page {
  position: relative;
  min-height: 100vh;
  min-height: 100dvh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  overflow: hidden;
  background: var(--bg);
}

/* 两团很淡的柔光，只为打破大片纯色；登录页的第一要务仍是看清文字 */
.glow {
  position: absolute;
  width: 560px;
  height: 560px;
  border-radius: 50%;
  filter: blur(90px);
  opacity: 0.5;
  pointer-events: none;
}
.glow-a {
  top: -180px;
  right: -120px;
  background: rgba(15, 118, 110, 0.22);
}
.glow-b {
  bottom: -200px;
  left: -140px;
  background: rgba(59, 130, 246, 0.12);
}
html.dark .glow-a {
  opacity: 0.28;
}
html.dark .glow-b {
  opacity: 0.18;
}

.login-shell {
  position: relative;
  display: flex;
  width: min(920px, 100%);
  min-height: 540px;
  border-radius: 18px;
  background: var(--bg-elevated);
  border: 1px solid var(--line);
  box-shadow: var(--shadow), 0 18px 48px rgba(16, 24, 40, 0.1);
  overflow: hidden;
}

/* ---- 左：品牌 ---- */
.login-brand {
  flex: 0 0 42%;
  display: flex;
  flex-direction: column;
  padding: 42px 36px;
  color: #e8f2f0;
  background:
    radial-gradient(900px 480px at -10% -20%, rgba(79, 209, 197, 0.22), transparent 60%),
    radial-gradient(700px 420px at 120% 115%, rgba(59, 130, 246, 0.16), transparent 55%),
    linear-gradient(160deg, #10312e 0%, #0c2523 55%, #081b1a 100%);
}

.brand-head {
  display: flex;
  align-items: center;
  gap: 12px;
}
.brand-logo {
  width: 44px;
  height: 44px;
  border-radius: 11px;
  background: #fff;
  padding: 4px;
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.3);
}
.brand-name {
  font-size: 20px;
  font-weight: 700;
  letter-spacing: 0.4px;
}

.brand-slogan {
  margin: 18px 0 30px;
  font-size: 15px;
  line-height: 1.7;
  color: rgba(232, 242, 240, 0.8);
}

.brand-points {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 22px;
}
.brand-points li {
  display: flex;
  gap: 14px;
  align-items: flex-start;
}
.point-icon {
  flex: 0 0 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 10px;
  font-size: 17px;
  color: #7fe0d6;
  background: rgba(127, 224, 214, 0.12);
  border: 1px solid rgba(127, 224, 214, 0.22);
}
.point-title {
  font-size: 14px;
  font-weight: 600;
}
.point-desc {
  margin-top: 3px;
  font-size: 12.5px;
  line-height: 1.6;
  color: rgba(232, 242, 240, 0.62);
}

.brand-foot {
  margin-top: auto;
  padding-top: 28px;
  font-size: 12px;
  color: rgba(232, 242, 240, 0.5);
}

/* ---- 右：表单 ---- */
.login-main {
  flex: 1;
  display: flex;
  flex-direction: column;
  justify-content: center;
  padding: 40px 44px;
}

.login-form {
  width: 100%;
  max-width: 360px;
  margin: 0 auto;
}

.login-title {
  font-size: 22px;
  font-weight: 700;
  color: var(--fg);
}
.login-sub {
  margin: 6px 0 22px;
  font-size: 13.5px;
  color: var(--fg-muted);
}

.identity {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px 14px;
  margin-bottom: 14px;
  border-radius: var(--radius);
  background: var(--accent-soft);
  border: 1px solid var(--el-color-primary-light-7);
}
.identity.muted {
  background: var(--el-fill-color-light);
  border-color: var(--line);
}
.identity-icon {
  font-size: 22px;
  color: var(--accent);
}
.identity.muted .identity-icon {
  color: var(--fg-muted);
}
.identity-text {
  min-width: 0;
}
.identity-name {
  font-weight: 600;
  font-size: 14.5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.identity-role {
  font-size: 12px;
  color: var(--fg-muted);
}
.identity-ok {
  margin-left: auto;
  color: var(--ok);
  font-size: 18px;
}

.note {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  padding: 9px 12px;
  margin-bottom: 16px;
  border-radius: 8px;
  font-size: 12.5px;
  line-height: 1.65;
  color: var(--el-text-color-regular);
  background: var(--el-fill-color-light);
  border: 1px solid var(--el-border-color-lighter);
}
.note.warn {
  color: var(--el-color-warning-dark-2, #b88230);
  background: var(--warn-bg);
  border-color: var(--el-color-warning-light-7, #faecd8);
}
.note.bad {
  color: var(--el-color-danger-dark-2, #c45656);
  background: var(--bad-bg);
  border-color: var(--el-color-danger-light-7, #fbd9d9);
}
html.dark .note.warn {
  color: #e6a23c;
}
.note-icon {
  margin-top: 3px;
  flex: none;
}

.login-submit {
  width: 100%;
  margin-top: 4px;
  font-size: 15px;
}

.login-hint {
  margin-top: 14px;
  font-size: 12px;
  line-height: 1.7;
  color: var(--fg-muted);
}

.login-mobile-head {
  display: none;
}

/* ---- 窄屏：单栏 ---- */
@media (max-width: 879px) {
  .login-page {
    padding: 14px;
    align-items: flex-start;
    padding-top: max(20px, env(safe-area-inset-top));
  }
  .login-shell {
    flex-direction: column;
    min-height: 0;
    margin-top: 4vh;
  }
  .login-brand {
    display: none;
  }
  .login-mobile-head {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 26px;
  }
  .brand-logo-sm {
    width: 40px;
    height: 40px;
    border-radius: 10px;
    background: #fff;
    padding: 3px;
    box-shadow: 0 3px 10px rgba(0, 0, 0, 0.18);
  }
  .mobile-name {
    font-size: 17px;
    font-weight: 700;
  }
  .mobile-sub {
    font-size: 12px;
    color: var(--fg-muted);
    margin-top: 2px;
  }
  .login-main {
    padding: 30px 22px 34px;
    justify-content: flex-start;
  }
  .login-form {
    max-width: 420px;
  }
  .login-title {
    font-size: 20px;
  }
  .glow {
    width: 320px;
    height: 320px;
    filter: blur(70px);
  }
}
</style>
