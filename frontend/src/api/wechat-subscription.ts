import { request } from './client'

export interface WechatSubscription {
  enabled: boolean
  templateId: string
  hasWechatIdentity: boolean
  remaining: number
  subscriptionType: 'once' | 'permanent'
}
export interface SubscriptionResult {
  templateId: string
  result: 'accept' | 'reject' | 'ban'
  requestId: string
}

export const wechatSubscriptionApi = {
  get(): Promise<WechatSubscription> { return request('/api/account/wechat-subscription') },
  record(result: SubscriptionResult): Promise<WechatSubscription> {
    return request('/api/account/wechat-subscription', { method: 'POST', body: result })
  },
}
