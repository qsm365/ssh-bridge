<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api, formatTime } from '../api'
import type { AgentCredential } from '../api'
import type { Target } from '../types'

const credentials = ref<AgentCredential[]>([])
const targets = ref<Target[]>([])
const loading = ref(true)
const error = ref('')
const saving = ref(false)
const workingId = ref('')
const formOpen = ref(false)
const editingId = ref('')
const name = ref('')
const selectedTargets = ref<string[]>([])
const enabled = ref(true)
const revealedToken = ref('')
const revealedName = ref('')
const copied = ref(false)
const mcpURL = `${window.location.origin}/mcp`

const selectedCount = computed(() => selectedTargets.value.length)
const targetNames = (ids: string[]) => ids.map(id => targets.value.find(target => target.id === id)?.name || '已删除的目标主机').join('、') || '未授权主机'

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [credentialData, targetData] = await Promise.all([api.agentCredentials(), api.targets()])
    credentials.value = credentialData.items ?? []
    targets.value = targetData.items ?? []
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '加载失败' }
  finally { loading.value = false }
}

function openCreate() {
  editingId.value = ''
  name.value = ''
  selectedTargets.value = []
  enabled.value = true
  error.value = ''
  formOpen.value = true
}

function openEdit(credential: AgentCredential) {
  editingId.value = credential.id
  name.value = credential.name
  selectedTargets.value = credential.target_ids.filter(id => targets.value.some(target => target.id === id))
  enabled.value = credential.enabled
  error.value = ''
  formOpen.value = true
}

async function save() {
  saving.value = true
  error.value = ''
  const body = { name: name.value.trim(), target_ids: selectedTargets.value, enabled: enabled.value }
  try {
    if (editingId.value) await api.updateAgentCredential(editingId.value, body)
    else {
      const issued = await api.createAgentCredential(body)
      revealedToken.value = issued.token
      revealedName.value = body.name
      copied.value = false
    }
    formOpen.value = false
    await load()
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '保存失败' }
  finally { saving.value = false }
}

async function rotate(credential: AgentCredential) {
  if (!window.confirm(`重新生成“${credential.name}”的 Token 后，旧 Token 会立即失效。确定继续吗？`)) return
  workingId.value = credential.id
  error.value = ''
  try {
    const issued = await api.regenerateAgentCredential(credential.id)
    revealedToken.value = issued.token
    revealedName.value = credential.name
    copied.value = false
    await load()
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '轮换失败' }
  finally { workingId.value = '' }
}

async function toggle(credential: AgentCredential) {
  if (credential.enabled && !window.confirm(`停用“${credential.name}”后，该 Token 将立即无法调用。确定继续吗？`)) return
  workingId.value = credential.id
  error.value = ''
  try {
    await api.updateAgentCredential(credential.id, { name: credential.name, target_ids: credential.target_ids.filter(id => targets.value.some(target => target.id === id)), enabled: !credential.enabled })
    await load()
  } catch (cause) { error.value = cause instanceof Error ? cause.message : '更新失败' }
  finally { workingId.value = '' }
}

async function copyToken() {
  try { await navigator.clipboard.writeText(revealedToken.value); copied.value = true }
  catch { error.value = '复制失败，请手动选择 Token' }
}

onMounted(load)
</script>

<template>
  <div class="credential-layout">
    <section v-if="revealedToken" class="panel access-section credential-reveal">
      <div class="section-title"><div><h3>{{ revealedName }} · 新 Token</h3><p>完整 Token 只在本次创建或轮换后展示，刷新页面后无法再次查看。</p></div><button class="text-button" @click="revealedToken = ''">关闭</button></div>
      <div class="token-notice"><span>!</span><p><strong>请现在复制并妥善保存</strong>不要将 Token 写入代码库、日志或共享文档。</p></div>
      <div class="token-field"><code>{{ revealedToken }}</code><button class="secondary-button" @click="copyToken">{{ copied ? '已复制' : '复制 Token' }}</button></div>
    </section>

    <section class="panel credential-panel">
      <div class="panel-heading"><div><h2>具名 Agent 凭据</h2><p>每个凭据仅能使用分配的目标主机；历史审计记录保留凭据归属</p></div><button class="primary-button" @click="openCreate">＋ 创建凭据</button></div>
      <div v-if="loading" class="empty-state">正在加载…</div>
      <div v-else-if="!credentials.length" class="empty-state">尚未创建 Agent 凭据</div>
      <div v-else class="credential-list">
        <article v-for="credential in credentials" :key="credential.id" class="credential-row">
          <div class="credential-info"><div class="credential-heading"><h3>{{ credential.name }}</h3><span class="credential-state" :class="{ disabled: !credential.enabled }">{{ credential.enabled ? '已启用' : '已停用' }}</span></div><p>{{ credential.id }} · Token {{ credential.token_prefix }}…</p><small>授权：{{ targetNames(credential.target_ids) }}</small><small>最后使用：{{ formatTime(credential.last_used_at || null) }}</small></div>
          <div class="credential-actions"><button class="secondary-button" :disabled="workingId === credential.id" @click="openEdit(credential)">编辑权限</button><button class="secondary-button" :disabled="workingId === credential.id" @click="rotate(credential)">轮换 Token</button><button class="danger-ghost" :disabled="workingId === credential.id" @click="toggle(credential)">{{ credential.enabled ? '停用' : '启用' }}</button></div>
        </article>
      </div>
    </section>

    <section class="panel access-section"><div class="section-title"><div><h3>MCP Server</h3><p>Agent 使用上述具名 Token 连接；先调用 list_targets 查看获授权主机。</p></div></div><div class="access-row"><span>连接地址</span><code>{{ mcpURL }}</code></div></section>
    <p v-if="error && !formOpen" class="form-error">{{ error }}</p>
  </div>

  <div v-if="formOpen" class="modal-layer" @click.self="formOpen = false">
    <form class="modal credential-modal" @submit.prevent="save">
      <div class="modal-heading"><div><h2>{{ editingId ? '编辑 Agent 凭据' : '创建 Agent 凭据' }}</h2><p>为凭据分配可访问的目标主机</p></div><button type="button" @click="formOpen = false">×</button></div>
      <div class="form-grid"><label class="full"><span>凭据名称</span><input v-model="name" maxlength="255" placeholder="例如：运维排障 Agent" required /></label></div>
      <div class="credential-target-heading"><strong>授权目标主机</strong><span>已选择 {{ selectedCount }} 台</span></div>
      <div v-if="!targets.length" class="credential-no-targets">暂无目标主机，可以先保存空权限凭据。</div>
      <div v-else class="credential-target-list"><label v-for="target in targets" :key="target.id"><input v-model="selectedTargets" type="checkbox" :value="target.id" /><span><strong>{{ target.name }}</strong><small>{{ target.ssh_user }}@{{ target.host }}:{{ target.port }}{{ target.enabled ? '' : ' · 主机已停用' }}</small></span></label></div>
      <label class="credential-enabled"><input v-model="enabled" type="checkbox" /><span>启用此凭据</span></label>
      <p v-if="error" class="form-error">{{ error }}</p>
      <div class="modal-actions"><button type="button" class="secondary-button" @click="formOpen = false">取消</button><button class="primary-button" :disabled="saving">{{ saving ? '正在保存…' : editingId ? '保存修改' : '创建并显示 Token' }}</button></div>
    </form>
  </div>
</template>
