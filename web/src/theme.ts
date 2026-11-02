/**
 * 主题：跟随系统 / 强制浅色 / 强制深色。
 *
 * 深色走 Element Plus 官方的 html.dark + dark/css-vars.css，
 * 我们自己的变量在 styles.css 里全部映射到 --el-*，因此换主题只需要切这个 class。
 */
import { computed, ref, watch } from 'vue'

export type ThemeMode = 'auto' | 'light' | 'dark'

const STORAGE_KEY = 'niudbm.theme'
const DARK_QUERY = '(prefers-color-scheme: dark)'

export const THEME_LABEL: Record<ThemeMode, string> = {
  auto: '跟随系统',
  light: '浅色',
  dark: '深色',
}

function loadMode(): ThemeMode {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw === 'auto' || raw === 'light' || raw === 'dark') return raw
  } catch {
    // 隐私模式下读不到，按跟随系统处理
  }
  return 'auto'
}

const media = window.matchMedia(DARK_QUERY)

export const themeMode = ref<ThemeMode>(loadMode())
export const systemDark = ref(media.matches)

export const isDark = computed<boolean>(() =>
  themeMode.value === 'auto' ? systemDark.value : themeMode.value === 'dark',
)

function apply(): void {
  const dark = isDark.value
  const root = document.documentElement
  root.classList.toggle('dark', dark)
  root.dataset.theme = dark ? 'dark' : 'light'

  // 手机上浏览器的地址栏配色跟着主题走
  const meta = document.querySelector('meta[name="theme-color"]')
  if (meta) meta.setAttribute('content', dark ? '#0d1314' : '#f5f7f9')

  try {
    localStorage.setItem(STORAGE_KEY, themeMode.value)
  } catch {
    // 同上，写不进去就只影响下次记住的选择
  }
}

media.addEventListener('change', (event) => {
  systemDark.value = event.matches
})

watch([themeMode, systemDark], apply, { immediate: true })

export function setTheme(mode: ThemeMode): void {
  themeMode.value = mode
}
