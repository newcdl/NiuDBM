/**
 * 访问来源（MySQL 账号的 Host）解析与展示。
 *
 * 后端接受 `%`、`localhost`、单个 IPv4、网段（`/24` 或 `/255.255.255.0`），
 * 落库前会把前缀长度换成点分掩码——MySQL 的 Host 列只认后者。界面上要反过来：
 * 存的是 `172.16.10.0/255.255.255.0`，显示时还原成 `172.16.10.0/24`，否则用户
 * 会以为自己填错了。
 */

export type HostKind = 'all' | 'local' | 'ip' | 'range'

export interface HostRule {
  /** 提交给接口的值：网段一律用 `/24` 前缀，后端负责转点分掩码 */
  value: string
  kind: HostKind
  /** 界面上显示的值 */
  label: string
}

export const HOST_KIND_TEXT: Record<HostKind, string> = {
  all: '所有人',
  local: '仅本机',
  ip: 'IP',
  range: '网段',
}

/** 255.255.255.0 → 24；不是合法掩码时返回 -1 */
export function maskToPrefix(mask: string): number {
  const parts = mask.split('.')
  if (parts.length !== 4) return -1
  let bits = 0
  for (const part of parts) {
    const n = Number(part)
    if (!Number.isInteger(n) || n < 0 || n > 255) return -1
    let byte = n
    for (let i = 0; i < 8; i += 1) {
      if (byte & 0x80) {
        bits += 1
        byte = (byte << 1) & 0xff
      } else if (byte !== 0) {
        return -1
      }
    }
  }
  return bits
}

function validIpv4(ip: string): boolean {
  const parts = ip.split('.')
  if (parts.length !== 4) return false
  return parts.every((p) => {
    if (!/^\d{1,3}$/.test(p)) return false
    const n = Number(p)
    return n >= 0 && n <= 255
  })
}

/** 把用户输入解析成一条来源规则；不合法时返回 null，由调用方给提示 */
export function parseHost(input: string): HostRule | null {
  const text = input.trim()
  if (!text) return null
  if (text === '%') return { value: '%', kind: 'all', label: '所有人' }
  if (text === 'localhost') return { value: 'localhost', kind: 'local', label: 'localhost' }

  const slash = text.indexOf('/')
  if (slash < 0) {
    if (!validIpv4(text)) return null
    return { value: text, kind: 'ip', label: text }
  }

  const ip = text.slice(0, slash).trim()
  const mask = text.slice(slash + 1).trim()
  if (!validIpv4(ip) || !mask) return null

  let prefix: number
  if (mask.includes('.')) {
    prefix = maskToPrefix(mask)
    if (prefix < 0) return null
  } else {
    if (!/^\d{1,2}$/.test(mask)) return null
    prefix = Number(mask)
    if (prefix > 32) return null
  }
  return { value: `${ip}/${prefix}`, kind: 'range', label: `${ip}/${prefix}` }
}

/** 展示用：把库里存回来的点分掩码还原成前缀长度 */
export function displayHost(host: string): string {
  const text = host.trim()
  if (text === '%') return '所有人'
  const slash = text.indexOf('/')
  if (slash < 0) return text
  const ip = text.slice(0, slash)
  const mask = text.slice(slash + 1)
  const prefix = maskToPrefix(mask)
  return prefix >= 0 && mask.includes('.') ? `${ip}/${prefix}` : text
}

/** 库里存的值 → 界面上的标签规则 */
export function ruleOfHost(host: string): HostRule | null {
  return parseHost(displayHost(host))
}

/** 去重 + 去空白，保持添加顺序 */
export function uniqueHosts(rules: HostRule[]): HostRule[] {
  const seen = new Set<string>()
  const out: HostRule[] = []
  for (const rule of rules) {
    if (seen.has(rule.value)) continue
    seen.add(rule.value)
    out.push(rule)
  }
  return out
}

/** 按输入形态给一句贴合的提示，输入不合法时用 */
export function hostHint(input: string): string {
  const text = input.trim()
  if (!text) return '填一个 IP 或网段'
  if (/[a-zA-Z]/.test(text) && text !== 'localhost') {
    return '不支持主机名：MySQL 默认不反解域名，写了也连不上'
  }
  const slash = text.indexOf('/')
  if (slash >= 0) {
    const mask = text.slice(slash + 1).trim()
    if (!mask) return '网段要带掩码，例如 172.16.10.0/24'
    if (!mask.includes('.') && Number(mask) > 32) return '掩码最大是 /32'
    return '网段格式不对，例如 172.16.10.0/24 或 172.16.10.0/255.255.255.0'
  }
  return 'IP 格式不对，每段要在 0–255 之间，例如 172.16.10.111'
}
