/** 把字节数转成人能读的大小，备份文件经常会到几百 MB，统一走这里避免各页各写一套。 */
export function formatBytes(n: number): string {
  if (!n || n < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = n
  let i = 0
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024
    i += 1
  }
  return `${value >= 100 || i === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[i]}`
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n)
}

/** 绝对时间：2026-10-25 14:03 */
export function formatTime(ms: number): string {
  if (!ms) return '—'
  const d = new Date(ms)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** 相对时间：刚刚 / 12 分钟前 / 3 天前，超过 30 天回落到绝对时间 */
export function relativeTime(ms: number): string {
  if (!ms) return '—'
  const diff = Date.now() - ms
  if (diff < 0) return formatTime(ms)
  const min = Math.floor(diff / 60000)
  if (min < 1) return '刚刚'
  if (min < 60) return `${min} 分钟前`
  const hour = Math.floor(min / 60)
  if (hour < 24) return `${hour} 小时前`
  const day = Math.floor(hour / 24)
  if (day <= 30) return `${day} 天前`
  return formatTime(ms)
}

/** 单元格渲染：null 与空串要能分开看，避免误以为数据丢了 */
// 任务类型的中文名。后端用"对象.动作"命名，翻译集中在这里：
// 概览、任务中心、通知里都会用到，各写一份必然对不上。
const JOB_TYPE_TEXT: Record<string, string> = {
  'instance.create': '创建实例',
  'instance.start': '启动实例',
  'instance.stop': '停止实例',
  'instance.restart': '重启实例',
  'instance.delete': '删除实例',
  'engine.download': '下载引擎',
  'engine.import': '导入引擎',
  'backup.create': '创建备份',
  'backup.restore': '恢复备份',
  'backup.verify': '校验备份',
  'backup.delete': '删除备份',
  'backup.scheduled': '定时备份',
  'backup.policy': '修改备份策略',
  'db.import': '导入数据',
  'db.export': '导出数据',
  'account.sync': '同步账号',
  'settings.update': '修改设置',
  'settings.notify_test': '测试通知',
  'job.cancel': '取消任务',
}

export function jobTypeText(type: string): string {
  return JOB_TYPE_TEXT[type] ?? type
}

export function cellText(v: unknown): string {
  if (v === null) return 'NULL'
  if (v === undefined) return ''
  if (typeof v === 'object') return JSON.stringify(v)
  return String(v)
}

export function errText(e: unknown): string {
  return e instanceof Error ? e.message : '未知错误'
}
