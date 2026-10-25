export type Level = 'pass' | 'warn' | 'fail'

export const LEVEL_TEXT: Record<Level, string> = {
  pass: '通过',
  warn: '需确认',
  fail: '未通过',
}

export interface DiagItem {
  id: string
  level: Level
  name: string
  detail: string
  /** 关联的核对清单编号，如 P-05 */
  ref?: string
}

export interface DiagGroup {
  name: string
  items: DiagItem[]
}

export interface EnvInfo {
  appName: string
  version: string
  user: string
  uid: number
  gid: number
  isAdmin: boolean
  arch: string
  /** gateway = 由统一网关转发，tcp = 开发模式直连 */
  listen: string
  osName: string
  glibc: string
}

export interface DiagReport {
  generatedAt: string
  env: EnvInfo
  summary: { pass: number; warn: number; fail: number; total: number }
  groups: DiagGroup[]
}

/**
 * 网关部署时页面位于 /app/niudbm/ 下，接口必须带上同样的前缀；
 * vite dev 下路径没有前缀，交给 devServer 代理。
 */
function apiBase(): string {
  const matched = window.location.pathname.match(/^\/app\/[^/]+/)
  return `${matched ? matched[0] : ''}/api/v1`
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${apiBase()}${path}`, {
    ...init,
    headers: { Accept: 'application/json', ...(init?.headers ?? {}) },
  })
  if (!res.ok) {
    let hint = ''
    try {
      const body = (await res.json()) as { message?: string }
      hint = body.message ?? ''
    } catch {
      hint = res.statusText
    }
    throw new Error(hint || `请求失败（HTTP ${res.status}）`)
  }
  return (await res.json()) as T
}

export const api = {
  diag: () => request<DiagReport>('/diag'),
  health: () => request<{ status: string; version: string }>('/health'),
}
