export type ExecutionStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'timeout'

export interface Execution {
  id: string
  session_id: string
  target_id: string
  target_name: string
  agent_credential_id?: string
  agent_credential_name?: string
  title: string
  command: string
  working_dir: string
  status: ExecutionStatus
  exit_code: number | null
  error_message?: string
  output_size: number
  output_preview: string
  created_at: string
  started_at: string | null
  finished_at: string | null
  artifacts?: Artifact[]
}

export interface Artifact {
  id: string
  execution_id: string
  placeholder: string
  original_name: string
  remote_path?: string
  size: number
  sha256: string
  created_at: string
}

export interface Target {
  id: string
  name: string
  host: string
  port: number
  ssh_user: string
  auth_method: 'key' | 'password'
  private_key_path?: string
  password_configured: boolean
  host_key_fingerprint: string
  description: string
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface AuditSession {
  id: string
  title: string
  agent_credential_id?: string
  agent_credential_name?: string
  created_at: string
  updated_at: string
  execution_count: number
  has_error: boolean
}
