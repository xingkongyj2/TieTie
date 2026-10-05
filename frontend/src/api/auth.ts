import { request } from './client'

export interface Binding {
  sessionId: string
  partnerId: number
}

export interface User {
  userId: number
  username: string
  code: string
}

export interface AccountResult {
  token?: string
  user: User
  binding: Binding | null
}

/** The account hook commits a login token only if its request is still current. */
export const authApi = {
  async register(username: string, password: string): Promise<AccountResult> {
    const result = await request<AccountResult>('/api/auth/register', { method: 'POST', body: { username, password } })
    return result
  },
  async login(username: string, password: string): Promise<AccountResult> {
    const result = await request<AccountResult>('/api/auth/login', { method: 'POST', body: { username, password } })
    return result
  },
  me(): Promise<AccountResult> {
    return request('/api/account/me')
  },
  /** 首次绑定会在云端新建专属会话，可能较慢。 */
  bind(code: string): Promise<AccountResult> {
    return request('/api/account/bind', { method: 'POST', body: { code }, timeoutMs: 90_000 })
  },
  /** 退出当前会话：删除绑定记录，云端会话和历史都保留。 */
  unbind(): Promise<AccountResult> {
    return request('/api/account/unbind', { method: 'POST' })
  },
}
