
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
  /** 主引擎（mysql）；完整清单看 engines */
  engine: string
  /** 支持的引擎清单：mysql / postgres */
  engines?: string[]
  instances: number
  platform: boolean
  /** Docker 是否可用：不可用时部署按钮要能说明原因 */
  docker: boolean
}

/** 引擎展示名：台账里的 engine 值 → 界面上显示的名字。 */
export const ENGINE_LABEL: Record<string, string> = {
  mysql: 'MySQL',
  postgres: 'PostgreSQL',
  mongodb: 'MongoDB',
}

/** 取引擎展示名；老数据没有 engine 时按 MySQL 显示。 */
export function engineLabel(engine?: string): string {
  if (!engine) return ENGINE_LABEL.mysql
  return ENGINE_LABEL[engine] ?? engine
}

export interface Instance {
  id: string
  name: string
  engine: string
  engineVersion: string
  /** managed：本应用创建的 Docker 服务；external：用户已有数据库 */
  mode: 'managed' | 'external'
  host?: string
  username?: string
  tlsMode?: 'disable' | 'require'
  /** 实际拉取/运行的镜像地址（含加速站或私有仓库前缀） */
  image?: string
  status: string
  statusReason?: string
  dataDir: string
  profile: string
  /** 服务占用的宿主端口 */
  port?: number
  /** CPU 核数上限，0 表示不限制（仅 docker 方式生效） */
  cpuLimit?: number
  /** 内存上限（MB），0 表示不限制 */
  memLimitMB?: number
  /** volume（Docker 命名卷）或 bind（宿主机目录） */
  storageKind?: string
  /** bind 方式下的宿主机目录 */
  storagePath?: string
  createdAt: number
  updatedAt: number
  sizeBytes: number
  /** 数据在 docker 卷里、宿主读不到体积时为真，界面要显示"未知"而不是 0 */
  sizeUnknown?: boolean
  filePath: string
  tableCount?: number
  /** 备份份数与最近一次备份时间（概览等聚合接口可能不带） */
  backupCount?: number
  lastBackupAt?: number
  /** 后端按服务类型给出的页面能力，前端不要自己按引擎猜 */
  capabilities: InstanceCapabilities
  accessRole: 'viewer' | 'operator' | 'admin'
}

export interface InstanceCapabilities {
  startStop: boolean
  container: boolean
  config: boolean
  configFile: boolean
  backup: boolean
  users: boolean
  databases: boolean
  transfer: boolean
  testConnection: boolean
  revealRoot: boolean
  deletePermanent: boolean
  members: boolean
}

/** 连接信息给的是 root 入口账号：一个服务下可有多个库，所以不带"库名" */
export interface ConnectionInfo {
  engine: string
  version: string
  /** 外部连接为 true，界面不显示容器地址与 root 管理 */
  external?: boolean
  host: string
  port: number
  /** 容器内地址：同一 Docker 网络里的应用用它连 */
  containerHost?: string
  containerPort?: number
  user: string
  /** PG 连接必须指定库，默认给 postgres 维护库；MySQL 为空 */
  database?: string
  password: string
  running: boolean
}

export interface BackupRecord {
  id: string
  instanceId: string
  type: string
  status: string
  sizeBytes: number
  checksum: string
  fileName: string
  /** 这份备份属于哪个库：恢复时回到同一个库 */
  database?: string
  note?: string
  startedAt: number
  finishedAt: number
  errorCode?: string
  errorHint?: string
}

export interface BackupWithInstance extends BackupRecord {
  instanceName: string
  engine: string
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

/**
 * 配置项。不同引擎的可调项与生效方式不同（例如 PG 走 ALTER SYSTEM），
 * 但形状一致：后端给什么就渲染什么，前端不预设某个引擎有哪些参数。
 */
export interface ConfigItem {
  key: string
  label: string
  value: string
  default: string
  /** int | size | enum | bool | text */
  kind: string
  options?: string[]
  /** persistent | reload | restart | session：改完要不要重启，必须告诉用户 */
  apply: string
  hint?: string
}

export interface InstanceConfig {
  engine: string
  items: ConfigItem[]
  readonly: boolean
}

export interface ConfigResult {
  needRestart: boolean
  message: string
  /** PostgreSQL 写完配置后 pg_file_settings 报出的解析错误（行/参数/原因） */
  warnings?: string[]
}

/** 一套按内存档位算好的参数组合 */
export interface TuningPreset {
  key: string
  label: string
  memoryMB: number
  values: Record<string, string>
  hint: string
}

/** 一条运行状态指标 */
export interface StatusMetric {
  key: string
  label: string
  value: string
  hint?: string
}

/** 数据库账号：权限只给三档，细到表级的授权在网页表单里既难表达也难核对。 */
export interface RoleGrant {
  database: string
  /** none | read | write | all */
  level: string
}

export interface DbUser {
  name: string
  /** 允许来源：["%"] 表示所有人，具体 IP 表示只允许这些地址 */
  hosts?: string[]
  login: boolean
  super: boolean
  /** 兼容字段：第一个授权库的档位 */
  level: string
  /** 引擎自带账号，界面只读 */
  system: boolean
  /** 库授权列表：一个账号可以有多个库的权限 */
  grants?: RoleGrant[]
}

export interface InstanceMember {
  instanceId: string
  userId: string
  username: string
  role: 'viewer' | 'operator' | 'admin'
  createdAt: number
  updatedAt: number
}

/** 可部署的镜像版本：tag 即版本，本地有没有实时问 docker。 */
export interface ImageInfo {
  tag: string
  /** 完整镜像名，例如 mysql:8.4 */
  name: string
  /** mysql / mariadb / postgres */
  family: string
  label: string
  note?: string
  archs: string[]
  /** 当前架构有没有官方镜像 */
  supports: boolean
  /** 本地是否已有该镜像 */
  local: boolean
  default: boolean
  /** 正在用这个版本的服务名；非空时不允许删镜像 */
  usedBy: string[]
}

/** 配置文件页的可选文件（MySQL 只有一份，PG 有三份）。 */
export interface ConfigFileMeta {
  name: string
  label: string
  writable: boolean
  note?: string
}

export interface ConfigFileData {
  path: string
  content: string
  backupExists: boolean
  suggested?: string
  missing?: string[]
  files?: ConfigFileMeta[]
  selected?: string
  /** 读不到时的原因（服务未运行、文件在卷里等），仍会返回文件清单 */
  readError?: string
  running?: boolean
}

function queryName(name?: string): string {
  return name ? `?name=${encodeURIComponent(name)}` : ''
}

export interface ImageList {
  items: ImageInfo[]
  total: number
  dockerOk: boolean
  arch: string
  /** 镜像加速站前缀（NIUDBM_DOCKER_MIRROR），可能为空 */
  dockerMirror?: string
}

export interface PortStatus {
  port: number
  inUse: boolean
  engine?: string
  hint?: string
}

export interface HostInstall {
  name: string
  path: string
  kind: 'binary' | 'datadir' | 'config'
}

export interface PreflightResult {
  arch: string
  engineRoot: string
  freeBytes: number
  ports: PortStatus[]
  docker: { installed: boolean; running: boolean; version?: string; note?: string }
  existing: HostInstall[]
  warnings: string[]
  checks: string[]
}

interface Envelope<T> {
  code: number
  message: string
  data: T
  traceId: string
}

/* ---------- 登录 ---------- */

/** 登录的人：身份来自 NAS，账号不是牛库管家自己建的。 */
export interface AuthUser {
  uid: string
  username: string
  isAdmin: boolean
}

/**
 * 这次请求自带的飞牛身份。
 * available=false 时 reason 会说明原因（直接访问端口、刷新过页面等），登录页据此解释。
 */
export interface AuthGateway {
  available: boolean
  uid?: string
  username?: string
  isAdmin: boolean
  reason?: string
}

export interface AuthState {
  authenticated: boolean
  user?: AuthUser
  gateway: AuthGateway
  platform: boolean
  /** 开发模式（直接 go run / vite dev）：没有网关，身份按 dev 管理员放行 */
  dev: boolean
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

async function send<T>(path: string, init: RequestInit): Promise<T> {
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
    // 会话过期/被注销时统一广播一次：App 收到就回到登录页，
    // 而不是让每个页面各自处理一遍「这次失败是不是因为没登录」。
    if (res.status === 401) window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
    throw new ApiError(env.message || `请求失败（HTTP ${res.status}）`, env.code, env.traceId)
  }
  return env.data
}

/** 未登录/会话失效时广播的事件名。 */
export const UNAUTHORIZED_EVENT = 'niudbm:unauthorized'

async function request<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  const init: RequestInit = { method, headers }
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }
  return send<T>(path, init)
}

/** 导入 SQL 脚本走纯文本请求体：脚本里全是引号与换行，再套一层 JSON 既浪费带宽又容易出错。 */
async function requestText<T>(path: string, body: string): Promise<T> {
  return send<T>(path, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'text/plain; charset=utf-8' },
    body,
  })
}

function qs(params: Record<string, string | undefined>): string {
  const parts = Object.entries(params)
    .filter(([, v]) => v !== undefined && v !== '')
    .map(([k, v]) => `${k}=${encodeURIComponent(v as string)}`)
  return parts.length ? `?${parts.join('&')}` : ''
}

export interface CreateUserInput {
  name: string
  password: string
  /** 访问来源：["%"] 所有人，或若干 IPv4 */
  hosts?: string[]
  /** 一次授好的库；不传表示先不授权 */
  grants?: RoleGrant[]
  /** 老表单简写：只授一个库 */
  level?: string
  database?: string
}

export interface CreateInstanceInput {
  name: string
  /** mysql / postgres；留空即 MySQL */
  engine?: string
  /** 镜像 tag（8.4 / 8.0 / 11.4 / 17）；留空用默认版本 */
  engineVersion?: string
  /** 完整镜像地址（可含加速站或私有仓库，必须带标签）；留空用清单地址 */
  image?: string
  profile?: string
  seedSql?: string
  note?: string
  /** 建库时一并创建的接入账号（仅常驻引擎） */
  users?: CreateUserInput[]
  /** 容器 root 密码；留空自动生成 */
  rootPassword?: string
  /** CPU 核数上限，0 表示不限制 */
  cpuLimit?: number
  /** 内存上限（MB），0 表示不限制 */
  memLimitMB?: number
  /** volume（默认）或 bind */
  storageKind?: string
  /** bind 方式下的宿主机目录 */
  storagePath?: string
  /** 指定监听端口；留空（0）由服务端从端口池自动分配 */
  port?: number
}

export interface ConnectInstanceInput {
  name: string
  engine: 'mysql' | 'postgres' | 'mongodb'
  engineVersion?: string
  host: string
  port: number
  username: string
  password: string
  tlsMode: 'disable' | 'require'
}

/** 部署常驻服务是异步的：POST 先回 taskId，前端轮询阶段。 */
export interface DeployTask {
  id: string
  name: string
  engine: string
  phase: string
  phaseText: string
  status: 'running' | 'ok' | 'failed'
  error?: string
  instanceId?: string
  startedAt: number
  finishedAt?: number
}

/** 一个数据库服务里的逻辑库：界面上叫「实例」。 */
export interface DatabaseItem {
  name: string
  charset: string
  tableCount: number
  sizeBytes: number
  /** 在这个库上有授权的账号名：列表里"授权用户"一列直接用 */
  users?: string[]
}

/** 新建库时可选的字符集：由引擎自己报告，不同版本能建的不一样。 */
export interface CharsetOption {
  value: string
  label: string
  /** 建库时用的排序规则，已按"跨版本稳定"挑过 */
  collation: string
  recommended?: boolean
}

/* ---------- 任务中心 ---------- */

export type JobStatus = 'queued' | 'running' | 'succeeded' | 'failed' | 'canceled'

export interface Job {
  id: string
  type: string
  targetKind?: string
  targetId?: string
  status: JobStatus
  /** 0–100，长任务会长时间停在某个值 */
  progress: number
  /** 脱敏后的参数快照 */
  params?: string
  /** 结构化结果，如新建实例的 id */
  result?: string
  errorCode?: string
  /** 失败时给用户的下一步 */
  errorHint?: string
  attempts: number
  createdBy: string
  createdAt: number
  startedAt?: number
  finishedAt?: number
}

export interface JobList {
  items: Job[]
  total: number
  /** 进行中的任务数，顶栏角标用 */
  active: number
}

/* ---------- 全局设置 ---------- */

export interface AppSettings {
  version: string
  /** 备份默认保留份数 */
  backupRetentionCount: number
  /** 默认备份目录（只读，来自共享目录授权） */
  backupDir: string
  /** 审计日志保留天数 */
  auditRetentionDays: number
  /** 通知 Webhook，留空则不推送 */
  notifyWebhook: string
  notifyBackupSucceeded: boolean
  notifyBackupFailed: boolean
  notifyInstanceDown: boolean
}

/* ---------- 定时备份策略 ---------- */

export type BackupTargetKind = 'local' | 'share'

export interface BackupPolicy {
  id: string
  instanceId: string
  enabled: boolean
  /** daily / weekly */
  schedule: 'daily' | 'weekly'
  /** 执行时刻，HH:mm */
  time: string
  /** 每周几，0=周日，仅 weekly 有意义 */
  weekday: number
  retentionCount: number
  retentionDays?: number
  targetKind: BackupTargetKind
  targetRef: string
  encrypt: boolean
  lastRunAt?: number
  lastResult?: 'succeeded' | 'failed'
  nextRunAt?: number
}

export const api = {
  // 登录：身份由飞牛统一网关提供，应用不建账号、不存密码
  authState: () => request<AuthState>('/auth/state'),
  authLogin: () => request<{ user: AuthUser; expiresAt: number }>('/auth/login', 'POST'),
  authLogout: () => request<{ ok: boolean }>('/auth/logout', 'POST'),
  authMe: () => request<AuthUser>('/auth/me'),

  diag: () => request<DiagReport>('/diag'),
  health: () => request<HealthInfo>('/health'),

  listInstances: (q = '') =>
    request<{ items: Instance[]; total: number }>(`/instances${qs({ q })}`),
  getInstance: (id: string) => request<Instance>(`/instances/${id}`),
  listMembers: (id: string) =>
    request<{ items: InstanceMember[]; total: number }>(`/instances/${id}/members`),
  upsertMember: (id: string, userId: string, username: string, role: InstanceMember['role']) =>
    request<InstanceMember>(`/instances/${id}/members/${encodeURIComponent(userId)}`, 'PUT', { username, role }),
  deleteMember: (id: string, userId: string) =>
    request<{ userId: string }>(`/instances/${id}/members/${encodeURIComponent(userId)}`, 'DELETE'),
  // MySQL / PostgreSQL / MongoDB 返回 { taskId }，需轮询 deployTask
  createInstance: (input: CreateInstanceInput) =>
    request<Instance | { taskId: string; name: string; async: true }>('/instances', 'POST', input),
  connectInstance: (input: ConnectInstanceInput) => request<Instance>('/instances/connect', 'POST', input),
  testConnection: (id: string) =>
    request<{ connected: boolean; message: string }>(`/instances/${id}/test-connection`, 'POST'),
  deployTask: (taskId: string) => request<DeployTask>(`/deploy-tasks/${taskId}`),
  /** 部署现场：失败时后端把容器日志固化到实例目录，这里原样取回 */
  deployLog: (id: string) =>
    request<{ available: boolean; path: string; log: string; message?: string }>(`/instances/${id}/deploy-log`),
  renameInstance: (id: string, name: string) => request<Instance>(`/instances/${id}`, 'PATCH', { name }),
  deleteInstance: (id: string, confirmName: string, keepData: boolean) =>
    request<{ id: string; keepData: boolean; trashPath: string }>(`/instances/${id}`, 'DELETE', {
      confirmName,
      keepData,
    }),

  // 配置：两边可调项完全不同，界面只按后端给的清单渲染
  getConfig: (id: string) => request<InstanceConfig>(`/instances/${id}/config`),
  updateConfig: (id: string, values: Record<string, string>) =>
    request<ConfigResult>(`/instances/${id}/config`, 'PUT', { values }),
  // 按内存档位算好的参数组合，一键填表
  configPresets: (id: string) =>
    request<{ items: TuningPreset[] }>(`/instances/${id}/config/presets`),
  // 运行状态指标（SHOW GLOBAL STATUS 里最常用的那些）
  instanceStatus: (id: string) => request<{ items: StatusMetric[] }>(`/instances/${id}/status`),
  // 密码回显：读本机保存的那一份（外部工具建的账号没有）
  revealUserPassword: (id: string, name: string) =>
    request<{ name: string; password: string }>(`/instances/${id}/users/${encodeURIComponent(name)}/password`),

  // 数据库用户（仅常驻引擎有）
  listUsers: (id: string) =>
    request<{ items: DbUser[]; total: number; levels: string[] }>(`/instances/${id}/users`),
  createUser: (id: string, input: CreateUserInput) =>
    request<{ name: string; grants: RoleGrant[] }>(`/instances/${id}/users`, 'POST', input),
  // grants / hosts 不传 = 不改；传空数组 = 撤销全部授权
  updateUser: (id: string, name: string, input: { password?: string; grants?: RoleGrant[]; hosts?: string[] }) =>
    request<{ name: string; changes: string[] }>(
      `/instances/${id}/users/${encodeURIComponent(name)}`,
      'PATCH',
      input,
    ),
  deleteUser: (id: string, name: string) =>
    request<{ name: string }>(`/instances/${id}/users/${encodeURIComponent(name)}`, 'DELETE'),

  // 导入导出：只到表级，不碰表结构
  listTables: (id: string, database?: string) =>
    request<{ items: string[]; total: number; database: string }>(
      `/instances/${id}/tables${qs({ database })}`,
    ),
  exportUrl: (id: string, database: string, table: string, limit?: number) =>
    `${apiBase()}/instances/${id}/export${qs({ database, table, limit: limit ? String(limit) : undefined })}`,
  importCSV: (id: string, database: string, table: string, csv: string) =>
    request<{ table: string; rows: number }>(`/instances/${id}/import/csv`, 'POST', {
      database,
      table,
      csv,
    }),
  importSQL: (id: string, database: string, script: string) =>
    requestText<{ statements: number; elapsedMs: number }>(
      `/instances/${id}/import/sql${qs({ database })}`,
      script,
    ),

  // 镜像管理：版本就是镜像 tag，本地状态实时问 docker
  listImages: () => request<ImageList>('/images'),
  // 立即拉取：本地没有时返回 jobId，前端轮询 /jobs/{id}
  pullImage: (tag: string, family: string) =>
    request<{ async?: boolean; jobId?: string; image: string; cached?: boolean; message?: string }>(
      '/images/pull',
      'POST',
      { tag, family },
    ),
  // 删除本地镜像：有服务在用时后端 409，并点名是谁
  deleteImage: (tag: string, family: string) =>
    request<{ message: string }>(`/images/${encodeURIComponent(tag)}${qs({ family })}`, 'DELETE'),
  preflight: () => request<PreflightResult>('/preflight'),

  startInstance: (id: string) => request<{ id: string; status: string }>(`/instances/${id}/start`, 'POST'),
  stopInstance: (id: string) => request<{ id: string; status: string }>(`/instances/${id}/stop`, 'POST'),
  restartInstance: (id: string) => request<{ id: string; status: string }>(`/instances/${id}/restart`, 'POST'),
  updateLimits: (id: string, input: { cpuLimit?: number; memLimitMB?: number; port?: number }) =>
    request<Instance>(`/instances/${id}`, 'PATCH', input),
  connection: (id: string) => request<ConnectionInfo>(`/instances/${id}/connection`),
  // root 远程访问开关（关掉后只有容器内能连 root）
  getRootAccess: (id: string) => request<{ remote: boolean }>(`/instances/${id}/root-access`),
  setRootAccess: (id: string, remote: boolean) =>
    request<{ remote: boolean; message: string }>(`/instances/${id}/root-access`, 'PUT', { remote }),
  setRootPassword: (id: string, password: string) =>
    request<{ message: string }>(`/instances/${id}/root-password`, 'PUT', { password }),
  // 配置文件原文：白名单之外的参数从这里写，保存后需重启
  // suggested 是一份"应有的默认配置"文本（不落盘），missing 是当前文件缺少的可配置项
  // name 只在 PostgreSQL 下有意义（postgresql.conf / pg_hba.conf / postgresql.auto.conf）
  getConfigFile: (id: string, name?: string) =>
    request<ConfigFileData>(`/instances/${id}/config/file${queryName(name)}`),
  putConfigFile: (id: string, content: string, name?: string) =>
    request<ConfigResult>(`/instances/${id}/config/file${queryName(name)}`, 'PUT', { content }),
  restoreConfigFile: (id: string, name?: string) =>
    request<ConfigResult>(`/instances/${id}/config/file/restore${queryName(name)}`, 'POST'),

  // 一个数据库服务里的多个实例（逻辑库）
  listDatabases: (id: string) =>
    request<{ items: DatabaseItem[]; total: number }>(`/instances/${id}/databases`),
  // 引擎实际支持的字符集：gb18030 / utf8mb3 这类在不同版本上出现时间不同
  listCharsets: (id: string) =>
    request<{ items: CharsetOption[]; total: number }>(`/instances/${id}/databases/charsets`),
  createDatabase: (id: string, input: { name: string; charset?: string; users?: CreateUserInput[] }) =>
    request<{ name: string; charset: string }>(`/instances/${id}/databases`, 'POST', input),
  // 给已存在的库补账号：账号只在这个库上有权限
  createDatabaseUser: (id: string, name: string, input: { name: string; password: string; level: string }) =>
    request<{ name: string; level: string; database: string }>(
      `/instances/${id}/databases/${encodeURIComponent(name)}/users`,
      'POST',
      input,
    ),
  dropDatabase: (id: string, name: string, confirmName: string) =>
    request<{ name: string; deleted: boolean }>(
      `/instances/${id}/databases/${encodeURIComponent(name)}${qs({ confirmName })}`,
      'DELETE',
    ),

  listBackups: (id: string) =>
    request<{ items: BackupRecord[]; total: number; backupRoot: string }>(`/instances/${id}/backups`),
  listBackupCenter: () => request<{ items: BackupWithInstance[]; total: number }>('/backups'),
  createBackup: (id: string, note: string, database?: string) =>
    request<BackupRecord>(`/instances/${id}/backups`, 'POST', { note, database }),
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

  // 任务中心：列表 / 详情 / 取消（取消只对 queued、running 有意义）
  // 任务中心：列表 / 详情 / 取消（取消只对 queued、running 有意义）
  listJobs: (status?: JobStatus) => request<JobList>(`/jobs${qs({ status })}`),
  getJob: (id: string) => request<Job>(`/jobs/${id}`),
  cancelJob: (id: string) => request<{ id: string; status: JobStatus }>(`/jobs/${id}/cancel`, 'POST'),

  // 全局设置
  getSettings: () => request<AppSettings>('/settings'),
  updateSettings: (patch: Partial<AppSettings>) => request<AppSettings>('/settings', 'PATCH', patch),
  testNotify: () => request<{ ok: boolean; message: string }>('/settings/notify/test', 'POST'),

  // 定时备份策略（一实例一条，读 / 写）
  getBackupPolicy: (id: string) => request<BackupPolicy>(`/instances/${id}/backup-policy`),
  updateBackupPolicy: (id: string, policy: Partial<BackupPolicy>) =>
    request<BackupPolicy>(`/instances/${id}/backup-policy`, 'PUT', policy),

  // 概览：一次拿全首屏数字与最近动态
  getOverview: () => request<Overview>('/overview'),
}

/* ---------- 概览（首屏聚合，一次请求） ---------- */

export interface OverviewImage {
  tag: string
  name: string
  label: string
  /** mysql / mariadb / postgres */
  family: string
  local: boolean
  dockerOk: boolean
}

export interface OverviewBackup {
  id: string
  instanceId: string
  instanceName: string
  type: string
  status: string
  sizeBytes: number
  fileName: string
  startedAt: number
}

export interface OverviewJob {
  id: string
  type: string
  status: JobStatus
  targetId: string
  createdAt: number
}

export interface OverviewAudit {
  id: number
  ts: number
  username: string
  action: string
  result: string
  detail: string
}

export interface Overview {
  version: string
  instances: {
    total: number
    running: number
    stopped: number
    /** 部署中的服务 */
    creating: number
    /** 部署失败、留着现场的服务 */
    failed: number
    error: number
    sizeBytes: number
    /** 数据在 docker 卷里、读不到体积的服务数 */
    sizeUnknown: number
    items: Instance[]
  }
  images: OverviewImage[]
  backups: { total: number; failed: number; latestAt: number; recent: OverviewBackup[] }
  jobs: { active: number; failed24h: number; recent: OverviewJob[] }
  audit: { today: number; recent: OverviewAudit[] }
  docker: { available: boolean; version: string }
}
