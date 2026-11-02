import { computed, ref, watch, type Ref } from 'vue'

/**
 * 前端分页：这些列表的数据都在一次请求里拿全了（服务、用户、备份、任务、操作记录），
 * 所以切片在前端做，翻页不重新请求。搜索条件一变就回到第一页，
 * 否则会出现"筛完只剩 2 条，却停在第 5 页，看起来像没数据"。
 */
export function usePager<T>(source: Ref<T[]>, defaultSize = 10) {
  const page = ref(1)
  const pageSize = ref(defaultSize)

  const total = computed(() => source.value.length)
  const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)))

  const items = computed(() => {
    const start = (page.value - 1) * pageSize.value
    return source.value.slice(start, start + pageSize.value)
  })

  watch([total, pageSize], () => {
    if (page.value > pages.value) page.value = pages.value
  })

  watch(source, () => {
    page.value = 1
  })

  return { page, pageSize, total, pages, items }
}
