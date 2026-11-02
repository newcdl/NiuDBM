/**
 * 列表展示形态：表格 / 卡片。
 *
 * 规则：
 * - 第一次进来按设备给默认值：手机卡片、桌面表格。
 * - 用户手动切过一次以后就记住，不再被窗口宽度改回去。
 * - 没手动切过时，横竖屏/窗口跨断点会自动跟着变，避免手机上被挤成一条表格。
 */
import { computed, ref, watch } from 'vue'
import { isMobile } from './media'

export type ViewMode = 'table' | 'card'

const STORAGE_KEY = 'niudbm.viewMode'

function loadMode(): ViewMode | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw === 'table' || raw === 'card') return raw
  } catch {
    // 隐私模式下读不到，按设备默认
  }
  return null
}

function deviceDefault(): ViewMode {
  return isMobile.value ? 'card' : 'table'
}

const saved = loadMode()

/** 用户是否手动选过；手动选过就不再跟随断点 */
const customized = ref(saved !== null)

export const viewMode = ref<ViewMode>(saved ?? deviceDefault())

export const isCardView = computed<boolean>(() => viewMode.value === 'card')

watch(isMobile, () => {
  if (!customized.value) viewMode.value = deviceDefault()
})

watch(viewMode, (value) => {
  try {
    localStorage.setItem(STORAGE_KEY, value)
  } catch {
    // 写不进去只影响下次进来，不影响当前页面
  }
})

export function setViewMode(mode: ViewMode): void {
  customized.value = true
  viewMode.value = mode
}

export function toggleViewMode(): void {
  setViewMode(viewMode.value === 'table' ? 'card' : 'table')
}
