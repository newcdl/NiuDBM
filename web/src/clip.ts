/**
 * 复制文本到剪贴板。
 *
 * NAS 上页面多数是 http 访问，不是安全上下文时 navigator.clipboard 压根不存在，
 * 所以必须留一条 execCommand 的退路：否则「复制连接串」点了没反应，用户只会以为坏了。
 */
export async function copyText(text: string): Promise<boolean> {
  if (window.isSecureContext && navigator.clipboard) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // 浏览器拒绝授权（未聚焦、无权限）时继续走退路
    }
  }

  const area = document.createElement('textarea')
  area.value = text
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.top = '-9999px'
  document.body.appendChild(area)
  area.select()
  area.setSelectionRange(0, text.length)
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  document.body.removeChild(area)
  return ok
}

/** 使用密码学随机源生成密码；64 字符集避免取模偏差。 */
export function randomPassword(length = 16): string {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_'
  const bytes = new Uint8Array(length)
  window.crypto.getRandomValues(bytes)
  let out = ''
  for (let i = 0; i < length; i++) out += alphabet[bytes[i] % alphabet.length]
  return out
}
