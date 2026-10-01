import { request } from './client'

export interface Impression {
  targetId: number
  summary: string
  status: 'pending' | 'generating' | 'ready' | 'failed'
  error?: string
  generatedAt?: string
}
export interface ImpressionResult { impression: Impression; memoryStatus?: 'synced' | 'pending' | '' }
const path = (sessionId: string) => `/api/qoder/sessions/${encodeURIComponent(sessionId)}/partner-impression`
export const impressionApi = {
  get: (sessionId: string, signal?: AbortSignal, retry = false) => request<ImpressionResult>(`${path(sessionId)}${retry ? '?retry=true' : ''}`, { signal }),
  supplement: (sessionId: string, text: string, requestId: string) => request<ImpressionResult>(path(sessionId), { method: 'POST', body: { text, requestId }, timeoutMs: 45_000 }),
}
