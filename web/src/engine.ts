/**
 * 引擎元信息与页签映射。
 *
 * 页签键与台账里的 engine 值不完全同名（postgresql ↔ postgres）：
 * MySQL 与 PostgreSQL 是产品名，postgres 是镜像与库里的标识，两者都别改，
 * 只在这里做一次映射，避免每个页面各写一份。
 */
export type Engine = 'mysql' | 'postgresql' | 'mongodb'

/** 页签键 → 台账 engine 值。 */
export function tabEngine(tab: Engine): string {
  return tab === 'postgresql' ? 'postgres' : tab
}

/** 台账 engine 值 → 页签键。 */
export function engineTab(engine?: string): Engine {
  if (engine === 'postgres') return 'postgresql'
  if (engine === 'mongodb') return 'mongodb'
  return 'mysql'
}
