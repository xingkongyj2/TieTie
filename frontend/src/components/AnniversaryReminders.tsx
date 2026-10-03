import { Cake } from 'lucide-react'
import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { anniversaryRemindersApi, type AnniversaryReminderSettings } from '../api/anniversaries'

export function AnniversaryReminders({ sessionId, onBind, notify }: { sessionId?: string; onBind: () => void; notify: (text: string) => void }) {
  const [settings, setSettings] = useState<AnniversaryReminderSettings | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const descriptionId = useId()
  const mounted = useRef(false)
  const mutating = useRef(false)
  const sequence = useRef(0)
  const reload = useCallback(async () => {
    if (!sessionId || mutating.current) return
    const seq = ++sequence.current
    try {
      const result = await anniversaryRemindersApi.get(sessionId)
      if (mounted.current && seq === sequence.current) { setSettings(result); setError('') }
    } catch (problem) {
      if (mounted.current && seq === sequence.current) setError(problem instanceof Error ? problem.message : '纪念日提醒设置暂时没加载出来。')
    }
  }, [sessionId])
  useEffect(() => {
    mounted.current = true
    void reload()
    const refresh = () => { if (document.visibilityState === 'visible') void reload() }
    const timer = setInterval(refresh, 15_000)
    window.addEventListener('focus', refresh)
    document.addEventListener('visibilitychange', refresh)
    return () => { mounted.current = false; sequence.current++; clearInterval(timer); window.removeEventListener('focus', refresh); document.removeEventListener('visibilitychange', refresh) }
  }, [reload])
  const toggle = async () => {
    if (!sessionId || !settings || mutating.current) return
    mutating.current = true
    const seq = ++sequence.current
    setBusy(true)
    try {
      const result = await anniversaryRemindersApi.save(sessionId, !settings.enabled)
      if (mounted.current && seq === sequence.current) {
        setSettings(result); setError('')
        notify(result.enabled ? '纪念日提醒已开启，每年提前 3 天 08:00 发到群里。' : '纪念日提醒已关闭。')
      }
    } catch (problem) {
      if (mounted.current && seq === sequence.current) notify(problem instanceof Error ? problem.message : '纪念日提醒没保存成功，请再试一次。')
    } finally {
      mutating.current = false
      if (mounted.current) { setBusy(false); void reload() }
    }
  }
  return <div className="anniversary-reminders">
    <div className="setting-row">
      <span className="setting-icon"><Cake size={18} /></span>
      <div className="setting-copy"><strong>纪念日提醒</strong><p id={descriptionId}>{!sessionId ? '绑定两人空间后，一起收到纪念日提示' : '每年提前 3 天，08:00 提醒我们'}</p>{!sessionId && <button type="button" className="care-time" onClick={onBind}>去绑定</button>}</div>
      <button type="button" className={`toggle ${settings?.enabled ? 'is-on' : ''}`} role="switch" aria-checked={settings?.enabled ?? false} aria-label="纪念日提醒" aria-describedby={descriptionId} disabled={!sessionId || !settings || busy} onClick={() => void toggle()}><span /></button>
    </div>
    {error && <div className="cloud-error" role="alert"><span>{error}</span><button type="button" disabled={busy} onClick={() => void reload()}>重试</button></div>}
  </div>
}
