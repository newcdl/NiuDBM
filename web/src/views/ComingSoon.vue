<script setup lang="ts">
defineProps<{ name: string }>()
</script>

<template>
  <div class="empty">
    <div class="icon">◫</div>
    <strong>{{ name }}尚未开发</strong>
    <p>
      骨架版本只实现「概览」与「平台自检」。数据库实例、任务中心与设置将在平台能力验证通过后按方案逐步落地。
    </p>
  </div>
</template>

<style scoped>
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 6px;
  max-width: 420px;
  margin: 12vh auto 0;
  text-align: center;
}

.icon {
  font-size: 28px;
  color: var(--fg-muted);
  opacity: 0.5;
}

.empty strong {
  font-size: 14px;
}

.empty p {
  margin: 0;
  color: var(--fg-muted);
  font-size: 12px;
  line-height: 1.7;
}
</style>
