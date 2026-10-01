<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import StatusBadge from '../components/StatusBadge.vue'
import { api, duration, formatBytes, formatTime } from '../api'
import type { AuditSession, Execution } from '../types'
const route=useRoute();const session=ref<AuditSession|null>(null);const executions=ref<Execution[]>([]);const loading=ref(true);const error=ref('')
onMounted(async()=>{try{const data=await api.session(String(route.params.id));session.value=data.session;executions.value=data.executions}catch(cause){error.value=cause instanceof Error?cause.message:'加载失败'}finally{loading.value=false}})
</script>
<template>
  <div class="detail-toolbar"><RouterLink to="/sessions" class="back-link">‹ 返回会话记录</RouterLink></div>
  <div v-if="loading" class="panel empty-state">正在加载…</div><div v-else-if="error" class="panel empty-state">{{ error }}</div>
  <template v-else-if="session">
    <section class="detail-hero session-hero"><div><div class="eyebrow">会话 #{{ session.id.slice(-10) }}</div><h2>{{ session.title || '未命名会话' }}</h2><p>{{ session.id }}</p></div><span v-if="session.has_error" class="session-status attention">有异常</span></section>
    <section class="session-summary-grid"><div><span>开始时间</span><strong>{{ formatTime(session.created_at) }}</strong></div><div><span>最近活动</span><strong>{{ formatTime(session.updated_at) }}</strong></div><div><span>SSH 调用</span><strong>{{ session.execution_count }} 次</strong></div><div v-if="session.agent_credential_name"><span>调用 Agent</span><strong>{{ session.agent_credential_name }}</strong></div></section>
    <section class="panel conversation-note"><span>关于会话标题</span><p>{{ session.title || 'Agent 未提供会话标题' }}</p><small>标题由调用方选填；SSH Bridge 不保存用户与 AI 的完整聊天内容。</small></section>
    <section class="panel session-executions"><div class="panel-heading"><div><h2>本次会话的执行时间线</h2><p>按照 Bridge 实际接收和执行的时间排序</p></div></div>
      <div v-if="!executions.length" class="empty-state">尚无执行记录</div><div v-else class="conversation-timeline">
        <article v-for="(execution,index) in executions" :key="execution.id"><div class="timeline-index">{{ index+1 }}</div><div class="timeline-card-content">
          <div class="execution-line-head"><RouterLink :to="`/executions/${execution.id}`">{{ execution.title||'未命名执行' }}</RouterLink><StatusBadge :status="execution.status" /></div><code>{{ execution.command }}</code>
          <div class="execution-line-meta"><span>{{ execution.target_name }}</span><span>{{ formatTime(execution.started_at||execution.created_at) }}</span><span>{{ duration(execution.started_at,execution.finished_at) }}</span><span v-if="execution.artifacts?.length">{{ execution.artifacts.length }} 个输入文件</span></div>
          <div class="session-result" :class="{error:execution.status==='failed'||execution.status==='timeout'}"><div class="session-result-head"><span>执行结果</span><span>退出码 {{ execution.exit_code??'—' }} · {{ formatBytes(execution.output_size) }}</span></div><pre>{{ execution.output_preview||execution.error_message||'命令没有产生输出' }}</pre></div>
          <RouterLink :to="`/executions/${execution.id}`" class="full-result-link">查看完整执行记录 ›</RouterLink>
        </div></article>
      </div>
    </section>
  </template>
</template>
