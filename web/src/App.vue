<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { api, type SystemInfo } from './api'

const route = useRoute()
const router = useRouter()
const mobileNavOpen = ref(false)
const logoutError = ref('')
const systemInfo = ref<SystemInfo>({ mode: 'local', database: 'sqlite', mock: false, openapi_url: '/api/openapi.json', mcp_url: '/mcp' })

onMounted(async () => {
  try {
    systemInfo.value = await api.systemInfo()
  } catch {
    // Keep the local defaults while the service is temporarily unavailable.
  }
})

const modeTitle = computed(() => systemInfo.value.mode === 'server' ? '服务端模式' : '本地模式')
const databaseLabel = computed(() => ({ sqlite: 'SQLite', postgresql: 'PostgreSQL', mysql: 'MySQL' })[systemInfo.value.database])
const modeBadge = computed(() => `${systemInfo.value.mode.toUpperCase()} · ${databaseLabel.value}`)
const modeStorage = computed(() => `${databaseLabel.value} · 本地文件`)

const pageTitle = computed(() => {
  if (route.path.startsWith('/sessions/')) return '会话详情'
  if (route.path === '/sessions') return '会话记录'
  if (route.path.startsWith('/executions/')) return '执行详情'
  if (route.path === '/executions') return '执行记录'
  if (route.path === '/targets') return '目标主机'
  if (route.path === '/agent-access') return 'Agent 接入'
  return 'SSH Bridge'
})

async function logout() {
  logoutError.value = ''
  try { await api.logout(); await router.replace('/login') }
  catch (cause) { logoutError.value = cause instanceof Error ? cause.message : '退出失败' }
}
</script>

<template>
  <RouterView v-if="route.path === '/login'" />
  <div v-else class="app-shell">
    <aside class="sidebar" :class="{ open: mobileNavOpen }">
      <div class="brand">
        <div class="brand-mark">S</div>
        <div>
          <strong>SSH Bridge</strong>
          <span>AI 操作审计</span>
        </div>
      </div>

      <nav class="nav-list" @click="mobileNavOpen = false">
        <RouterLink to="/sessions" class="nav-item">
          <span class="nav-icon">◫</span><span>会话记录</span>
        </RouterLink>
        <RouterLink to="/executions" class="nav-item">
          <span class="nav-icon">⌁</span><span>全部执行</span>
        </RouterLink>
        <RouterLink to="/targets" class="nav-item">
          <span class="nav-icon">▣</span><span>目标主机</span>
        </RouterLink>
        <RouterLink to="/agent-access" class="nav-item">
          <span class="nav-icon">⌘</span><span>Agent 接入</span>
        </RouterLink>
      </nav>

      <div class="sidebar-footer">
        <div class="mode-card">
          <span class="live-dot"></span>
          <div><strong>{{ modeTitle }}</strong><small>{{ modeStorage }}</small></div>
        </div>
        <div class="user-card">
          <div class="avatar">A</div>
          <div><strong>admin</strong><span>系统管理员</span></div>
          <button v-if="systemInfo.mode === 'server'" class="logout-button" @click="logout">退出</button>
        </div>
        <small v-if="logoutError" class="logout-error">{{ logoutError }}</small>
      </div>
    </aside>

    <button v-if="mobileNavOpen" class="nav-backdrop" @click="mobileNavOpen = false" aria-label="关闭菜单"></button>

    <main class="main-area">
      <header class="topbar">
        <button class="mobile-menu" @click="mobileNavOpen = true" aria-label="打开菜单">☰</button>
        <div>
          <h1>{{ pageTitle }}</h1>
          <p>查看 AI 通过 SSH Bridge 发起的实际操作</p>
        </div>
        <div class="top-actions">
          <span class="mock-pill">{{ modeBadge }}</span>
          <button class="icon-button" title="通知">◌<span class="notification-dot"></span></button>
        </div>
      </header>

      <div class="page-container">
        <RouterView />
      </div>
    </main>
  </div>
</template>
