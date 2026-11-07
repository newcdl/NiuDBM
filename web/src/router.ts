import { readonly, ref } from 'vue'

export type ViewKey =
  | 'overview'
  | 'instances'
  | 'backups'
  | 'deploy'
  | 'images'
  | 'audit'
  | 'jobs'
  | 'settings'
  | 'diag'

export interface AppRoute {
  view: ViewKey
  instanceId?: string
  tab?: string
}

const PATH_BY_VIEW: Record<Exclude<ViewKey, 'deploy'>, string> = {
  overview: '/overview',
  instances: '/databases',
  backups: '/backups',
  images: '/images',
  audit: '/audit',
  jobs: '/jobs',
  settings: '/settings',
  diag: '/diagnostics',
}

function appBasePath(): string {
  // 生产包里后端会注入 <base href="/app/niudbm/">；Vite 开发服务器没有 base，
  // 此时 document.baseURI 会跟着当前深链走，不能再当作应用根路径。
  const base = document.querySelector('base')?.getAttribute('href')
  if (!base) return ''
  const path = new URL(base, window.location.origin).pathname
  return path.endsWith('/') ? path.slice(0, -1) : path
}

function relativePath(pathname: string): string {
  const base = appBasePath()
  if (base && pathname.startsWith(base)) {
    return pathname.slice(base.length) || '/'
  }
  return pathname
}

function parseRoute(): AppRoute {
  const path = relativePath(window.location.pathname).replace(/\/+$/, '') || '/'
  if (path === '/databases/new') return { view: 'deploy' }
  const detail = path.match(/^\/databases\/([^/]+)(?:\/([^/]+))?$/)
  if (detail) {
    return {
      view: 'instances',
      instanceId: decodeURIComponent(detail[1]),
      tab: detail[2] ? decodeURIComponent(detail[2]) : 'overview',
    }
  }
  for (const [view, candidate] of Object.entries(PATH_BY_VIEW)) {
    if (path === candidate) return { view: view as ViewKey }
  }
  // 兼容旧通知链接 ?view=jobs，之后逐步只保留正式路径。
  const legacy = new URLSearchParams(window.location.search).get('view')
  const views: ViewKey[] = ['overview', 'instances', 'deploy', 'images', 'audit', 'jobs', 'settings', 'diag']
  if (views.includes(legacy as ViewKey)) return { view: legacy as ViewKey }
  return { view: 'overview' }
}

const routeState = ref<AppRoute>(parseRoute())
export const appRoute = readonly(routeState)

function setRoute(next: AppRoute): void {
  routeState.value = next
}

function pathFor(route: AppRoute): string {
  if (route.view === 'deploy') return appBasePath() + '/databases/new'
  if (route.view === 'instances' && route.instanceId) {
    const tab = route.tab || 'overview'
    return `${appBasePath()}/databases/${encodeURIComponent(route.instanceId)}/${encodeURIComponent(tab)}`
  }
  return appBasePath() + PATH_BY_VIEW[route.view]
}

export function navigate(route: AppRoute, replace = false): void {
  const url = pathFor(route)
  if (replace) window.history.replaceState({}, '', url)
  else window.history.pushState({}, '', url)
  setRoute(parseRoute())
}

export function navigateView(view: ViewKey): void {
  navigate({ view })
}

window.addEventListener('popstate', () => setRoute(parseRoute()))
