import { request } from './client'
import { setToken } from '../lib/token'

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

/** 账号系统：注册/登录保存 JWT，me/bind 走鉴权接口。 */
export const authApi = {
  async register(username: string, password: string): Promise<AccountResult> {
    const result = await request<AccountResult>('/api/auth/register', { method: 'POST', body: { username, password } })
    if (result.token) setToken(result.token)
    return result
  },
  async login(username: string, password: string): Promise<AccountResult> {
    const result = await request<AccountResult>('/api/auth/login', { method: 'POST', body: { username, password } })
    if (result.token) setToken(result.token)
    return result
  },
  me(): Promise<AccountResult> {
    return request('/api/account/me')
  },
  /** 首次绑定会在云端新建专属会话，可能较慢。 */
  bind(code: string): Promise<AccountResult> {
    return request('/api/account/bind', { method: 'POST', body: { code }, timeoutMs: 90_000 })
  },
}
