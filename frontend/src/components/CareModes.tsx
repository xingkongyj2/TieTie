import { ArrowUpRight, Clock3, MapPin, Moon, Sun } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '../api/client'
import { careApi, type CareState, type CareMode } from '../api/care'
import { TimePicker } from './TimePicker'
import { AnniversaryReminders } from './AnniversaryReminders'
import { TabLoading } from './TabLoading'
import './CareModes.css'

export function CareWithAnniversary({ sessionId, selfId, onEditRegion, onBind, notify }: {
  sessionId?: string; selfId?: number; onEditRegion: () => void; onBind: () => void; notify: (text: string) => void
}) {
  const [loading, setLoading] = useState(true)
  const [state, setState] = useState<CareState | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<string | null>(null)
  const [editingTime, setEditingTime] = useState<CareMode | null>(null)
  const sequence = useRef(0)
  const mutating = useRef(false)
  const mounted = useRef(true)
  const reload = useCallback(async () => {
    if (!sessionId || mutating.current) return
    const seq = ++sequence.current
    try {
      const result = await careApi.get(sessionId)
      if (mounted.current && seq === sequence.current) { setState(result); setError('') }
    } catch (e) { if (mounted.current && seq === sequence.current) setError(e instanceof Error ? e.message : '天气关怀暂时没加载出来。') }
    finally { if (mounted.current && seq === sequence.current) setLoading(false) }
  }, [sessionId])
  useEffect(() => {
    mounted.current = true
    setLoading(true)
    void reload()
    const timer = setInterval(() => { void reload() }, 30_000)
    const focus = () => { void reload() }
    window.addEventListener('focus', focus)
    return () => { mounted.current = false; sequence.current++; clearInterval(timer); window.removeEventListener('focus', focus) }
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
        setState(result)
        notify(timeOnly ? `提醒时间已改为 ${time}。` : enabled ? `${mode.mode === 'morning' ? '早安' : '晚安'}提醒已开启，每天 ${time} 发到群里。` : '已关闭这个模式。')
      }
      return true
    } catch (e) {
      const regionGap = e instanceof ApiError && e.code === 'care_region_required'
      notify(regionGap ? (selfMissing ? '还没开启：先填写你的地区。' : `还没开启：请${missingLabel}在「我的 → 关于我 → 编辑资料」填写地区。`) : e instanceof Error ? e.message : '保存失败，请再试一次。')
      if (regionGap && selfMissing) onEditRegion()
      return false
    }
    finally { mutating.current = false; if (mounted.current) setBusy(null); void reload() }
  }
  if (!sessionId) return <div className="care-region-guide"><MapPin size={18} /><p>绑定两人空间后，就能一起收到早安和晚安提醒。</p><button type="button" onClick={onBind}>去绑定<ArrowUpRight size={14} /></button></div>
  if (loading) return <TabLoading />
  const modes: CareMode[] = state?.modes ?? [
    { mode: 'morning', enabled: false, time: '08:00', nextDue: '', state: 'off' },
    { mode: 'night', enabled: false, time: '21:00', nextDue: '', state: 'off' },
  ]
  return <div className="form-card settings-card" aria-label="提醒偏好">
    <div className="care-modes">
      {error && <p className="care-status" role="alert">{error}<button type="button" className="care-retry" onClick={() => void reload()}>重试</button></p>}
      {modes.map((mode) => {
        const night = mode.mode === 'night'
        const time = mode.time
        const title = night ? '晚安提醒' : '早安提醒'
        return <section className="setting-row care-mode-row" key={mode.mode} aria-label={`${title}设置`}>
          <span className="setting-icon">{night ? <Moon size={18} /> : <Sun size={18} />}</span>
          <div className="setting-copy"><div className="care-mode-title"><strong>{title}</strong><button type="button" className="care-time" aria-label={`${title}时间 ${state ? time : '尚未读取'}`} aria-haspopup="dialog" disabled={!state || busy !== null} onClick={() => setEditingTime(mode)}><Clock3 size={10} aria-hidden="true" /><span>{state ? time : '--:--'}</span></button></div><p>{night ? '看明天天气，把穿搭和出门准备好' : '今天的天气，还有要记得的小事'}</p>{mode.lastError && <p className="care-status" role="alert">{mode.lastError}</p>}</div>
          <button type="button" role="switch" className={`toggle ${mode.enabled ? 'is-on' : ''}`} aria-label={title} aria-checked={mode.enabled} disabled={!state || busy !== null} onClick={() => void save(mode, !mode.enabled)}><span /></button>
        </section>
      })}
      {editingTime && <TimePicker title={`${editingTime.mode === 'night' ? '晚安' : '早安'}提醒时间`} value={editingTime.time}
        onClose={() => setEditingTime(null)} onConfirm={(time) => time === editingTime.time ? Promise.resolve(true) : save(editingTime, editingTime.enabled, time, true)} />}
    </div>
    <AnniversaryReminders key={`anniversary:${sessionId}`} sessionId={sessionId} onBind={onBind} notify={notify} />
  </div>
}
