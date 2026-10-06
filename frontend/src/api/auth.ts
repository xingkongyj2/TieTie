import Taro from '@tarojs/taro'
import { request } from './client'
import { completeWechatLogin } from './wechat-login'

const fallbackWechatAvatars = [
  '/avatars/cream-cat.png', '/avatars/peach-cat.png', '/avatars/golden-longhair-cat.png',
  '/avatars/zodiac-rabbit.png', '/avatars/corgi-dog.png', '/avatars/otter.png', '/avatars/penguin.png',
]
const randomFallbackWechatAvatar = () => fallbackWechatAvatars[Math.floor(Math.random() * fallbackWechatAvatars.length)]

export interface Binding {
  sessionId: string
  partnerId: number
}

export interface User {
  userId: number
  username: string
  code: string
}

/** Optional display data returned by wx.getUserProfile. It is never used as
 * authentication proof; the server still verifies the one-time login code. */
export interface WechatProfile {
  nickname: string
  avatarUrl: string
}

export interface AccountResult {
  token?: string
  user: User
  binding: Binding | null
  isNewUser?: boolean
  wechatProfile?: WechatProfile
}

/** The account hook commits a login token only if its request is still current. */
export const authApi = {
  async wechatLogin(): Promise<AccountResult> {
    if (process.env.TARO_ENV !== 'weapp') throw new Error('请在微信小程序中使用微信登录。')
    return completeWechatLogin({
      // This call stays inside the button's user gesture. completeWechatLogin
      // treats a refusal as optional and continues with wx.login.
      getProfile: async () => {
        try {
          const result = await Taro.getUserProfile({ desc: '用于完善你的贴贴资料' })
          return { nickname: String(result.userInfo?.nickName ?? '').trim(), avatarUrl: String(result.userInfo?.avatarUrl ?? '').trim() }
        } catch {
          // Keep a profile object in the request so the server can persist a
          // different local default for each newly created account.
          return { nickname: '', avatarUrl: randomFallbackWechatAvatar() }
        }
      },
      login: () => Taro.login({ timeout: 10_000 }),
      exchange: (code, profile) => request<AccountResult>('/api/auth/wechat', { method: 'POST', body: { code, nickname: profile?.nickname, avatarUrl: profile?.avatarUrl } }),
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
