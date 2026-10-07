import Taro from '@tarojs/taro'
import { request } from './client'
import { completeWechatLogin, startWechatLogin, type WechatLoginProfile } from './wechat-login'
import { readWechatAvatar } from '../lib/wechat-avatar'

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
  isNewUser?: boolean
  needsProfileSetup?: boolean
}

/** The account hook commits a login token only if its request is still current. */
export const authApi = {
  async wechatLogin(profile?: WechatLoginProfile): Promise<AccountResult> {
    if (process.env.TARO_ENV !== 'weapp') throw new Error('请在微信小程序中使用微信登录。')
    if (!profile) return startWechatLogin({
      login: () => Taro.login({ timeout: 10_000 }),
      exchange: code => request<AccountResult>('/api/auth/wechat', { method: 'POST', body: { code } }),
    })
    return completeWechatLogin(profile, {
      readAvatar: readWechatAvatar,
      login: () => Taro.login({ timeout: 10_000 }),
      exchange: (code, selected) => request<AccountResult>('/api/auth/wechat', { method: 'POST', body: { code, ...selected } }),
    })
  },
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
