<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import StatusBadge from '../components/StatusBadge.vue'
import { api, duration, formatBytes, formatTime } from '../api'
import type { Execution } from '../types'
const route=useRoute();const execution=ref<Execution|null>(null);const loading=ref(true);const error=ref('')
const output=computed(()=>execution.value?.output_preview||execution.value?.error_message||'命令没有产生输出')
onMounted(async()=>{try{execution.value=((await api.executions()).items??[]).find(e=>e.id===route.params.id)||null;if(!execution.value)error.value='执行记录不存在'}catch(cause){error.value=cause instanceof Error?cause.message:'加载失败'}finally{loading.value=false}})
async function copyCommand(){if(execution.value)await navigator.clipboard.writeText(execution.value.command)}
function placeholderLabel(name:string){return `{{file:${name}}}`}
</script>
<template>
  <div class="detail-toolbar"><RouterLink to="/executions" class="back-link">‹ 返回执行记录</RouterLink></div>
  <div v-if="loading" class="panel empty-state">正在加载…</div><div v-else-if="error" class="panel empty-state">{{ error }}</div>
  <template v-else-if="execution">
    <section class="detail-hero"><div><div class="eyebrow">执行 #{{ execution.id.slice(-10) }}</div><h2>{{ execution.title||'未命名执行' }}</h2><p>{{ execution.id }}</p></div><StatusBadge :status="execution.status" /></section>
    <div class="detail-grid"><div class="detail-main">
      <section class="panel detail-section"><div class="section-title"><h3>实际执行命令</h3><button class="text-button" @click="copyCommand">复制</button></div><div class="command-block"><span>$</span><code>{{ execution.command }}</code></div><div class="metadata-grid"><div><span>工作目录</span><strong>{{ execution.working_dir||'默认目录' }}</strong></div><div><span>退出码</span><strong>{{ execution.exit_code??'—' }}</strong></div><div><span>输出大小</span><strong>{{ formatBytes(execution.output_size) }}</strong></div><div><span>总耗时</span><strong>{{ duration(execution.started_at,execution.finished_at) }}</strong></div></div></section>
      <section v-if="execution.artifacts?.length" class="panel detail-section"><div class="section-title"><div><h3>输入文件</h3><p>命令通过占位符引用，远端路径由系统生成</p></div></div><div v-for="artifact in execution.artifacts" :key="artifact.id" class="artifact-row"><div class="file-icon">FILE</div><div class="artifact-info"><strong>{{ artifact.original_name }}</strong><span>{{ placeholderLabel(artifact.placeholder) }} · {{ formatBytes(artifact.size) }}</span></div><code>SHA256 {{ artifact.sha256.slice(0,12) }}…</code><a class="text-button" :href="`/api/v1/executions/${execution.id}/artifacts/${artifact.id}`">下载</a></div></section>
      <section class="panel output-section"><div class="output-tabs"><button class="active">输出预览</button><span></span><a class="output-action" :href="`/api/v1/executions/${execution.id}/output`">下载完整输出</a></div><pre>{{ output }}</pre><div class="output-footer">当前展示最多 64 KiB，完整 stdout/stderr 已按顺序归档</div></section>
    </div><aside class="detail-side"><section class="panel side-card"><h3>执行信息</h3><dl><div><dt>目标主机</dt><dd>{{ execution.target_name }}</dd></div><div v-if="execution.agent_credential_name"><dt>调用 Agent</dt><dd>{{ execution.agent_credential_name }}</dd></div><div><dt>会话 ID</dt><dd>{{ execution.session_id }}</dd></div><div><dt>创建时间</dt><dd>{{ formatTime(execution.created_at) }}</dd></div><div><dt>开始时间</dt><dd>{{ formatTime(execution.started_at) }}</dd></div><div><dt>结束时间</dt><dd>{{ formatTime(execution.finished_at) }}</dd></div><div v-if="execution.error_message"><dt>错误信息</dt><dd>{{ execution.error_message }}</dd></div></dl></section></aside></div>
  </template>
</template>
