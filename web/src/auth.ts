import { ref } from 'vue'
import { api, type AuthState } from './api'

/*
 * 登录态是模块级状态：App 决定渲染登录页还是主界面，顶栏显示当前账号，
 * 两者读同一份数据，刷新页面后靠 /auth/state 重新问一次。
 */
const state = ref<AuthState | null>(null)
const loading = ref(false)
const error = ref('')

export function useAuth() {
  /** 问后端「我现在是谁」。登录页与主界面进来都先走这一下。 */
  async function load(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      state.value = await api.authState()
    } catch (e) {
      // 后端没起来（或前端单独 dev）时不要假装已登录：让登录页说明连不上
      error.value = e instanceof Error ? e.message : '无法读取登录状态'
      state.value = null
    } finally {
      loading.value = false
    }
  }

  /** 点「登录」：用本次请求自带的飞牛身份换一份会话。 */
  async function login(): Promise<void> {
    loading.value = true
    try {
      await api.authLogin()
      state.value = await api.authState()
    } finally {
      loading.value = false
    }
  }

  /** 退出登录；退出后重新读一次状态，让登录页拿到最新的身份说明。 */
  async function logout(): Promise<void> {
    try {
      await api.authLogout()
    } finally {
      await load()
    }
  }

  return { state, loading, error, load, login, logout }
}
