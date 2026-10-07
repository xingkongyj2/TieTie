import { AbortController } from '../lib/abort'
import { isAppVisible, onAppVisibilityChange } from '../lib/platform'
import { Textarea, Form, SubmitButton } from './Fields';
import { Textarea as NativeTextarea } from '@tarojs/components'
import { RefreshCw, Sparkles, ArrowUp } from './Icons'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { impressionApi, type Impression } from '../api/impression'
import type { Member } from '../types'
import './PartnerImpression.css'

interface Props { member: Member; sessionId?: string; notify: (text: string) => void }

const beijingDate = (value?: string): string => {
  const timestamp = value ? Date.parse(value) : NaN
  if (!Number.isFinite(timestamp)) return ''
  const date = new Date(timestamp + 8 * 60 * 60 * 1000)
  return `${date.getUTCFullYear()}.${String(date.getUTCMonth() + 1).padStart(2, '0')}.${String(date.getUTCDate()).padStart(2, '0')}`
}

export function PartnerImpression({ member, sessionId, notify }: Props) {
  const [impression, setImpression] = useState<Impression | null>(null)
  const [draft, setDraft] = useState('')
  const [inputFocused, setInputFocused] = useState(false)
  const [saving, setSaving] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [saveError, setSaveError] = useState('')
  const [memoryPending, setMemoryPending] = useState(false)
  const [refresh, setRefresh] = useState(0)
  const requestKey = useRef<{ text: string; key: string } | null>(null)
  const mounted = useRef(false)
  const saveLock = useRef(false)
  const version = useRef(0)
  const retryRequested = useRef(false)
  const processing = impression?.status === 'pending' || impression?.status === 'generating'
  const scheduled = processing && Date.parse(impression?.nextAnalysisAt || '') > Date.now()
  const generatedDate = beijingDate(impression?.generatedAt)
  const reload = (retry = false) => {
    retryRequested.current = retry
    setLoading(true)
    setRefresh((value) => value + 1)
  }

  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  useEffect(() => {
    if (!sessionId) { setLoading(false); return }
    const controller = new AbortController()
    const ownVersion = ++version.current
    let timer: ReturnType<typeof setTimeout> | undefined
    let polling = false
    const load = async (retry: boolean) => {
      if (controller.signal.aborted || ownVersion !== version.current || !isAppVisible() || polling) return
      polling = true
      try {
        const result = await impressionApi.get(sessionId, controller.signal, retry)
        if (controller.signal.aborted || ownVersion !== version.current) return
        setImpression(result.impression)
        setError('')
        setLoading(false)
        if (result.impression.status === 'pending' || result.impression.status === 'generating') {
          const nextAnalysisAt = Date.parse(result.impression.nextAnalysisAt || '')
          // A same-day analysis in another space is queued for tomorrow. Read
          // once at that boundary instead of polling a deliberately idle job.
          const delay = nextAnalysisAt > Date.now() ? Math.min(nextAnalysisAt - Date.now() + 250, 2_147_483_647) : 2500
          timer = setTimeout(() => void load(false), delay)
        }
      } catch (e) {
        if (controller.signal.aborted || ownVersion !== version.current) return
        setLoading(false)
        setError(e instanceof Error ? e.message : '印象暂时没加载出来，请再试一次。')
      } finally { polling = false }
    }
    const retry = retryRequested.current
    retryRequested.current = false
    void load(retry)
    const unsubscribe = onAppVisibilityChange(() => {
      if (timer) clearTimeout(timer)
      if (isAppVisible()) void load(false)
    })
    return () => { controller.abort(); if (timer) clearTimeout(timer); unsubscribe() }
  }, [sessionId, refresh])

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const text = draft.trim()
    if (!sessionId || !text || saveLock.current) return
    if (requestKey.current?.text !== text) requestKey.current = { text, key: `note_${Date.now()}_${Math.random().toString(36).slice(2)}` }
    saveLock.current = true
    setSaving(true)
    setSaveError('')
    ++version.current // A poll started before this save cannot replace its result.
    try {
      const result = await impressionApi.supplement(sessionId, text, requestKey.current.key)
      if (!mounted.current) return
      setDraft('')
      requestKey.current = null
      setImpression(result.impression)
      setMemoryPending(result.memoryStatus === 'pending')
      setRefresh((value) => value + 1)
      notify(result.impression.status === 'ready' || Date.parse(result.impression.nextAnalysisAt || '') > Date.now()
        ? '已记住，印象会在明天更新。'
        : result.memoryStatus === 'pending' ? '已保存，记忆同步中。' : '已记住，正在整理印象。')
    } catch (e) {
      if (mounted.current) {
        setSaveError(e instanceof Error ? e.message : '补充还没保存好，请重试。')
        // Retain the draft and the same idempotency key after an uncertain send.
        setRefresh((value) => value + 1)
      }
    } finally { saveLock.current = false; if (mounted.current) setSaving(false) }
  }

  if (!sessionId) return <section className="impression-card"><p>绑定两人空间后查看印象。</p></section>
  return <div className="partner-impression">
    <section className="impression-card" aria-labelledby="impression-title" aria-busy={loading || (processing && !scheduled)}>
      <div className="impression-heading"><h2 id="impression-title"><Sparkles className="impression-title-icon" size={19} aria-hidden="true" /><span>贴贴眼中的{member.name}</span></h2><button type="button" className="impression-refresh" aria-label="刷新印象" disabled={saving || loading || (processing && !scheduled)} onClick={() => reload(impression?.status === 'failed')}><RefreshCw size={15} aria-hidden="true" /></button></div>
      <p className="impression-update-note">{impression?.status === 'ready' && generatedDate ? `${generatedDate} 更新 · 每日更新一次` : '每日更新一次'}</p>
      {impression?.summary && <p className="impression-summary">{impression.summary}</p>}
      {(loading || (processing && !scheduled)) && <div className="impression-loading" role="status"><span className="typing-dots"><i /><i /><i /></span>{loading ? '正在加载印象…' : impression?.summary ? '正在更新印象…' : '正在整理印象…'}</div>}
      {!loading && scheduled && <p className="impression-update-note" role="status">新空间的印象会在明天更新。</p>}
      {!loading && impression?.status === 'failed' && <div className="impression-problem" role="alert"><p>{impression.error || '印象整理失败。'}</p><button type="button" disabled={saving} onClick={() => reload(true)}>重试</button></div>}
      {error && <div className="impression-problem" role="alert"><p>{error}</p><button type="button" disabled={saving} onClick={() => reload(impression?.status === 'failed')}>重试</button></div>}
    </section>
    <Form className="impression-supplement" onSubmit={(event) => void submit(event)}>
      <label htmlFor="impression-input">补充关于{member.name}的信息</label>
      {process.env.TARO_ENV === 'weapp' ? <div className={`impression-input-frame${inputFocused ? ' is-focused' : ''}`}>
        <NativeTextarea id="impression-input" className="h5-textarea impression-textarea" maxlength={2000} disabled={saving} value={draft}
          placeholder="习惯、爱好、工作…" placeholderStyle="color:#b0b9c8;font-size:13px;line-height:24px;"
          adjustPosition={false} onInput={(event) => setDraft(event.detail.value)}
          onFocus={() => setInputFocused(true)} onBlur={() => setInputFocused(false)} />
      </div> : <Textarea id="impression-input" rows={4} maxLength={2000} disabled={saving} value={draft} placeholder="习惯、爱好、工作…" onChange={(event) => setDraft(event.target.value)} />}
      <div className="impression-input-footer"><SubmitButton className="primary-button"  disabled={saving || !draft.trim()}>{saving ? '正在记住…' : '告诉贴贴'}<ArrowUp size={15} aria-hidden="true" /></SubmitButton></div>
      {saveError && <p className="impression-problem" role="alert">{saveError} 草稿已保留。</p>}
      {memoryPending && <p className="impression-sync" role="status">已保存，记忆同步中。</p>}
    </Form>
  </div>
}
