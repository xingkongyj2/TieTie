import { request } from './client'

export interface Countdown {
 id: string; title: string; date: string; repeat: 'annual' | 'once'; kind: 'birthday' | 'deadline' | 'other'
 nextDate: string; daysRemaining: number; expired: boolean; leapAdjusted: boolean; createdAt: string
}
const path = (session: string) => `/api/qoder/sessions/${encodeURIComponent(session)}/countdowns`
export const countdownApi = {
 list: (session: string, after = ''): Promise<{ items: Countdown[]; nextCursor: string }> => request(`${path(session)}${after ? `?after=${encodeURIComponent(after)}` : ''}`),
}
