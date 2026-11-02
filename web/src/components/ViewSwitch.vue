<script setup lang="ts">
import { Grid, List } from '@element-plus/icons-vue'
import { setViewMode, viewMode } from '../view-mode'

function onChange(value: string | number | boolean): void {
  if (value === 'table' || value === 'card') setViewMode(value)
}
</script>

<template>
  <!-- 所有列表共用：切了以后全站一致，下次进来还是这个形态 -->
  <el-radio-group :model-value="viewMode" size="small" class="view-switch" @update:model-value="onChange">
    <el-radio-button value="table" title="表格视图">
      <el-icon><List /></el-icon>
    </el-radio-button>
    <el-radio-button value="card" title="卡片视图">
      <el-icon><Grid /></el-icon>
    </el-radio-button>
  </el-radio-group>
</template>

<style scoped>
.view-switch :deep(.el-radio-button__inner) {
  display: inline-flex;
  align-items: center;
  padding: 7px 10px;
}
</style>
