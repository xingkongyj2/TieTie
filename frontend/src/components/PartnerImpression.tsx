import { RefreshCw, Sparkles, ArrowUp } from 'lucide-react'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { impressionApi, type Impression } from '../api/impression'
import type { Member } from '../types'
import './PartnerImpression.css'

interface Props { member: Member; sessionId?: string; notify: (text: string) => void }

export function PartnerImpression({ member, sessionId, notify }: Props) {
  const [impression, setImpression] = useState<Impression | null>(null)
  const [draft, setDraft] = useState('')
  const [saving, setSaving] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [saveError, setSaveError] = useState('')
  const [memoryPending, setMemoryPending] = useState(false)
  const [refresh, setRefresh] = useState(0)
  const requestKey = useRef<{ text: string; key: string } | null>(null)
  const mounted = useRef(false)
  const version = useRef(0)
  const processing = impression?.status === 'pending' || impression?.status === 'generating'

  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  useEffect(() => {
    if (!sessionId) { setLoading(false); return }
    const controller = new AbortController()
    const ownVersion = ++version.current
    let timer: ReturnType<typeof setTimeout> | undefined
    const load = async (retry: boolean) => {
      try {
        const result = await impressionApi.get(sessionId, controller.signal, retry)
        if (controller.signal.aborted || ownVersion !== version.current) return
        setImpression(result.impression)
        setError('')
        setLoading(false)
        if (result.impression.status === 'pending' || result.impression.status === 'generating') timer = setTimeout(() => void load(false), 2500)
      } catch (e) {
        if (controller.signal.aborted || ownVersion !== version.current) return
        setLoading(false)
        setError(e instanceof Error ? e.message : '印象暂时没加载出来，请再试一次。')
      }
    }
    void load(refresh > 0)
    return () => { controller.abort(); if (timer) clearTimeout(timer) }
  }, [sessionId, refresh])

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const text = draft.trim()
    if (!sessionId || !text || saving) return
    if (requestKey.current?.text !== text) requestKey.current = { text, key: typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `note_${Date.now()}_${Math.random().toString(36).slice(2)}` }
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
      notify(result.memoryStatus === 'pending' ? '已保存，记忆同步中。' : '已记住，正在更新印象。')
    } catch (e) {
      if (mounted.current) {
        setSaveError(e instanceof Error ? e.message : '补充还没保存好，请重试。')
        // Retain the draft and the same idempotency key after an uncertain send.
        setRefresh((value) => value + 1)
      }
    } finally { if (mounted.current) setSaving(false) }
  }

  if (!sessionId) return <section className="impression-card"><p>绑定两人空间后查看印象。</p></section>
  return <div className="partner-impression">
    <section className="impression-card" aria-labelledby="impression-title" aria-busy={loading || processing}>
      <div className="impression-heading"><h2 id="impression-title"><Sparkles className="impression-title-icon" size={19} aria-hidden="true" /><span>贴贴眼中的{member.name}</span></h2><button type="button" className="impression-refresh" aria-label="更新对方的印象" disabled={saving || loading || processing} onClick={() => { setLoading(true); setRefresh((value) => value + 1) }}><RefreshCw size={15} aria-hidden="true" /></button></div>
      {impression?.summary && <p className="impression-summary">{impression.summary}</p>}
      {(loading || processing) && <div className="impression-loading" role="status"><span className="typing-dots"><i /><i /><i /></span>{impression?.summary ? '正在更新印象…' : '正在整理印象…'}</div>}
      {!loading && impression?.status === 'failed' && <div className="impression-problem" role="alert"><p>{impression.error || '印象整理失败。'}</p><button type="button" onClick={() => { setLoading(true); setRefresh((value) => value + 1) }}>重试</button></div>}
      {error && <div className="impression-problem" role="alert"><p>{error}</p><button type="button" disabled={saving} onClick={() => { setLoading(true); setRefresh((value) => value + 1) }}>重试</button></div>}
    </section>
    <form className="impression-supplement" onSubmit={(event) => void submit(event)}>
      <label htmlFor="impression-input">补充关于{member.name}的信息</label>
      <textarea id="impression-input" rows={4} maxLength={2000} disabled={saving} value={draft} placeholder="习惯、爱好、工作…" onChange={(event) => setDraft(event.target.value)} />
      <div className="impression-input-footer"><button className="primary-button" type="submit" disabled={saving || !draft.trim()}>{saving ? '正在记住…' : '告诉贴贴'}<ArrowUp size={15} aria-hidden="true" /></button></div>
      {saveError && <p className="impression-problem" role="alert">{saveError} 草稿已保留。</p>}
      {memoryPending && <p className="impression-sync" role="status">已保存，记忆同步中。</p>}
    </form>
  </div>
}
