<script setup lang="ts">
import type { ExecutionStatus } from '../types'

defineProps<{ status: ExecutionStatus }>()

const labels: Record<ExecutionStatus, string> = {
  pending: '等待中',
  succeeded: '成功',
  failed: '失败',
  running: '执行中',
  timeout: '已超时',
}
</script>

<template>
  <span class="status-badge" :class="`status-${status}`">
    <span class="status-dot"></span>{{ labels[status] }}
  </span>
</template>
