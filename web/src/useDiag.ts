import { ref } from 'vue'
import { api, type DiagReport } from './api'

// 模块级状态：概览与自检页共用同一份报告，避免重复请求
const report = ref<DiagReport | null>(null)
const loading = ref(false)
const error = ref('')

export function useDiag() {
  async function load(): Promise<void> {
    if (loading.value) return
    loading.value = true
    error.value = ''
    try {
      report.value = await api.diag()
    } catch (e) {
      error.value = e instanceof Error ? e.message : '未知错误'
    } finally {
      loading.value = false
    }
  }

  return { report, loading, error, load }
}
