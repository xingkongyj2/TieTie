import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useSyncExternalStore } from 'react'
import { wechatSubscriptionApi } from '../api/wechat-subscription'
import { isAppVisible, onAppVisibilityChange } from '../lib/platform'
import { getToken } from '../lib/token'
import { createWechatSubscriptionController, type WechatSubscriptionController } from '../lib/wechatSubscriptionState'

export const WechatSubscriptionContext = createContext<WechatSubscriptionController | null>(null)

export function useWechatSubscriptionState(controller: WechatSubscriptionController) {
  return useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot)
}

export function useWechatSubscription(accountId: number | undefined, enabled: boolean, showingSettings: boolean) {
  const token = getToken()
  const controller = useMemo(() => createWechatSubscriptionController(
    () => wechatSubscriptionApi.get(),
    () => enabled && accountId !== undefined && token !== null && getToken() === token,
  ), [accountId, enabled, token])
  const state = useWechatSubscriptionState(controller)
  const deliveryRefresh = useRef<ReturnType<typeof setTimeout> | undefined>()
  useEffect(() => () => clearTimeout(deliveryRefresh.current), [controller])
  const refreshAfterReminder = useCallback(() => {
    void controller.refresh()
    clearTimeout(deliveryRefresh.current)
    // The push worker polls every 5 seconds, after the chat message has arrived.
    deliveryRefresh.current = setTimeout(() => {
      if (isAppVisible()) void controller.refresh()
    }, 6_000)
  }, [controller])
  useEffect(() => {
    if (!enabled || accountId === undefined) return
    void controller.refresh()
    return onAppVisibilityChange(visible => { if (visible) void controller.refresh() })
  }, [controller, enabled, accountId])
  useEffect(() => {
    if (!enabled || !showingSettings || accountId === undefined) return
    void controller.refresh()
    // Delivery runs after chat persistence; keep visible counts current when it finishes.
    const timer = setInterval(() => { if (isAppVisible()) void controller.refresh() }, 15_000)
    return () => clearInterval(timer)
  }, [controller, enabled, accountId, showingSettings])
  return { ...state, controller, refreshAfterReminder }
}

export function useSharedWechatSubscription() {
  const controller = useContext(WechatSubscriptionContext)
  if (!controller) throw new Error('微信提醒状态尚未初始化。')
  return { ...useWechatSubscriptionState(controller), controller }
}
