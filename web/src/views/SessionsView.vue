<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { api, formatTime } from '../api'
import type { AuditSession } from '../types'

const sessions=ref<AuditSession[]>([]);const keyword=ref('');const loading=ref(true);const error=ref('')
const filtered=computed(()=>sessions.value.filter(s=>!keyword.value||[s.title,s.id].some(v=>v.toLowerCase().includes(keyword.value.toLowerCase()))))
const totalExecutions=computed(()=>sessions.value.reduce((sum,s)=>sum+s.execution_count,0))
onMounted(async()=>{try{sessions.value=(await api.sessions()).items??[]}catch(cause){error.value=cause instanceof Error?cause.message:'加载失败'}finally{loading.value=false}})
</script>
<template>
  <section class="stats-grid session-stats">
    <article class="stat-card"><div class="stat-icon blue">◫</div><div><span>最近会话</span><strong>{{ sessions.length }}</strong><small>最近 100 个会话</small></div></article>
    <article class="stat-card"><div class="stat-icon green">⌁</div><div><span>SSH 调用</span><strong>{{ totalExecutions }}</strong><small>以上会话中的执行</small></div></article>
    <article class="stat-card"><div class="stat-icon amber">!</div><div><span>有异常</span><strong>{{ sessions.filter(s=>s.has_error).length }}</strong><small>存在失败或超时执行</small></div></article>
  </section>
  <section class="panel session-panel">
    <div class="panel-heading"><div><h2>Agent 会话</h2><p>按一次 AI 对话聚合其中产生的所有 SSH 操作</p></div></div>
    <div class="filter-bar"><label class="search-box"><span>⌕</span><input v-model="keyword" placeholder="搜索会话标题或会话 ID" /></label></div>
    <div v-if="loading" class="empty-state">正在加载…</div><div v-else-if="error" class="empty-state">{{ error }}</div><div v-else-if="!filtered.length" class="empty-state">尚无会话记录</div>
    <div v-else class="session-list">
      <RouterLink v-for="session in filtered" :key="session.id" :to="`/sessions/${session.id}`" class="session-row">
        <div class="session-state" :class="{ attention: session.has_error }"><i></i></div>
        <div class="session-copy"><div class="session-title-line"><h3>{{ session.title || '未命名会话' }}</h3><span v-if="session.has_error" class="session-status attention">有异常</span></div><p>{{ session.id }}</p></div>
        <div class="session-metric"><strong>{{ session.execution_count }}</strong><span>次执行</span></div><div class="session-time"><strong>{{ formatTime(session.updated_at) }}</strong><span>最近活动</span></div><span class="row-arrow">›</span>
      </RouterLink>
    </div>
  </section>
</template>
