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

export interface HealthInfo {
  status: string
  version: string
  engine: string
  instances: number
  platform: boolean
}

export interface Instance {
  id: string
  name: string
  engine: string
  engineVersion: string
  mode: string
  status: string
  statusReason?: string
  dataDir: string
  profile: string
  createdAt: number
  updatedAt: number
  sizeBytes: number
  filePath: string
  tableCount?: number
}

export interface BackupRecord {
  id: string
  instanceId: string
  type: string
  status: string
  sizeBytes: number
  checksum: string
  fileName: string
  note?: string
  startedAt: number
  finishedAt: number
  errorCode?: string
  errorHint?: string
}

export interface AuditRecord {
  id: number
  ts: number
  userId: string
  username: string
  action: string
  targetKind?: string
  targetId?: string
  result: string
  detail?: string
}

export interface Column {
  name: string
  type: string
  notNull: boolean
  pk: boolean
  default?: string
}

export interface Table {
  name: string
  type: string
  /** -1 表示未能取到行数（视图或超时） */
  rows: number
  columns: Column[]
}

export interface QueryResult {
  kind: 'query' | 'exec'
  statement: string
  columns?: string[]
  rows?: unknown[][]
  rowCount: number
  rowsAffected: number
  truncated: boolean
  elapsedMs: number
}

interface Envelope<T> {
  code: number
  message: string
  data: T
  traceId: string
}

/** 后端统一响应包里的非 0 code 会转成这个错误，页面直接展示 message。 */
export class ApiError extends Error {
  code: number
  traceId: string

  constructor(message: string, code: number, traceId = '') {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.traceId = traceId
  }
}

/**
 * 网关部署时页面位于 /app/niudbm/ 下，接口必须带上同样的前缀；
 * vite dev 下路径没有前缀，交给 devServer 代理。
 */
function apiBase(): string {
  const matched = window.location.pathname.match(/^\/app\/[^/]+/)
  return `${matched ? matched[0] : ''}/api/v1`
}

async function request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  const init: RequestInit = { method, headers }
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }

  let res: Response
  try {
    res = await fetch(`${apiBase()}${path}`, init)
  } catch {
    throw new ApiError('无法连接到服务，请确认应用正在运行', 0)
  }

  const text = await res.text()
  let env: Envelope<T> | null = null
  if (text) {
    try {
      env = JSON.parse(text) as Envelope<T>
    } catch {
      env = null
    }
  }
  if (!env || typeof env.code !== 'number') {
    const hint = res.ok ? '返回内容不是合法的 JSON' : `请求失败（HTTP ${res.status}）`
    throw new ApiError(hint, res.status)
  }
  if (!res.ok || env.code !== 0) {
    throw new ApiError(env.message || `请求失败（HTTP ${res.status}）`, env.code, env.traceId)
  }
  return env.data
}

function qs(params: Record<string, string | undefined>): string {
  const parts = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== '')
    .map(([k, v]) => `${k}=${encodeURIComponent(v as string)}`)
  return parts.length ? `?${parts.join('&')}` : ''
}

export interface CreateInstanceInput {
  name: string
  profile?: string
  seedSql?: string
  note?: string
}

export const api = {
  diag: () => request<DiagReport>('/diag'),
  health: () => request<HealthInfo>('/health'),

  listInstances: (q = '') =>
    request<{ items: Instance[]; total: number }>(`/instances${qs({ q })}`),
  getInstance: (id: string) => request<Instance>(`/instances/${id}`),
  createInstance: (input: CreateInstanceInput) => request<Instance>('/instances', 'POST', input),
  renameInstance: (id: string, name: string) => request<Instance>(`/instances/${id}`, 'PATCH', { name }),
  deleteInstance: (id: string, confirmName: string, keepData: boolean) =>
    request<{ id: string; keepData: boolean; trashPath: string }>(`/instances/${id}`, 'DELETE', {
      confirmName,
      keepData,
    }),

  schema: (id: string) => request<{ tables: Table[]; total: number }>(`/instances/${id}/schema`),
  query: (id: string, sql: string, limit?: number) =>
    request<QueryResult>(`/instances/${id}/query`, 'POST', { sql, limit }),

  listBackups: (id: string) =>
    request<{ items: BackupRecord[]; total: number; backupRoot: string }>(`/instances/${id}/backups`),
  createBackup: (id: string, note: string) =>
    request<BackupRecord>(`/instances/${id}/backups`, 'POST', { note }),
  restoreBackup: (id: string, bid: string, confirmName: string) =>
    request<{ restored: boolean; backupId: string; protectiveBackup: BackupRecord; sizeBytes: number }>(
      `/instances/${id}/backups/${bid}/restore`,
      'POST',
      { confirmName },
    ),
  deleteBackup: (id: string, bid: string) =>
    request<{ id: string }>(`/instances/${id}/backups/${bid}`, 'DELETE'),
  backupDownloadUrl: (id: string, bid: string) =>
    `${apiBase()}/instances/${id}/backups/${bid}/download`,

  audit: (limit = 100) => request<{ items: AuditRecord[]; total: number }>(`/audit${qs({ limit: String(limit) })}`),
}
