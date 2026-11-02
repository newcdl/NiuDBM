/**
 * 响应式断点：手机布局需要换掉的不只是 CSS（有些列、按钮要直接不渲染），
 * 所以在这里统一监听一次，各页面共用。
 */
import { onScopeDispose, readonly, ref } from 'vue'

const MOBILE_QUERY = '(max-width: 720px)'
const media = window.matchMedia(MOBILE_QUERY)

const mobile = ref(media.matches)

media.addEventListener('change', (event) => {
  mobile.value = event.matches
})

/** 只读：断点只应该由窗口大小决定，页面不要自己改 */
export const isMobile = readonly(mobile)

/** 需要在组件销毁时解绑的场景用这个，避免动画、定时器在后台继续跑 */
export function onMobileChange(handler: (mobile: boolean) => void): void {
  const listener = (event: MediaQueryListEvent): void => handler(event.matches)
  media.addEventListener('change', listener)
  onScopeDispose(() => media.removeEventListener('change', listener))
}
