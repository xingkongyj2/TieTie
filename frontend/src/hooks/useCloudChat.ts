import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '../api/client'
import { qoderApi, type CloudHistory, type CloudOperation, type CloudSession, type CloudStreamEvent } from '../api/qoder'
import type { CloudMember, CloudReminder, Message } from '../types'
import { isVisibleChatMessage } from '../lib/chatMessages'
import { preserveSavedFeedback, savedReminderFeedback, type ReplyFeedback } from '../lib/replyFeedback'

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
  replyFeedback: ReplyFeedback | null
}

interface PendingTurn {
  startedAt: number
  messageIds: Set<string>
  idleEventId: string | null
  silent: boolean
  savedReminders?: CloudReminder[]
}

const initialState: CloudState = {
  replyMode: '',
  selectedId: null, session: null, messages: [], members: [], reminders: [], remindersError: '', cursor: null, lastIdleEventId: null,
  loaded: false, loading: true, refreshing: false, error: '', submitting: false, pending: false,
  thinking: false, streaming: false,
  turnError: '', replyFeedback: null,
}

function mergeMessages(existing: Message[], incoming: Message[]): Message[] {
  const byId = new Map(existing.map((message) => [message.id, message]))
  for (const message of incoming) byId.set(message.id, message)
  // A local bubble is replaced by the verified cloud event, including when
  // SSE beats the POST response or a full history refresh finishes first.
  for (const local of existing.filter((message) => message.localStatus)) {
    const sentAt = Date.parse(local.createdAt ?? '')
    const echoed = [...byId.values()].some((message) => message.id !== local.id && !message.localStatus
      && message.sender === 'self' && message.userId === local.userId && message.text === local.text
      && (message.visibility ?? 'shared') === (local.visibility ?? 'shared')
      && (Number.isNaN(sentAt) || Number.isNaN(Date.parse(message.createdAt ?? ''))
        || Math.abs(Date.parse(message.createdAt!) - sentAt) < 120_000))
    if (echoed) byId.delete(local.id)
  }
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
export function useCloudChat(pinnedId: string | null, onSessionForbidden?: () => Promise<void>, selfUserId?: number) {
  const [state, setState] = useState<CloudState>(initialState)
  const current = useRef(state)
  const mounted = useRef(false)
  const readVersion = useRef(0)
  const controller = useRef<AbortController | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const sendLock = useRef(false)
  const localMessageSequence = useRef(0)
  const pendingTurns = useRef(new Map<string, PendingTurn>())
  const operationController = useRef<AbortController | null>(null)
  const operationTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const readRef = useRef<(options?: { full?: boolean }) => Promise<void>>(async () => {})

  const update = useCallback((patch: Partial<CloudState>, newTurn = false) => {
    if (!mounted.current) return
    if (!newTurn && patch.replyFeedback !== undefined) {
      patch = { ...patch, replyFeedback: preserveSavedFeedback(current.current.replyFeedback, patch.replyFeedback) }
    }
    current.current = { ...current.current, ...patch }
    setState(current.current)
  }, [])

  const cancelOperationPoll = useCallback(() => {
    operationController.current?.abort()
    operationController.current = null
    if (operationTimer.current !== null) clearTimeout(operationTimer.current)
    operationTimer.current = null
  }, [])

  const pollOperation = useCallback((id: string, turn: PendingTurn, operation: CloudOperation) => {
    cancelOperationPoll()
    const abort = new AbortController()
    operationController.current = abort
    const isCurrent = () => mounted.current && !abort.signal.aborted && current.current.selectedId === id
      && pendingTurns.current.get(id) === turn
    const poll = async () => {
      if (!isCurrent() || Date.now() - turn.startedAt >= 120_000) return
      try {
        const result = await qoderApi.getOperationStatus(operation, abort.signal)
        if (!isCurrent()) return
        if (result.status === 'saved') {
          const feedback = savedReminderFeedback(result.reminders)
          if (feedback) {
            turn.savedReminders = result.reminders
            const reminders = new Map(current.current.reminders.map((reminder) => [reminder.id, reminder]))
            for (const reminder of result.reminders) reminders.set(reminder.id, reminder)
            update({ reminders: [...reminders.values()], replyFeedback: feedback })
            cancelOperationPoll()
            return
          }
        } else if (result.status === 'failed') {
          update({ replyFeedback: { phase: 'error', message: result.message || '这次操作未完成，正在等待 AI 回复…' } })
          cancelOperationPoll()
          return
        }
      } catch (error) {
        // This reads local operation state. A transient read failure is neither
        // a failed reminder nor grounds to replace the chat's current feedback.
        if (!isCurrent()) return
        if (error instanceof ApiError && [401, 403, 404].includes(error.status)) {
          cancelOperationPoll()
          return
        }
      }
      if (isCurrent()) operationTimer.current = setTimeout(() => { void poll() }, Date.now() - turn.startedAt < 30_000 ? 500 : 2_000)
    }
    void poll()
  }, [cancelOperationPoll, update])

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
    // The live stream delivers new events; polling the entire cloud history
    // every three seconds contends with the reminder/control worker's lock.
    const active = current.current.pending || isRemoteBusy(current.current.session)
    const delay = active ? current.current.streaming ? 12_000 : 5_000 : 15_000
    timer.current = setTimeout(() => { void readRef.current() }, delay)
  }, [])

  const applyHistory = useCallback((id: string, history: CloudHistory, full = false) => {
    const pending = pendingTurns.current.get(id)
    const snapshot = current.current
    let replyFeedback = snapshot.replyFeedback?.phase === 'complete' && history.session.status.toLowerCase() === 'idle'
      ? null : snapshot.replyFeedback
    if (pending) {
      const replied = !pending.silent && history.messages.some((message) => message.sender === 'ai' && message.source === 'chat'
        && !pending.messageIds.has(message.id))
      const turnEnded = history.session.status.toLowerCase() === 'idle' && history.idleEventId !== null
        && history.idleEventId !== pending.idleEventId
      const failed = history.session.status.toLowerCase() === 'terminated'
        || turnEnded && Date.now() - pending.startedAt > 120_000
      const silentDone = pending.silent && turnEnded
      if (replied || failed || silentDone) {
        pendingTurns.current.delete(id)
        cancelOperationPoll()
        replyFeedback = replied ? { phase: 'complete' }
          : silentDone ? { phase: 'sent', message: '消息已发给对方' }
            : { phase: 'error', message: history.turnError || '这次没有收到 AI 回复，请稍后重试。' }
      } else if (turnEnded && !pending.silent) {
        // A tool/control turn becomes idle before its hidden action result and
        // final user-facing reply. Keep the same visible feedback until then.
        replyFeedback = { phase: 'syncing', message: '正在处理并准备回复…' }
      } else if (!pending.silent && Date.now() - pending.startedAt > 30_000) {
        replyFeedback = { phase: 'delayed', message: '回复时间较长，仍在等待并自动检查…' }
      }
    }
    // A cloud read started before the local save can finish afterward. Keep
    // confirmed rows while merging any newer row versions from that read.
    const reminders = new Map((pending?.savedReminders ?? []).map((reminder) => [reminder.id, reminder]))
    for (const reminder of history.reminders ?? snapshot.reminders) reminders.set(reminder.id, reminder)
    update({
      session: history.session,
      replyMode: history.session.replyMode ?? '',
      messages: mergeMessages(full ? snapshot.messages.filter((message) => !!message.localStatus) : snapshot.messages, history.messages)
        .filter((message) => isVisibleChatMessage(message, history.members ?? snapshot.members)),
      members: history.members ?? snapshot.members,
      reminders: [...reminders.values()],
      remindersError: history.remindersError ?? '',
      cursor: history.cursor ?? snapshot.cursor,
      lastIdleEventId: history.idleEventId ?? snapshot.lastIdleEventId,
      loaded: true, loading: false, refreshing: false, error: '',
      pending: pendingTurns.current.has(id),
      replyFeedback,
      ...(history.session.status.toLowerCase() === 'idle' ? { thinking: false } : {}),
      ...(history.turnError !== null ? { turnError: history.turnError } : {}),
    })
  }, [cancelOperationPoll, update])

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
        loaded: false, loading: true, refreshing: false, error: '', thinking: false, turnError: '', replyFeedback: null,
        pending: pendingTurns.current.has(id),
      })
    } else {
      update({ loading: !current.current.loaded, refreshing: full && current.current.loaded })
    }
    if (changed || !current.current.loaded) {
      // Show verified recent rows while the server replays the complete cloud
      // history. The full response replaces this preview and unlocks sending.
      void qoderApi.getCachedMessages(id, abort.signal).then(({ messages }) => {
        if (isCurrent() && current.current.selectedId === id && !current.current.loaded && messages.length) {
          update({ messages })
        }
      }).catch(() => {})
    }
    try {
      const lastCare = full || changed ? undefined : current.current.messages.filter((message) => message.id.startsWith('evt_care_')).reduce<Message | undefined>((latest, message) => !latest || (message.createdAt ?? '') > (latest.createdAt ?? '') || message.createdAt === latest.createdAt && message.id > latest.id ? message : latest, undefined);
      const history = await qoderApi.getMessages(id, full || changed ? null : current.current.cursor, abort.signal, lastCare?.id)
      if (!isCurrent() || current.current.selectedId !== id) return
      applyHistory(id, history, full || changed)
    } catch (error) {
      if (error instanceof ApiError && error.code === 'session_forbidden') {
        void onSessionForbidden?.()
      }
      if (isCurrent()) {
        const pendingTurn = pendingTurns.current.get(id)
        update({
          error: pendingTurn ? '' : errorMessage(error), loading: false, refreshing: false,
          ...(pendingTurn && !pendingTurn.silent
            ? { replyFeedback: { phase: 'delayed', message: '回复还在同步，正在自动重试…' } as ReplyFeedback } : {}),
        })
      }
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
    if (!selfUserId) throw new Error('账号信息还未加载，请稍后再发送。')
    const id = snapshot.selectedId
    const localId = `local_${Date.now()}_${++localMessageSequence.current}`
    const now = new Date()
    const optimistic: Message = {
      id: localId, sender: 'self', userId: selfUserId,
      displayName: snapshot.members.find((member) => member.userId === selfUserId)?.name,
      source: 'chat', kind: 'text', text: draft, visibility,
      files: files.map((file) => file.name), time: now.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false }),
      createdAt: now.toISOString(), localStatus: 'sending',
    }
    sendLock.current = true
    cancelOperationPoll()
    cancelRead()
    pendingTurns.current.set(id, {
      startedAt: Date.now(),
      messageIds: new Set(snapshot.messages.map((message) => message.id)),
      idleEventId: snapshot.lastIdleEventId,
      silent,
    })
    update({ messages: mergeMessages(snapshot.messages, [optimistic]), submitting: true, pending: true, replyMode: silent ? 'silent' : '', error: '', turnError: '', refreshing: false,
      replyFeedback: { phase: 'sending' } }, true)
    let shouldRead = false
    try {
      const result = await qoderApi.sendMessage(id, draft, files, visibility)
      shouldRead = true
      const pendingTurn = pendingTurns.current.get(id)
      if (pendingTurn && result.replyMode === 'silent') pendingTurn.silent = true
      if (mounted.current && current.current.selectedId === id) {
        update({ replyMode: result.replyMode ?? '', messages: mergeMessages(current.current.messages, result.messages)
          .map((message) => message.id === localId ? { ...message, localStatus: 'sent' as const } : message)
          .filter((message) => isVisibleChatMessage(message, current.current.members)),
        ...(current.current.replyFeedback?.phase === 'sending'
          ? { replyFeedback: silent || result.replyMode === 'silent'
            ? { phase: 'sent', message: '消息已发给对方' } as ReplyFeedback
            : { phase: 'waiting' } as ReplyFeedback }
          : {}) })
        if (pendingTurn && !pendingTurn.silent && result.operation) pollOperation(id, pendingTurn, result.operation)
      }
      // An accepted POST is success even if the following history read fails.
      return true
    } catch (error) {
      const uncertain = error instanceof ApiError && (error.status >= 500 || !!error.code && ['TIMEOUT', 'NETWORK_ERROR', 'upstream_timeout', 'connection_failed'].includes(error.code))
      if (uncertain) shouldRead = true
      if (!uncertain) pendingTurns.current.delete(id)
      if (mounted.current && current.current.selectedId === id) {
        update({
          messages: current.current.messages.map((message) => message.id === localId
            ? { ...message, localStatus: uncertain ? 'uncertain' : 'failed' } : message),
          pending: uncertain, error: '', replyFeedback: uncertain
            ? { phase: 'delayed', message: '消息发送状态待确认，正在自动检查…' }
            : null,
        })
      }
      if (uncertain) return true
      throw error
    } finally {
      sendLock.current = false
      if (mounted.current && current.current.selectedId === id) {
        update({ submitting: false })
        if (shouldRead) void readRef.current()
        else schedule()
      }
    }
  }, [cancelOperationPoll, cancelRead, pollOperation, schedule, selfUserId, update])

  useEffect(() => {
    mounted.current = true
    void reload()
    return () => { mounted.current = false; cancelRead(); cancelOperationPoll() }
  }, [cancelOperationPoll, cancelRead, reload])

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
    cancelOperationPoll()
    cancelRead()
    pendingTurns.current.set(id, {
      startedAt: Date.now(),
      messageIds: new Set(snapshot.messages.map((message) => message.id)),
      idleEventId: snapshot.lastIdleEventId,
      silent: false,
    })
    update({ submitting: true, pending: true, error: '', replyFeedback: { phase: 'sending' } }, true)
    try {
      await qoderApi.sendToolResult(id, toolUseId, text)
      if (mounted.current && current.current.selectedId === id) {
        // 增量刷新取不到更早的 ask 事件，先本地标记已回答，免得卡片还能再点一次。
        update({ messages: current.current.messages.map((message) => (message.id === toolUseId ? { ...message, answered: true } : message)),
          ...(current.current.replyFeedback?.phase === 'sending' ? { replyFeedback: { phase: 'waiting' } as ReplyFeedback } : {}) })
      }
      return true
    } catch (error) {
      pendingTurns.current.delete(id)
      if (mounted.current && current.current.selectedId === id) update({ pending: false, error: '', replyFeedback: { phase: 'error', message: errorMessage(error) } })
      throw error
    } finally {
      sendLock.current = false
      if (mounted.current && current.current.selectedId === id) {
        update({ submitting: false })
        void readRef.current()
      }
    }
  }, [cancelOperationPoll, cancelRead, update])

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
      const pendingTurn = pendingTurns.current.get(id)
      if (item.type === 'start') {
        if (item.kind === 'thinking') update({ thinking: true, ...(pendingTurn && !pendingTurn.silent ? { replyFeedback: { phase: 'thinking' } as ReplyFeedback } : {}) })
        else update({ thinking: false, ...(pendingTurn && !pendingTurn.silent ? { replyFeedback: { phase: 'replying' } as ReplyFeedback } : {}) })
      } else if (item.type === 'delta') {
        // The assistant streams a structured envelope. Only server-parsed final
        // messages are safe to display; keep the thinking indicator until then.
        update({ thinking: true, ...(pendingTurn && !pendingTurn.silent ? { replyFeedback: { phase: 'replying' } as ReplyFeedback } : {}) })
      } else if (item.type === 'message') {
        if (item.message && item.message.sender !== 'ai') update({ replyMode: item.message.replyMode ?? '' })
        if (item.message && isVisibleChatMessage(item.message, snapshot.members)) {
          const replied = !!pendingTurn && item.message.sender === 'ai' && item.message.source === 'chat'
            && !pendingTurn.messageIds.has(item.message.id)
          if (replied) { pendingTurns.current.delete(id); cancelOperationPoll() }
          update({ messages: mergeMessages(current.current.messages, [item.message]), cursor: nextCursor, thinking: false,
            pending: pendingTurns.current.has(id), ...(item.message.sender === 'ai' ? { turnError: '' } : {}),
            ...(replied ? { replyFeedback: { phase: 'complete' } as ReplyFeedback } : {}) })
        }
        else update({ cursor: nextCursor })
      } else if (item.type === 'thinking_end') {
        update({ thinking: false, cursor: nextCursor,
          ...(pendingTurn && !pendingTurn.silent ? { replyFeedback: { phase: 'syncing' } as ReplyFeedback } : {}) })
      } else if (item.type === 'status') {
        const status = item.status
        if (status === 'terminated') { pendingTurns.current.delete(id); cancelOperationPoll() }
        update({
          session: snapshot.session ? { ...snapshot.session, status } : null,
          cursor: nextCursor, pending: pendingTurns.current.has(id),
          thinking: status === 'idle' || status === 'terminated' ? false : snapshot.thinking,
          ...(status === 'idle' ? { lastIdleEventId: item.id } : {}),
          ...(pendingTurn && !pendingTurn.silent && status === 'idle' ? { replyFeedback: { phase: 'syncing' } as ReplyFeedback } : {}),
          ...(!pendingTurn && status === 'idle' && snapshot.replyFeedback?.phase === 'complete' ? { replyFeedback: null } : {}),
          ...(pendingTurn && status === 'terminated' ? { replyFeedback: { phase: 'error', message: 'AI 会话已中断，请重试。' } as ReplyFeedback } : {}),
        })
        if (status === 'idle' || status === 'terminated') void readRef.current({ full: true })
      } else if (item.type === 'session_error') {
        if (pendingTurn) { pendingTurns.current.delete(id); cancelOperationPoll() }
        update({ turnError: snapshot.replyMode === 'silent' ? '' : item.message, cursor: nextCursor, thinking: false,
          pending: pendingTurns.current.has(id),
          ...(pendingTurn ? { replyFeedback: { phase: 'error', message: item.message || 'AI 回复失败，请重试。' } as ReplyFeedback } : {}) })
      }
    }
    const privateSource = hasPrivateChannel ? qoderApi.privateStream(id) : null
    if (privateSource) privateSource.onmessage = source.onmessage
    return () => { source.close(); privateSource?.close(); update({ streaming: false }) }
  }, [state.selectedId, state.loaded, hasPrivateChannel, cancelOperationPoll, update])

  const busy = state.pending || isRemoteBusy(state.session)
  const replyFeedback = state.replyFeedback?.phase === 'complete' ? null : state.replyFeedback
    ?? (busy || state.thinking ? { phase: 'waiting' } as ReplyFeedback : null)
  // 云端在等待工具应答时状态仍是 idle，但此时发消息会被上游拒 409，所以按忙处理。
  const awaitingAsk = state.messages.some((message) => message.kind === 'ask' && !message.answered)
  const canSend = state.loaded && !!state.selectedId && !state.error && !state.submitting
    && !busy && !awaitingAsk && state.session?.status.toLowerCase() === 'idle'
  return {
    selectedId: state.selectedId, session: state.session, pinned: !!pinnedId,
    messages: state.messages, loading: state.loading, refreshing: state.refreshing,
    members: state.members, reminders: state.reminders, remindersError: state.remindersError,
    error: state.error, submitting: state.submitting, busy, awaitingAsk, canSend,
    thinking: state.thinking, streaming: state.streaming, replyFeedback,
    silent: state.replyMode === 'silent',
    turnError: state.turnError,
    reload, sendMessage, answerAsk, saveReminder, changeReminder,
  }
}
