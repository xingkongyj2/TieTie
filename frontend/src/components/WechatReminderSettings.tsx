import Taro from '@tarojs/taro'
import { useEffect, useRef, useState } from 'react'
import { ApiError } from '../api/client'
import { wechatSubscriptionApi, type SubscriptionResult, type WechatSubscription } from '../api/wechat-subscription'
import { readStorage, removeStorage, writeStorage } from '../lib/storage'
import { wechatSubscriptionErrorMessage } from '../lib/wechatSubscriptionError'
import { useSharedWechatSubscription } from '../hooks/useWechatSubscription'
import { Bell, CircleHelp } from './Icons'

interface Props {
  notify: (text: string) => void
  accountId?: number
  onSubscriptionChange?: (subscription: WechatSubscription) => void
  onExplain: (type: WechatSubscription['subscriptionType']) => void
}

const pendingKey = (accountId: number | undefined, templateId: string) => `tietie.wechat-subscription.pending.v1:${accountId ?? 'unknown'}:${templateId}`

/** Authorization is requested directly from this user click, never on page load. */
export function WechatReminderSettings({ notify, accountId, onSubscriptionChange, onExplain }: Props) {
  const { subscription, loading, error: refreshError, controller } = useSharedWechatSubscription()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const mounted = useRef(true)
  const submitting = useRef(false)
  const pending = useRef<SubscriptionResult | null>(null)
  const owner = useRef(controller)
  owner.current = controller
  const current = () => mounted.current && owner.current === controller && controller.isCurrent()

  useEffect(() => {
    mounted.current = true
    submitting.current = false
    pending.current = null
    setBusy(false)
    setError('')
    return () => { mounted.current = false }
  }, [controller])
  useEffect(() => {
    if (!subscription?.templateId || !current()) return
    const saved = readStorage<SubscriptionResult | null>(pendingKey(accountId, subscription.templateId), () => null)
    if (saved?.templateId === subscription.templateId && saved.requestId) pending.current = saved
  }, [controller, accountId, subscription?.templateId])

  const subscribe = async () => {
    if (submitting.current || !current() || !subscription?.enabled || !subscription.hasWechatIdentity) return
    submitting.current = true
    setBusy(true); setError('')
    try {
      if (!pending.current) {
        // Taro's shared declaration also requires Alipay fields; WeChat uses tmplIds.
        const result = await Taro.requestSubscribeMessage({ tmplIds: [subscription.templateId] } as Taro.requestSubscribeMessage.Option)
        if (!current()) return
        const choice = (result as unknown as Record<string, unknown>)[subscription.templateId]
        const normalizedChoice = choice === 'acceptWithAudio' ? 'accept' : choice
        if (normalizedChoice !== 'accept' && normalizedChoice !== 'reject' && normalizedChoice !== 'ban') throw new Error('微信订阅未完成，请再试一次。')
        pending.current = {
          templateId: subscription.templateId, result: normalizedChoice,
          requestId: `wxsub_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 14)}`,
        }
        writeStorage(pendingKey(accountId, subscription.templateId), pending.current)
      }
      const choice = pending.current.result
      const saved = await wechatSubscriptionApi.record(pending.current)
      if (!current()) return
      pending.current = null
      removeStorage(pendingKey(accountId, saved.templateId))
      controller.update(saved)
      onSubscriptionChange?.(saved)
      notify(choice === 'accept' ? '微信提醒已订阅' : '这次未订阅微信提醒')
    } catch (e) {
      if (!current()) return
      if (e instanceof ApiError && e.status >= 400 && e.status < 500 && e.status !== 408 && e.status !== 429) {
        pending.current = null
        if (subscription?.templateId) removeStorage(pendingKey(accountId, subscription.templateId))
      }
      setError(wechatSubscriptionErrorMessage(e))
    } finally {
      if (current()) { submitting.current = false; setBusy(false) }
    }
  }

  const count = subscription ? subscription.remaining < 0 ? '不限' : `${subscription.remaining} 次` : '—'
  const buttonDisabled = busy || loading || !subscription?.enabled || !subscription.hasWechatIdentity

  return <section className="mine-wechat-reminders" aria-label="微信提醒">
    <div className="wechat-reminder-row">
      <div className="wechat-reminder-copy"><span className="wechat-reminder-label">提醒剩余次数<button type="button" className="wechat-reminder-help" aria-label="了解微信提醒次数" onClick={() => onExplain(subscription?.subscriptionType ?? 'once')}><CircleHelp size={13} strokeWidth={1.7} aria-hidden="true" /></button></span><strong>{count}</strong></div>
      <button type="button" className={`wechat-reminder-button${buttonDisabled ? ' is-disabled' : ''}`} aria-disabled={buttonDisabled} aria-busy={busy} aria-label="开启微信提醒" onClick={() => { if (!buttonDisabled) void subscribe() }}>
        <span className="wechat-reminder-mark" aria-hidden="true"><Bell size={17} /></span>
      </button>
    </div>
    {(error || refreshError) && <p className="mine-wechat-reminder-error" role="alert">{error || refreshError}</p>}
  </section>
}
