import { useCallback, useEffect, useRef, useState } from 'react'
import { qoderApi, type CloudHistory, type CloudSession, type CloudStreamEvent } from '../api/qoder'
import { readStorage, writeStorage } from '../lib/storage'
import type { Message } from '../types'

const SELECTION_KEY = 'tietie.qoder.selectedSession.v1'
const isRemoteBusy = (session: CloudSession | null) =>
  !!session && ['running', 'rescheduling', 'canceling'].includes(session.status.toLowerCase())

interface CloudState {
  sessions: CloudSession[]
  selectedId: string | null
  session: CloudSession | null
  messages: Message[]
  cursor: string | null
  lastIdleEventId: string | null
  loaded: boolean
  loading: boolean
  refreshing: boolean
  error: string
  submitting: boolean
  pending: boolean
  thinking: boolean
  streaming: boolean
  turnError: string
}

interface PendingTurn {
  status: string
  messageIds: Set<string>
  idleEventId: string | null
}

interface ReadOptions { refreshList?: boolean; full?: boolean }

const initialState: CloudState = {
  sessions: [], selectedId: null, session: null, messages: [], cursor: null, lastIdleEventId: null,
  loaded: false, loading: true, refreshing: false, error: '', submitting: false, pending: false,
  thinking: false, streaming: false,
  turnError: '',
}

function mergeMessages(existing: Message[], incoming: Message[]): Message[] {
  const byId = new Map(existing.map((message) => [message.id, message]))
  for (const message of incoming) byId.set(message.id, message)
  // The accepted user event may arrive before an unread earlier reply is polled.
  // Reconcile in timestamp order rather than the order requests completed.
  return [...byId.values()].sort((a, b) => {
    const first = Date.parse(a.createdAt ?? '')
    const second = Date.parse(b.createdAt ?? '')
    return Number.isNaN(first) || Number.isNaN(second) ? 0 : first - second
  })
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : '云端会话暂时无法同步，请稍后刷新。'
}

export function useCloudChat() {
  const [state, setState] = useState<CloudState>(initialState)
  const current = useRef(state)
  const mounted = useRef(false)
  const readVersion = useRef(0)
  const controller = useRef<AbortController | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const sendLock = useRef(false)
  const pendingTurns = useRef(new Map<string, PendingTurn>())
  const readRef = useRef<(options?: ReadOptions) => Promise<void>>(async () => {})

  const update = useCallback((patch: Partial<CloudState>) => {
    if (!mounted.current) return
    current.current = { ...current.current, ...patch }
    setState(current.current)
  }, [])

  const cancelRead = useCallback(() => {
    readVersion.current += 1
    controller.current?.abort()
    controller.current = null
    if (timer.current !== null) clearTimeout(timer.current)
    timer.current = null
  }, [])

  const schedule = useCallback(() => {
    if (!mounted.current || sendLock.current || !current.current.selectedId) return
    if (timer.current !== null) clearTimeout(timer.current)
    const delay = current.current.pending || isRemoteBusy(current.current.session) ? 8_000 : 30_000
    timer.current = setTimeout(() => { void readRef.current() }, delay)
  }, [])

  const applyHistory = useCallback((id: string, history: CloudHistory) => {
    const pending = pendingTurns.current.get(id)
    if (pending && (history.session.status.toLowerCase() !== pending.status
      || (history.session.status.toLowerCase() === 'idle' && history.idleEventId !== null
        && history.idleEventId !== pending.idleEventId)
      || history.messages.some((message) => message.sender === 'ai' && !pending.messageIds.has(message.id)))) {
      pendingTurns.current.delete(id)
    }
    const snapshot = current.current
    update({
      session: history.session,
      sessions: snapshot.sessions.map((session) => session.id === id ? history.session : session),
      messages: mergeMessages(snapshot.messages, history.messages),
      cursor: history.cursor ?? snapshot.cursor,
      lastIdleEventId: history.idleEventId ?? snapshot.lastIdleEventId,
      loaded: true, loading: false, refreshing: false, error: '',
      pending: pendingTurns.current.has(id),
      ...(history.session.status.toLowerCase() === 'idle' ? { thinking: false } : {}),
      ...(history.turnError !== null ? { turnError: history.turnError } : {}),
    })
  }, [update])

  const readCloud = useCallback(async ({ refreshList = false, full = false }: ReadOptions = {}) => {
    if (!mounted.current || sendLock.current) return
    cancelRead()
    const version = readVersion.current
    const abort = new AbortController()
    controller.current = abort
    const isCurrent = () => mounted.current && version === readVersion.current && !abort.signal.aborted
    let id = current.current.selectedId
    update({ loading: !current.current.loaded, refreshing: full && current.current.loaded })
    try {
      if (refreshList) {
        const result = await qoderApi.listSessions(abort.signal)
        if (!isCurrent()) return
        const saved = readStorage<unknown>(SELECTION_KEY, () => null)
        const candidates = [id, typeof saved === 'string' ? saved : null, result.defaultSessionId]
        id = candidates.find((candidate) => candidate && result.data.some((session) => session.id === candidate))
          ?? result.data[0]?.id ?? null
        const changed = id !== current.current.selectedId
        update({
          sessions: result.data,
          selectedId: id,
          session: result.data.find((session) => session.id === id) ?? null,
          ...(changed ? { messages: [], cursor: null, lastIdleEventId: null, loaded: false, loading: !!id, thinking: false, turnError: '' } : {}),
          pending: !!id && pendingTurns.current.has(id),
        })
        if (id) writeStorage(SELECTION_KEY, id)
        if (!id) {
          update({ messages: [], cursor: null, lastIdleEventId: null, loaded: false, loading: false, refreshing: false, error: '' })
          return
        }
      }
      if (!id) return
      const history = await qoderApi.getMessages(id, full ? null : current.current.cursor, abort.signal)
      if (!isCurrent() || current.current.selectedId !== id) return
      applyHistory(id, history)
    } catch (error) {
      if (isCurrent()) update({ error: errorMessage(error), loading: false, refreshing: false })
    } finally {
      if (isCurrent()) {
        controller.current = null
        schedule()
      }
    }
  }, [applyHistory, cancelRead, schedule, update])
  readRef.current = readCloud

  const reload = useCallback(() => readCloud({ refreshList: true, full: true }), [readCloud])

  const selectSession = useCallback((id: string) => {
    if (sendLock.current || id === current.current.selectedId) return
    const session = current.current.sessions.find((candidate) => candidate.id === id)
    if (!session) return
    cancelRead()
    writeStorage(SELECTION_KEY, id)
    update({
      selectedId: id, session, messages: [], cursor: null, lastIdleEventId: null, loaded: false, thinking: false,
      loading: true, refreshing: false, error: '', turnError: '', pending: pendingTurns.current.has(id),
    })
    void readCloud({ full: true })
  }, [cancelRead, readCloud, update])

  const sendMessage = useCallback(async (text: string, files: File[] = []): Promise<boolean> => {
    const snapshot = current.current
    const draft = text.trim()
    if ((!draft && !files.length) || sendLock.current) return false
    if (!snapshot.selectedId || !snapshot.loaded || snapshot.error || snapshot.pending
      || snapshot.session?.status.toLowerCase() !== 'idle') {
      throw new Error('请等待会话同步完成或当前回复结束后再发送。')
    }
    if (draft.length > 2_000) throw new Error('这条消息有点长，请控制在 2000 字以内。')
    const id = snapshot.selectedId
    sendLock.current = true
    cancelRead()
    pendingTurns.current.set(id, {
      status: snapshot.session.status.toLowerCase(),
      messageIds: new Set(snapshot.messages.map((message) => message.id)),
      idleEventId: snapshot.lastIdleEventId,
    })
    update({ submitting: true, pending: true, error: '', turnError: '', refreshing: false })
    let accepted = false
    try {
      const result = await qoderApi.sendMessage(id, draft, files)
      accepted = true
      if (mounted.current && current.current.selectedId === id) {
        update({ messages: mergeMessages(current.current.messages, result.messages) })
      }
      // An accepted POST is success even if the following history read fails.
      return true
    } catch (error) {
      pendingTurns.current.delete(id)
      if (mounted.current && current.current.selectedId === id) {
        update({ pending: false, error: errorMessage(error) })
      }
      throw error
    } finally {
      sendLock.current = false
      if (mounted.current && current.current.selectedId === id) {
        update({ submitting: false })
        if (accepted) void readRef.current()
        else schedule()
      }
    }
  }, [cancelRead, schedule, update])

  useEffect(() => {
    mounted.current = true
    void reload()
    return () => { mounted.current = false; cancelRead() }
  }, [cancelRead, reload])

  useEffect(() => {
    const id = state.selectedId
    if (!state.loaded || !id) return
    const source = qoderApi.stream(id, current.current.cursor)
    source.onopen = () => update({ streaming: true })
    source.onerror = () => update({ streaming: false })
    source.onmessage = (event) => {
      let item: CloudStreamEvent
      try { item = JSON.parse(event.data) as CloudStreamEvent } catch { return }
      if (current.current.selectedId !== id) return
      const snapshot = current.current
      if (item.type === 'start') {
        if (item.kind === 'thinking') update({ thinking: true })
        else update({ thinking: false })
      } else if (item.type === 'delta') {
        const prior = snapshot.messages.find((message) => message.id === item.id)
        if (prior && !prior.streaming) return
        const now = new Date().toISOString()
        const partial: Message = prior
          ? { ...prior, text: prior.text + item.text }
          : { id: item.id, sender: 'ai', text: item.text, time: '', createdAt: now, kind: 'text', streaming: true }
        update({ messages: mergeMessages(snapshot.messages, [partial]), thinking: false })
      } else if (item.type === 'message') {
        if (item.message) update({ messages: mergeMessages(snapshot.messages, [item.message]), cursor: item.id, thinking: false, ...(item.message.sender === 'ai' ? { turnError: '' } : {}) })
        else update({ cursor: item.id })
      } else if (item.type === 'thinking_end') {
        update({ thinking: false, cursor: item.id })
      } else if (item.type === 'status') {
        const status = item.status
        if (status === 'idle' || status === 'terminated') pendingTurns.current.delete(id)
        update({
          session: snapshot.session ? { ...snapshot.session, status } : null,
          cursor: item.id, pending: pendingTurns.current.has(id),
          thinking: status === 'idle' || status === 'terminated' ? false : snapshot.thinking,
          ...(status === 'idle' ? { lastIdleEventId: item.id } : {}),
        })
        if (status === 'idle' || status === 'terminated') void readRef.current({ full: true })
      } else if (item.type === 'session_error') {
        update({ turnError: item.message, cursor: item.id, thinking: false })
      }
    }
    return () => { source.close(); update({ streaming: false }) }
  }, [state.selectedId, state.loaded, update])

  const busy = state.pending || isRemoteBusy(state.session)
  const canSend = state.loaded && !!state.selectedId && !state.error && !state.submitting
    && !busy && state.session?.status.toLowerCase() === 'idle'
  return {
    sessions: state.sessions, selectedId: state.selectedId, session: state.session,
    messages: state.messages, loading: state.loading, refreshing: state.refreshing,
    error: state.error, submitting: state.submitting, busy, canSend,
    thinking: state.thinking, streaming: state.streaming,
    turnError: state.turnError,
    selectSession, reload, sendMessage,
  }
}
