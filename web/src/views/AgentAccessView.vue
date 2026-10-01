<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '../api'
import type { AgentTokenInfo } from '../api'

const info = ref<AgentTokenInfo | null>(null)
const loading = ref(true)
const rotating = ref(false)
const error = ref('')
const copied = ref('')
const serverMode = ref(false)

const mcpURL = computed(() => info.value ? `${window.location.origin}${info.value.mcp_url}` : '')
const codexConfig = computed(() => `[mcp_servers.ssh_bridge]
url = "${mcpURL.value}"
bearer_token_env_var = "SSH_BRIDGE_AGENT_TOKEN"
enabled = true
tool_timeout_sec = 70`)
const tokenCommand = computed(() => info.value?.token ? `export SSH_BRIDGE_AGENT_TOKEN='${info.value.token}'` : '')

async function load() {
  loading.value = true
  error.value = ''
  try {
    serverMode.value = (await api.systemInfo()).mode === 'server'
    if (!serverMode.value) info.value = await api.agentToken()
  }
  catch (cause) { error.value = cause instanceof Error ? cause.message : '加载失败' }
  finally { loading.value = false }
}

async function copy(value: string, label: string) {
  try {
    await navigator.clipboard.writeText(value)
    copied.value = label
    window.setTimeout(() => { if (copied.value === label) copied.value = '' }, 1600)
  } catch { error.value = '复制失败，请手动选择文本' }
}

async function regenerate() {
  if (!window.confirm('重新生成后，当前 Token 会立即失效。确定继续吗？')) return
  rotating.value = true
  error.value = ''
  try { info.value = await api.regenerateAgentToken() }
  catch (cause) { error.value = cause instanceof Error ? cause.message : '重新生成失败' }
  finally { rotating.value = false }
}

onMounted(load)
</script>

<template>
  <section class="page-intro">
    <div><h2>Agent 接入</h2><p>{{ serverMode ? 'Server 模式使用具名 Agent 凭据和主机授权' : '连接 MCP Server，并管理本机 Agent 使用的访问 Token' }}</p></div>
  </section>

  <div v-if="loading" class="panel empty-state">正在加载…</div>
  <div v-else-if="serverMode" class="panel empty-state">具名凭据管理接口已可用；创建、授权与轮换的页面将在下一阶段加入。当前可通过管理 API 验收。</div>
  <p v-else-if="error && !info" class="form-error panel">{{ error }}</p>
  <div v-else-if="info" class="access-layout">
    <section class="panel access-section">
      <div class="section-title">
        <div><h3>访问 Token</h3><p>用于 Agent 调用 HTTP API 和 MCP Server</p></div>
        <span class="token-status"><i></i>已启用</span>
      </div>

      <div v-if="info.token_visible" class="token-notice">
        <span>!</span>
        <p><strong>请现在复制保存</strong>完整 Token 只在首次生成或重新生成后展示一次，离开页面后将不再显示。</p>
      </div>
      <div v-else class="info-banner">
        <span>i</span><p>Token 已安全保存在本机数据目录中。为了避免泄露，页面不会再次读取并展示原值；如已遗失，可以重新生成。</p>
      </div>

      <div class="token-field">
        <code>{{ info.token_visible ? info.token : '•••••••••••••••••••••••••••••••••••••••••••' }}</code>
        <button v-if="info.token_visible" class="secondary-button" @click="copy(info.token, 'token')">{{ copied === 'token' ? '已复制' : '复制 Token' }}</button>
      </div>
      <div class="access-actions">
        <small>重新生成后，使用旧 Token 的 Agent 会立即无法调用。</small>
        <button class="danger-ghost" :disabled="rotating" @click="regenerate">{{ rotating ? '正在生成…' : '重新生成 Token' }}</button>
      </div>
      <p v-if="error" class="form-error">{{ error }}</p>
    </section>

    <section class="panel access-section">
      <div class="section-title"><div><h3>MCP Server</h3><p>支持 Streamable HTTP，复用现有执行与审计能力</p></div></div>
      <div class="access-row"><span>连接地址</span><code>{{ mcpURL }}</code><button class="text-button" @click="copy(mcpURL, 'url')">{{ copied === 'url' ? '已复制' : '复制' }}</button></div>
      <div class="tool-chips"><span>list_targets</span><span>execute_command</span><span>get_execution</span><span>wait_execution</span><span>download_output</span></div>
    </section>

    <section class="panel access-section config-section">
      <div class="section-title"><div><h3>Codex 配置</h3><p>添加到 ~/.codex/config.toml，Token 通过环境变量提供</p></div><button class="secondary-button" @click="copy(codexConfig, 'config')">{{ copied === 'config' ? '已复制' : '复制配置' }}</button></div>
      <pre>{{ codexConfig }}</pre>
      <template v-if="info.token_visible">
        <div class="section-title command-title"><div><h3>设置 Token</h3><p>在启动 Agent 的环境中设置</p></div><button class="secondary-button" @click="copy(tokenCommand, 'command')">{{ copied === 'command' ? '已复制' : '复制命令' }}</button></div>
        <pre>{{ tokenCommand }}</pre>
      </template>
    </section>
  </div>
</template>
