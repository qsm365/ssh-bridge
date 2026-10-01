import { createRouter, createWebHistory } from 'vue-router'
import ExecutionsView from './views/ExecutionsView.vue'
import ExecutionDetailView from './views/ExecutionDetailView.vue'
import TargetsView from './views/TargetsView.vue'
import SessionsView from './views/SessionsView.vue'
import SessionDetailView from './views/SessionDetailView.vue'
import AgentAccessView from './views/AgentAccessView.vue'
import LoginView from './views/LoginView.vue'
import { api } from './api'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/executions' },
    { path: '/login', component: LoginView },
    { path: '/sessions', component: SessionsView },
    { path: '/sessions/:id', component: SessionDetailView },
    { path: '/executions', component: ExecutionsView },
    { path: '/executions/:id', component: ExecutionDetailView },
    { path: '/targets', component: TargetsView },
    { path: '/agent-access', component: AgentAccessView },
    { path: '/settings', redirect: '/agent-access' },
  ],
})

router.beforeEach(async to => {
  let mode: 'local' | 'server'
  try { mode = (await api.systemInfo()).mode }
  catch { return to.path === '/login' ? true : { path: '/login', query: { redirect: to.fullPath } } }
  if (mode === 'local') return to.path === '/login' ? { path: '/sessions' } : true
  try {
    await api.authMe()
    return to.path === '/login' ? { path: '/sessions' } : true
  } catch {
    return to.path === '/login' ? true : { path: '/login', query: { redirect: to.fullPath } }
  }
})

export default router
