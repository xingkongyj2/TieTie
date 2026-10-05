import { request } from './client'
import type { AISettings } from '../types'

interface Result { settings: Pick<AISettings, 'tone'>; memoryStatus?: 'pending' | 'synced' }
const path = (sessionId: string) => `/api/qoder/sessions/${encodeURIComponent(sessionId)}/assistant-settings`
export const assistantSettingsApi = {
  get(sessionId: string): Promise<Result> { return request(path(sessionId)) },
  save(sessionId: string, tone: AISettings['tone']): Promise<Result> {
    return request(path(sessionId), { method: 'PUT', body: { tone } })
  },
}
