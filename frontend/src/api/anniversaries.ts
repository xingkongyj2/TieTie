import { request } from './client'

export interface Anniversary {
  id: string
  title: string
  date: string
  kind: 'together' | 'birthday' | 'wedding' | 'first_meet' | 'other'
  pinned: boolean
  createdAt: string
  updatedAt: string
}
export interface AnniversaryResult {
  anniversaries: Anniversary[]
  featured: Anniversary | null
  nextCursor: string
  spaceCreatedAt: string
  removedIds?: string[]
  deletionCursor?: string
  resetRequired?: boolean
}
const path = (session: string) => `/api/qoder/sessions/${encodeURIComponent(session)}/anniversaries`
export const anniversariesApi = {
  list(session: string, after = '', afterDeletion = ''): Promise<AnniversaryResult> {
    const query = new URLSearchParams()
    if (after) query.set('after', after)
    if (afterDeletion) query.set('afterDeletion', afterDeletion)
    return request(`${path(session)}${query.size ? `?${query}` : ''}`)
  },
  pin(session: string, id: string, pinned: boolean): Promise<{ anniversary: Anniversary }> {
    return request(`${path(session)}/${encodeURIComponent(id)}`, { method: 'PATCH', body: { pinned } })
  },
}
