import type { AuditSession, Execution, Target } from './types'

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/api/v1${path}`, { credentials: 'same-origin', headers: { 'Content-Type': 'application/json', ...init?.headers }, ...init })
  if (!response.ok) {
    const body = await response.json().catch(() => ({ error: `请求失败 (${response.status})` }))
    throw new Error(body.error || `请求失败 (${response.status})`)
  }
  if (response.status === 204) return undefined as T
  return response.json()
}

export const api = {
  systemInfo: () => request<SystemInfo>('/system/info'),
  targets: () => request<{ items: Target[] }>('/targets'),
  createTarget: (body: object) => request<Target>('/targets', { method: 'POST', body: JSON.stringify(body) }),
  updateTarget: (id: string, body: object) => request<Target>(`/targets/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
  deleteTarget: (id: string) => request<void>(`/targets/${id}`, { method: 'DELETE' }),
  testTarget: (body: object) => request<ConnectionTest>('/targets/test', { method: 'POST', body: JSON.stringify(body) }),
  testSavedTarget: (id: string) => request<ConnectionTest>(`/targets/${id}/test`, { method: 'POST' }),
  sessions: () => request<{ items: AuditSession[] }>('/sessions'),
  session: (id: string) => request<{ session: AuditSession; executions: Execution[] }>(`/sessions/${id}`),
  executions: () => request<{ items: Execution[] }>('/executions'),
  execution: (sessionId: string, executionId: string) => request<Execution>(`/sessions/${sessionId}/executions/${executionId}`),
  agentToken: () => request<AgentTokenInfo>('/agent-token'),
  regenerateAgentToken: () => request<AgentTokenInfo>('/agent-token/regenerate', { method: 'POST' }),
}

export interface ConnectionTest { success: boolean; duration_ms: number; message: string }
export interface AgentTokenInfo { configured: boolean; token: string; token_visible: boolean; mcp_url: string }
export interface SystemInfo { mode: 'local' | 'server'; database: 'sqlite' | 'postgresql' | 'mysql'; mock: boolean; openapi_url: string; mcp_url: string }

export function formatTime(value: string | null) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'medium', hour12: false }).format(new Date(value))
}
export function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}
export function duration(start: string | null, finish: string | null) {
  if (!start) return '—'
  const ms = (finish ? new Date(finish) : new Date()).getTime() - new Date(start).getTime()
  return ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`
}
