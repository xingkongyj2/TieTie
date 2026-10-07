import type { WechatSubscription } from '../api/wechat-subscription'

export interface WechatSubscriptionState {
  subscription: WechatSubscription | null
  loading: boolean
  error: string
}

/** One account/token owns the state; all consumers share its request and snapshot. */
export function createWechatSubscriptionController(read: () => Promise<WechatSubscription>, isCurrent: () => boolean) {
  let state: WechatSubscriptionState = { subscription: null, loading: false, error: '' }
  let revision = 0
  let inFlight: Promise<WechatSubscription | null> | null = null
  const listeners = new Set<() => void>()
  const publish = (next: WechatSubscriptionState) => {
    state = next
    for (const listener of [...listeners]) listener()
  }
  return {
    isCurrent,
    getSnapshot: () => state,
    subscribe: (listener: () => void) => {
      listeners.add(listener)
      return () => { listeners.delete(listener) }
    },
    refresh(): Promise<WechatSubscription | null> {
      if (!isCurrent()) return Promise.resolve(null)
      if (inFlight) return inFlight
      const requestRevision = revision
      publish({ ...state, loading: true, error: '' })
      const request = Promise.resolve().then(read).then(subscription => {
        if (!isCurrent() || requestRevision !== revision) return null
        publish({ subscription, loading: false, error: '' })
        return subscription
      }).catch(error => {
        if (isCurrent() && requestRevision === revision) {
          publish({ ...state, loading: false, error: error instanceof Error ? error.message : '暂时无法获取微信提醒状态。' })
        }
        return null
      }).finally(() => {
        if (inFlight === request) inFlight = null
      })
      inFlight = request
      return request
    },
    update(subscription: WechatSubscription) {
      if (!isCurrent()) return
      // An older GET must never undo a successfully saved subscription result.
      revision++
      publish({ subscription, loading: false, error: '' })
    },
  }
}

export type WechatSubscriptionController = ReturnType<typeof createWechatSubscriptionController>
