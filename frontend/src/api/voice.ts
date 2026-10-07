import type { AbortSignalLike } from '../lib/abort'
import { request } from './client'

export interface VoiceSession {
  url: string
  voiceId: string
  expiresAt: string
}

export const voiceApi = {
  getSession: (signal?: AbortSignalLike) => request<VoiceSession>('/api/account/voice-session', {
    method: 'POST', signal, timeoutMs: 10_000,
  }),
}
