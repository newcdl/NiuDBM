<script setup lang="ts">
/**
 * 列表翻页条。用原生控件而不是 el-pagination：
 * 这一版的按钮/下拉全是自绘的，混进一套 Element Plus 分页会显得突兀。
 */
const props = defineProps<{
  page: number
  pageSize: number
  total: number
  sizes?: number[]
}>()

const emit = defineEmits<{
  'update:page': [number]
  'update:pageSize': [number]
}>()

const SIZES = props.sizes ?? [10, 20, 50]

function pages(): number {
  return Math.max(1, Math.ceil(props.total / props.pageSize))
}

function go(next: number): void {
  const max = pages()
  const target = Math.min(Math.max(1, next), max)
  if (target !== props.page) emit('update:page', target)
}

function changeSize(event: Event): void {
  const value = Number((event.target as HTMLSelectElement).value)
  emit('update:pageSize', value)
  emit('update:page', 1)
}
</script>

<template>
  <div v-if="total > 0" class="pager">
    <span class="muted small">共 {{ total }} 条</span>
    <div class="grow" />
    <select class="select size" :value="pageSize" aria-label="每页条数" @change="changeSize">
      <option v-for="s in SIZES" :key="s" :value="s">{{ s }} 条/页</option>
    </select>
    <button type="button" class="btn btn-sm" :disabled="page <= 1" @click="go(page - 1)">上一页</button>
    <span class="muted small num">{{ page }} / {{ pages() }}</span>
    <button type="button" class="btn btn-sm" :disabled="page >= pages()" @click="go(page + 1)">下一页</button>
  </div>
</template>

<style scoped>
.pager {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 10px 2px 2px;
}

.grow {
  flex: 1 1 auto;
}

.size {
  width: auto;
  min-width: 104px;
}

.num {
  min-width: 52px;
  text-align: center;
}

@media (max-width: 720px) {
  .pager {
    gap: 6px;
  }

  .pager .btn {
    flex: 1 1 auto;
  }
}
</style>
