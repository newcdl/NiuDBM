/** 静态资源在开发服务器使用站点根目录，在飞牛网关下使用后端注入的 base。 */
export function assetUrl(path: string): string {
  const base = document.querySelector('base')?.href ?? `${window.location.origin}/`
  return new URL(path, base).href
}
