import { Clock3, Moon, Sun } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '../api/client'
import { careApi, type CareState, type CareMode } from '../api/care'
import { anniversaryRemindersApi, type AnniversaryReminderSettings } from '../api/anniversaries'
import { TimePicker } from './TimePicker'
import { AnniversaryReminders } from './AnniversaryReminders'
import { TabLoading } from './TabLoading'
import { EmptyTabState } from './EmptyTabState'
import './CareModes.css'

export function CareWithAnniversary({ sessionId, selfId, onEditRegion, notify }: {
  sessionId?: string; selfId?: number; onEditRegion: () => void; notify: (text: string) => void
}) {
  const [loading, setLoading] = useState(true)
  const [settings, setSettings] = useState<{ care: CareState; anniversary: AnniversaryReminderSettings } | null>(null)
  const state = settings?.care
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<string | null>(null)
  const [editingTime, setEditingTime] = useState<CareMode | null>(null)
  const sequence = useRef(0)
  const mutating = useRef(false)
  const mounted = useRef(true)
  const reload = useCallback(async () => {
    if (!sessionId || !mounted.current || mutating.current) return
    const seq = ++sequence.current
    try {
      const [care, anniversary] = await Promise.all([careApi.get(sessionId), anniversaryRemindersApi.get(sessionId)])
      if (mounted.current && seq === sequence.current) { setSettings({ care, anniversary }); setError('') }
    } catch (e) { if (mounted.current && seq === sequence.current) setError(e instanceof Error ? e.message : '提醒设置暂时没加载出来。') }
    finally { if (mounted.current && seq === sequence.current) setLoading(false) }
  }, [sessionId])
  useEffect(() => {
    mounted.current = true
    setLoading(true)
    void reload()
    const refresh = () => { if (document.visibilityState === 'visible') void reload() }
    const timer = setInterval(refresh, 15_000)
    window.addEventListener('focus', refresh)
    document.addEventListener('visibilitychange', refresh)
    return () => { mounted.current = false; sequence.current++; clearInterval(timer); window.removeEventListener('focus', refresh); document.removeEventListener('visibilitychange', refresh) }
  }, [reload])
  const missing = state?.members.filter((member) => !member.region?.cityCode) ?? []
  const selfMissing = missing.some((member) => member.userId === selfId)
  const missingLabel = missing.map((member) => member.userId === selfId ? '你' : member.name).join('和')
  const save = async (mode: CareMode, enabled: boolean, time = mode.time, timeOnly = false): Promise<boolean> => {
    if (!sessionId || mutating.current) return false
    mutating.current = true
    const seq = ++sequence.current
    setBusy(mode.mode)
    try {
      const result = await careApi.save(sessionId, { ...mode, enabled, time })
      if (mounted.current && seq === sequence.current) {
        setSettings(current => current ? { ...current, care: result } : current)
        setError('')
        notify(timeOnly ? `提醒时间已改为 ${time}。` : enabled ? `${mode.mode === 'morning' ? '早安' : '晚安'}提醒已开启，每天 ${time} 发到群里。` : '已关闭这个模式。')
      }
      return true
    } catch (e) {
      if (!mounted.current || seq !== sequence.current) return false
      const regionGap = e instanceof ApiError && e.code === 'care_region_required'
      notify(regionGap ? (selfMissing ? '还没开启：先填写你的地区。' : `还没开启：请${missingLabel}在「我的 → 关于我 → 编辑资料」填写地区。`) : e instanceof Error ? e.message : '保存失败，请再试一次。')
      if (regionGap && selfMissing) onEditRegion()
      return false
    }
    finally { mutating.current = false; if (mounted.current && seq === sequence.current) { setBusy(null); void reload() } }
  }
  const toggleAnniversary = async () => {
    if (!sessionId || !settings || mutating.current) return
    mutating.current = true
    const seq = ++sequence.current
    setBusy('anniversary')
    try {
      const result = await anniversaryRemindersApi.save(sessionId, !settings.anniversary.enabled)
      if (mounted.current && seq === sequence.current) {
        setSettings(current => current ? { ...current, anniversary: result } : current)
        setError('')
        notify(result.enabled ? '纪念日提醒已开启，每年提前 3 天 08:00 发到群里。' : '纪念日提醒已关闭。')
      }
    } catch (problem) {
      if (mounted.current && seq === sequence.current) notify(problem instanceof Error ? problem.message : '纪念日提醒没保存成功，请再试一次。')
    } finally {
      mutating.current = false
      if (mounted.current && seq === sequence.current) { setBusy(null); void reload() }
    }
  }
  if (!sessionId) return <EmptyTabState kind="care" title="还没有贴贴提醒" example="帮我们看看明天的天气" />
  if (loading) return <TabLoading />
  if (!settings || !state) return <div className="cloud-error" role="alert"><span>{error}</span><button type="button" onClick={() => { setLoading(true); void reload() }}>重试</button></div>
  if (!state.modes.length && !error) return <EmptyTabState kind="care" title="还没有贴贴提醒" example="帮我们看看明天的天气" />
  const modes = state.modes
  return <div className="form-card settings-card" aria-label="提醒偏好">
    <div className="care-modes">
      {error && <p className="care-status" role="alert">{error}<button type="button" className="care-retry" disabled={busy !== null} onClick={() => void reload()}>重试</button></p>}
      {modes.map((mode) => {
        const night = mode.mode === 'night'
        const time = mode.time
        const title = night ? '晚安提醒' : '早安提醒'
        return <section className="setting-row care-mode-row" key={mode.mode} aria-label={`${title}设置`}>
          <span className="setting-icon">{night ? <Moon size={18} /> : <Sun size={18} />}</span>
          <div className="setting-copy"><div className="care-mode-title"><strong>{title}</strong><button type="button" className="care-time" aria-label={`${title}时间 ${time}`} aria-haspopup="dialog" disabled={busy !== null} onClick={() => setEditingTime(mode)}><Clock3 size={10} aria-hidden="true" /><span>{time}</span></button></div><p>{night ? '看明天天气，把穿搭和出门准备好' : '今天的天气，还有要记得的小事'}</p>{mode.lastError && <p className="care-status" role="alert">{mode.lastError}</p>}</div>
          <button type="button" role="switch" className={`toggle ${mode.enabled ? 'is-on' : ''}`} aria-label={title} aria-checked={mode.enabled} disabled={busy !== null} onClick={() => void save(mode, !mode.enabled)}><span /></button>
        </section>
      })}
      {editingTime && <TimePicker title={`${editingTime.mode === 'night' ? '晚安' : '早安'}提醒时间`} value={editingTime.time}
        onClose={() => setEditingTime(null)} onConfirm={(time) => time === editingTime.time ? Promise.resolve(true) : save(editingTime, editingTime.enabled, time, true)} />}
    </div>
    <AnniversaryReminders settings={settings.anniversary} disabled={busy !== null} onToggle={() => void toggleAnniversary()} />
  </div>
}
