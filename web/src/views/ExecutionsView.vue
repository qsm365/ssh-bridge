<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import StatusBadge from '../components/StatusBadge.vue'
import { api, duration, formatBytes, formatTime } from '../api'
import type { Execution, ExecutionStatus } from '../types'
const executions=ref<Execution[]>([]);const keyword=ref('');const status=ref<'all'|ExecutionStatus>('all');const loading=ref(true);const error=ref('')
const filtered=computed(()=>executions.value.filter(e=>(!keyword.value||[e.target_name,e.command,e.title,e.id].some(v=>v.toLowerCase().includes(keyword.value.toLowerCase())))&&(status.value==='all'||e.status===status.value)))
const successRate=computed(()=>{const done=executions.value.filter(e=>['succeeded','failed','timeout'].includes(e.status));return done.length?`${Math.round(done.filter(e=>e.status==='succeeded').length/done.length*100)}%`:'—'})
onMounted(async()=>{try{executions.value=(await api.executions()).items??[]}catch(cause){error.value=cause instanceof Error?cause.message:'加载失败'}finally{loading.value=false}})
</script>
<template>
  <section class="stats-grid"><article class="stat-card"><div class="stat-icon blue">⌁</div><div><span>最近执行</span><strong>{{ executions.length }}</strong><small>最多显示 100 条</small></div></article><article class="stat-card"><div class="stat-icon green">✓</div><div><span>成功率</span><strong>{{ successRate }}</strong><small>已结束执行</small></div></article><article class="stat-card"><div class="stat-icon amber">!</div><div><span>异常</span><strong>{{ executions.filter(e=>e.status==='failed'||e.status==='timeout').length }}</strong><small>失败或超时</small></div></article><article class="stat-card"><div class="stat-icon violet">▤</div><div><span>归档输出</span><strong>{{ formatBytes(executions.reduce((n,e)=>n+e.output_size,0)) }}</strong><small>本地文件存储</small></div></article></section>
  <section class="panel execution-panel"><div class="panel-heading"><div><h2>最近执行</h2><p>展示 Agent 实际发送到目标主机的命令与执行状态</p></div></div>
    <div class="filter-bar"><label class="search-box"><span>⌕</span><input v-model="keyword" placeholder="搜索主机、命令、用途或执行 ID" /></label><select v-model="status" class="select-control"><option value="all">全部状态</option><option value="succeeded">成功</option><option value="failed">失败</option><option value="running">执行中</option><option value="pending">等待中</option><option value="timeout">已超时</option></select></div>
    <div class="table-wrap"><table><thead><tr><th>执行信息</th><th>目标主机</th><th>开始时间</th><th>耗时</th><th>状态</th><th></th></tr></thead><tbody>
      <tr v-for="item in filtered" :key="item.id"><td class="execution-main"><RouterLink :to="`/executions/${item.id}`">{{ item.title || '未命名执行' }}</RouterLink><code>{{ item.command }}</code><span>#{{ item.id.slice(-10) }}<span v-if="item.agent_credential_name"> · Agent：{{ item.agent_credential_name }}</span></span></td><td><strong class="cell-primary">{{ item.target_name }}</strong></td><td><span class="cell-primary normal">{{ formatTime(item.started_at || item.created_at) }}</span></td><td><span class="cell-primary normal">{{ duration(item.started_at,item.finished_at) }}</span></td><td><StatusBadge :status="item.status" /></td><td><RouterLink :to="`/executions/${item.id}`" class="row-arrow">›</RouterLink></td></tr>
      <tr v-if="loading||error||!filtered.length"><td colspan="6" class="empty-state">{{ loading?'正在加载…':error||'没有找到执行记录' }}</td></tr>
    </tbody></table></div><div class="panel-footer"><span>显示 {{ filtered.length }} 条记录</span></div>
  </section>
</template>
