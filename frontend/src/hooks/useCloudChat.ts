import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '../api/client'
import { qoderApi, type CloudHistory, type CloudSession, type CloudStreamEvent } from '../api/qoder'
import type { CloudMember, CloudReminder, Message } from '../types'
import { isVisibleChatMessage } from '../lib/chatMessages'

const isRemoteBusy = (session: CloudSession | null) =>
  !!session && ['running', 'rescheduling', 'canceling'].includes(session.status.toLowerCase())

interface CloudState {
  replyMode: '' | 'silent'
  selectedId: string | null
  session: CloudSession | null
  messages: Message[]
  members: CloudMember[]
  reminders: CloudReminder[]
  remindersError: string
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

const initialState: CloudState = {
  replyMode: '',
  selectedId: null, session: null, messages: [], members: [], reminders: [], remindersError: '', cursor: null, lastIdleEventId: null,
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

/**
 * 云端聊天状态机。会话由绑定关系固定（pinnedId），不再列出/切换账号下的全部会话；
 * 未绑定（pinnedId 为 null）时保持空闲，由界面展示绑定引导页。
 */
export function useCloudChat(pinnedId: string | null, onSessionForbidden?: () => Promise<void>) {
  const [state, setState] = useState<CloudState>(initialState)
  const current = useRef(state)
  const mounted = useRef(false)
  const readVersion = useRef(0)
  const controller = useRef<AbortController | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const sendLock = useRef(false)
  const pendingTurns = useRef(new Map<string, PendingTurn>())
  const readRef = useRef<(options?: { full?: boolean }) => Promise<void>>(async () => {})

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
    const delay = current.current.pending || isRemoteBusy(current.current.session) ? 3_000 : 15_000
    timer.current = setTimeout(() => { void readRef.current() }, delay)
  }, [])

  const applyHistory = useCallback((id: string, history: CloudHistory, full = false) => {
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
      replyMode: history.session.replyMode ?? '',
      messages: (full ? history.messages : mergeMessages(snapshot.messages, history.messages))
        .filter((message) => isVisibleChatMessage(message, history.members ?? snapshot.members)),
      members: history.members ?? snapshot.members,
      reminders: history.reminders ?? snapshot.reminders,
      remindersError: history.remindersError ?? '',
      cursor: history.cursor ?? snapshot.cursor,
      lastIdleEventId: history.idleEventId ?? snapshot.lastIdleEventId,
      loaded: true, loading: false, refreshing: false, error: '',
      pending: pendingTurns.current.has(id),
      ...(history.session.status.toLowerCase() === 'idle' ? { thinking: false } : {}),
      ...(history.turnError !== null ? { turnError: history.turnError } : {}),
    })
  }, [update])

  const readCloud = useCallback(async ({ full = false }: { full?: boolean } = {}) => {
    if (!mounted.current || sendLock.current) return
    const id = pinnedId
    if (!id) {
      // 解绑后清空会话状态，SSE 与轮询随 selectedId 归零一起停掉。
      cancelRead()
      update(current.current.selectedId ? { ...initialState, loading: false } : { loading: false, refreshing: false })
      return
    }
    cancelRead()
    const version = readVersion.current
    const abort = new AbortController()
    controller.current = abort
    const isCurrent = () => mounted.current && version === readVersion.current && !abort.signal.aborted
    const changed = id !== current.current.selectedId
    if (changed) {
      update({
        selectedId: id, session: null, messages: [], members: [], reminders: [], remindersError: '', cursor: null, lastIdleEventId: null,
        loaded: false, loading: true, refreshing: false, error: '', thinking: false, turnError: '',
        pending: pendingTurns.current.has(id),
      })
    } else {
      update({ loading: !current.current.loaded, refreshing: full && current.current.loaded })
    }
    try {
      const history = await qoderApi.getMessages(id, full || changed ? null : current.current.cursor, abort.signal)
      if (!isCurrent() || current.current.selectedId !== id) return
      applyHistory(id, history, full || changed)
    } catch (error) {
      if (error instanceof ApiError && error.code === 'session_forbidden') {
        void onSessionForbidden?.()
      }
      if (isCurrent()) update({ error: errorMessage(error), loading: false, refreshing: false })
    } finally {
      if (isCurrent()) {
        controller.current = null
        schedule()
      }
    }
  }, [pinnedId, onSessionForbidden, applyHistory, cancelRead, schedule, update])
  readRef.current = readCloud

  const reload = useCallback(() => readCloud({ full: true }), [readCloud])

  const saveReminder = useCallback(async (input: { title: string; dueAt: string; recipientIds: number[] }) => {
    const id = current.current.selectedId
    if (!id || !current.current.loaded) throw new Error('请先绑定并加载共享空间，再添加提醒。')
    const { reminder } = await qoderApi.createReminder(id, input)
    if (mounted.current && current.current.selectedId === id) {
      update({ reminders: [...current.current.reminders.filter((item) => item.id !== reminder.id), reminder] })
      void readRef.current()
    }
  }, [update])

  const changeReminder = useCallback(async (reminderId: string, status: 'completed' | 'scheduled' | 'cancelled') => {
    const id = current.current.selectedId
    if (!id || !current.current.loaded) throw new Error('共享提醒还未加载，请稍后再试。')
    const { reminder } = await qoderApi.updateReminder(id, reminderId, status)
    if (mounted.current && current.current.selectedId === id) {
      update({ reminders: current.current.reminders.map((item) => item.id === reminder.id ? reminder : item) })
      void readRef.current()
    }
  }, [update])

  const sendMessage = useCallback(async (text: string, files: File[] = [], visibility: 'shared' | 'private' = 'shared', silent = false): Promise<boolean> => {
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
    update({ submitting: true, pending: true, replyMode: silent ? 'silent' : '', error: '', turnError: '', refreshing: false })
    let accepted = false
    try {
      const result = await qoderApi.sendMessage(id, draft, files, visibility)
      accepted = true
      if (mounted.current && current.current.selectedId === id) {
        update({ replyMode: result.replyMode ?? '', messages: mergeMessages(current.current.messages, result.messages)
          .filter((message) => isVisibleChatMessage(message, current.current.members)) })
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
    const resume = () => { if (document.visibilityState === 'visible') void readRef.current() }
    window.addEventListener('focus', resume)
    document.addEventListener('visibilitychange', resume)
    return () => { window.removeEventListener('focus', resume); document.removeEventListener('visibilitychange', resume) }
  }, [])

  /** 回答云端选择题（AskUserQuestion）：回传后本轮继续，所以按发送一样置忙并刷新。 */
  const answerAsk = useCallback(async (toolUseId: string, text: string): Promise<boolean> => {
    const snapshot = current.current
    const id = snapshot.selectedId
    if (!id || sendLock.current) throw new Error('会话还没准备好，请稍后再试。')
    sendLock.current = true
    cancelRead()
    pendingTurns.current.set(id, {
      status: (snapshot.session?.status ?? 'idle').toLowerCase(),
      messageIds: new Set(snapshot.messages.map((message) => message.id)),
      idleEventId: snapshot.lastIdleEventId,
    })
    update({ submitting: true, pending: true, error: '' })
    try {
      await qoderApi.sendToolResult(id, toolUseId, text)
      if (mounted.current && current.current.selectedId === id) {
        // 增量刷新取不到更早的 ask 事件，先本地标记已回答，免得卡片还能再点一次。
        update({ messages: current.current.messages.map((message) => (message.id === toolUseId ? { ...message, answered: true } : message)) })
      }
      return true
    } catch (error) {
      pendingTurns.current.delete(id)
      if (mounted.current && current.current.selectedId === id) update({ pending: false, error: errorMessage(error) })
      throw error
    } finally {
      sendLock.current = false
      if (mounted.current && current.current.selectedId === id) {
        update({ submitting: false })
        void readRef.current()
      }
    }
  }, [cancelRead, update])

  const hasPrivateChannel = state.messages.some((message) => message.visibility === 'private')

  useEffect(() => {
    const id = state.selectedId
    if (!state.loaded || !id) return
    const source = qoderApi.stream(id, current.current.cursor)
    source.onopen = () => update({ streaming: true })
    source.onerror = () => update({ streaming: false })
    source.onmessage = (event) => {
      let item: CloudStreamEvent & { private?: boolean }
      try { item = JSON.parse(event.data) as CloudStreamEvent & { private?: boolean } } catch { return }
      if (current.current.selectedId !== id) return
      const snapshot = current.current
      if (item.private && item.type === 'status') { void readRef.current({ full: true }); return }
      const nextCursor = item.private ? snapshot.cursor : item.id
      if (item.type === 'start') {
        if (item.kind === 'thinking') update({ thinking: true })
        else update({ thinking: false })
      } else if (item.type === 'delta') {
        // The assistant streams a structured envelope. Only server-parsed final
        // messages are safe to display; keep the thinking indicator until then.
        update({ thinking: true })
      } else if (item.type === 'message') {
        if (item.message && item.message.sender !== 'ai') update({ replyMode: item.message.replyMode ?? '' })
        if (item.message && isVisibleChatMessage(item.message, snapshot.members)) update({ messages: mergeMessages(snapshot.messages, [item.message]), cursor: nextCursor, thinking: false, ...(item.message.sender === 'ai' ? { turnError: '' } : {}) })
        else update({ cursor: nextCursor })
      } else if (item.type === 'thinking_end') {
        update({ thinking: false, cursor: nextCursor })
      } else if (item.type === 'status') {
        const status = item.status
        if (status === 'idle' || status === 'terminated') pendingTurns.current.delete(id)
        update({
          session: snapshot.session ? { ...snapshot.session, status } : null,
          cursor: nextCursor, pending: pendingTurns.current.has(id),
          thinking: status === 'idle' || status === 'terminated' ? false : snapshot.thinking,
          ...(status === 'idle' ? { lastIdleEventId: item.id } : {}),
        })
        if (status === 'idle' || status === 'terminated') void readRef.current({ full: true })
      } else if (item.type === 'session_error') {
        update({ turnError: snapshot.replyMode === 'silent' ? '' : item.message, cursor: nextCursor, thinking: false })
      }
    }
    const privateSource = hasPrivateChannel ? qoderApi.privateStream(id) : null
    if (privateSource) privateSource.onmessage = source.onmessage
    return () => { source.close(); privateSource?.close(); update({ streaming: false }) }
  }, [state.selectedId, state.loaded, hasPrivateChannel, update])

  const busy = state.pending || isRemoteBusy(state.session)
  // 云端在等待工具应答时状态仍是 idle，但此时发消息会被上游拒 409，所以按忙处理。
  const awaitingAsk = state.messages.some((message) => message.kind === 'ask' && !message.answered)
  const canSend = state.loaded && !!state.selectedId && !state.error && !state.submitting
    && !busy && !awaitingAsk && state.session?.status.toLowerCase() === 'idle'
  return {
    selectedId: state.selectedId, session: state.session, pinned: !!pinnedId,
    messages: state.messages, loading: state.loading, refreshing: state.refreshing,
    members: state.members, reminders: state.reminders, remindersError: state.remindersError,
    error: state.error, submitting: state.submitting, busy, awaitingAsk, canSend,
    thinking: state.thinking, streaming: state.streaming,
    silent: state.replyMode === 'silent',
    turnError: state.turnError,
    reload, sendMessage, answerAsk, saveReminder, changeReminder,
  }
}
