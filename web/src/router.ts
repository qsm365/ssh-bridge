import { createRouter, createWebHistory } from 'vue-router'
import ExecutionsView from './views/ExecutionsView.vue'
import ExecutionDetailView from './views/ExecutionDetailView.vue'
import TargetsView from './views/TargetsView.vue'
import SessionsView from './views/SessionsView.vue'
import SessionDetailView from './views/SessionDetailView.vue'
import AgentAccessView from './views/AgentAccessView.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/executions' },
    { path: '/login', redirect: '/sessions' },
    { path: '/sessions', component: SessionsView },
    { path: '/sessions/:id', component: SessionDetailView },
    { path: '/executions', component: ExecutionsView },
    { path: '/executions/:id', component: ExecutionDetailView },
    { path: '/targets', component: TargetsView },
    { path: '/agent-access', component: AgentAccessView },
    { path: '/settings', redirect: '/agent-access' },
  ],
})
