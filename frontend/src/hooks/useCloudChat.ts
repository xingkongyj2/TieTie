import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '../api/client'
import { qoderApi, SendCancelledError, type CloudHistory, type CloudSession, type CloudStreamEvent } from '../api/qoder'
import type { CloudMember, CloudReminder, Message, ReminderRecurrence } from '../types'
import { isTurnCancellationMarker, isVisibleChatMessage } from '../lib/chatMessages'
import { AbortController } from '../lib/abort'
import type { MiniFile } from '../lib/files'
import { isAppVisible, onAppVisibilityChange } from '../lib/platform'

const isRemoteBusy = (session: CloudSession | null) =>
  !!session && ['running', 'rescheduling', 'canceling'].includes(session.status.toLowerCase())
const INITIAL_LOAD_NOTICE_MS = 10_000
const INITIAL_LOAD_TIMEOUT_MS = 30_000

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
  slowLoading: boolean
  refreshing: boolean
  error: string
  submitting: boolean
  stopping: boolean
  pending: boolean
  thinking: boolean
  streaming: boolean
  turnError: string
  replyFeedback: ReplyFeedback | null
}

type ReplyPhase = 'sending' | 'waiting' | 'thinking' | 'replying' | 'syncing' | 'delayed' | 'stopping' | 'stopped' | 'sent' | 'error' | 'complete' | 'proactive_reminder' | 'proactive_update'
interface ReplyFeedback { phase: ReplyPhase; message?: string }

interface PendingTurn {
  startedAt: number
  messageIds: Set<string>
  idleEventId: string | null
  silent: boolean
  visibility: 'shared' | 'private'
  stopRequested?: boolean
  cancelAccepted?: boolean
  dispatched: boolean
  toolUseId?: string
  submissionDone: Promise<void>
  finishSubmission: () => void
}

const initialState: CloudState = {
  replyMode: '',
  selectedId: null, session: null, messages: [], members: [], reminders: [], remindersError: '', cursor: null, lastIdleEventId: null,
  loaded: false, loading: true, slowLoading: false, refreshing: false, error: '', submitting: false, stopping: false, pending: false,
  thinking: false, streaming: false,
  turnError: '', replyFeedback: null,
}

function mergeMessages(existing: Message[], incoming: Message[], replace = false): Message[] {
  const previous = new Map(existing.map((message) => [message.id, message]))
  const byId = new Map((replace ? existing.filter((message) => !!message.localStatus) : existing)
    .map((message) => [message.id, message]))
  for (const message of incoming) {
    const renderKey = previous.get(message.id)?.renderKey
    byId.set(message.id, renderKey ? { ...message, renderKey } : message)
  }
  // Only a newly received cloud event can acknowledge a local bubble. Reusing
  // an older equal-text row would briefly remove a fresh short message.
  const newEchoes = incoming.filter((message) => message.sender === 'self' && !message.localStatus && !previous.has(message.id))
  const usedEchoes = new Set<string>()
  for (const local of existing.filter((message) => message.localStatus && message.localStatus !== 'failed')) {
    const sentAt = Date.parse(local.createdAt ?? '')
    const echoed = newEchoes.find((message) => !usedEchoes.has(message.id) && message.id !== local.id
      && message.sender === 'self' && message.userId === local.userId && message.text === local.text
      && (message.visibility ?? 'shared') === (local.visibility ?? 'shared')
      && (Number.isNaN(sentAt) || Number.isNaN(Date.parse(message.createdAt ?? ''))
        || Math.abs(Date.parse(message.createdAt!) - sentAt) < 120_000))
    if (echoed) {
      usedEchoes.add(echoed.id)
      byId.set(echoed.id, { ...byId.get(echoed.id)!, renderKey: local.renderKey ?? local.id })
      byId.delete(local.id)
    }
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
  const [visible, setVisible] = useState(isAppVisible)
  const scopeRef = useRef('')
  const loadedScope = useRef('')
  const scopeIdentity = `${selfUserId ?? ''}:${pinnedId ?? ''}`
  const scopeEpoch = useRef({ identity: scopeIdentity, version: 0 })
  if (scopeEpoch.current.identity !== scopeIdentity) scopeEpoch.current = { identity: scopeIdentity, version: scopeEpoch.current.version + 1 }
  const scope = `${scopeIdentity}:${scopeEpoch.current.version}`
  scopeRef.current = scope
  const readVersion = useRef(0)
  const controller = useRef<AbortController | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const loadNoticeTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const sendLock = useRef<{ scope: string; operation: symbol } | null>(null)
  const stopLock = useRef<{ scope: string; operation: symbol } | null>(null)
  const suppressStoppedError = useRef(false)
  const stoppedTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const localMessageSequence = useRef(0)
  const pendingTurns = useRef(new Map<string, PendingTurn>())
  const readRef = useRef<(options?: { full?: boolean }) => Promise<void>>(async () => {})

  const update = useCallback((patch: Partial<CloudState>) => {
    if (!mounted.current) return
    current.current = { ...current.current, ...patch }
    setState(current.current)
  }, [])

  const clearStoppedLater = useCallback(() => {
    const stoppedScope = scopeRef.current
    if (stoppedTimer.current) clearTimeout(stoppedTimer.current)
    stoppedTimer.current = setTimeout(() => {
      stoppedTimer.current = null
      if (scopeRef.current === stoppedScope && current.current.replyFeedback?.phase === 'stopped') update({ replyFeedback: null })
    }, 2200)
  }, [update])

  const cancelRead = useCallback(() => {
    readVersion.current += 1
    controller.current?.abort()
    controller.current = null
    if (timer.current !== null) clearTimeout(timer.current)
    timer.current = null
    if (loadNoticeTimer.current !== null) clearTimeout(loadNoticeTimer.current)
    loadNoticeTimer.current = null
  }, [])

  const schedule = useCallback(() => {
    if (!mounted.current || !isAppVisible() || sendLock.current?.scope === scopeRef.current || !current.current.selectedId
      || !current.current.loaded && !!current.current.error) return
    if (timer.current !== null) clearTimeout(timer.current)
    // The live stream delivers new events; polling the entire cloud history
    // every three seconds contends with the reminder/control worker's lock.
    const active = current.current.pending || isRemoteBusy(current.current.session)
    const delay = active ? current.current.streaming ? 12_000 : 3_000 : 15_000
    timer.current = setTimeout(() => { void readRef.current() }, delay)
  }, [])

  const applyHistory = useCallback((id: string, history: CloudHistory, full = false) => {
    const pending = pendingTurns.current.get(id)
    const snapshot = current.current
    let replyFeedback = snapshot.replyFeedback?.phase === 'complete' && history.session.status.toLowerCase() === 'idle'
      ? null : snapshot.replyFeedback
    let stopped = false
    if (!pending && (replyFeedback?.phase === 'proactive_reminder' || replyFeedback?.phase === 'proactive_update')) {
      if (history.session.status.toLowerCase() === 'terminated') {
        replyFeedback = { phase: 'error', message: history.turnError || 'AI 会话已中断，请重试。' }
      } else if (history.session.status.toLowerCase() === 'idle' || history.messages.some((message) => message.sender === 'ai'
        && !snapshot.messages.some((existing) => existing.id === message.id))) replyFeedback = null
    }
    if (pending) {
      const replied = !pending.silent && history.messages.some((message) => message.sender === 'ai' && message.source === 'chat'
        && !pending.messageIds.has(message.id))
      const turnEnded = history.session.status.toLowerCase() === 'idle' && history.idleEventId !== null
        && history.idleEventId !== pending.idleEventId
      const failed = history.session.status.toLowerCase() === 'terminated'
        || turnEnded && Date.now() - pending.startedAt > 120_000
      const silentDone = pending.silent && turnEnded
      stopped = !!pending.cancelAccepted && history.session.status.toLowerCase() === 'idle'
      if (replied || failed || silentDone || stopped) {
        pendingTurns.current.delete(id)
        replyFeedback = replied ? { phase: 'complete' }
          : silentDone ? { phase: 'sent', message: '消息已发给对方' }
            : stopped ? { phase: 'stopped' }
            : { phase: 'error', message: history.turnError || '这次没有收到 AI 回复，请稍后重试。' }
        if (stopped && !replied) { suppressStoppedError.current = true; clearStoppedLater() }
      } else if (pending.stopRequested) {
        replyFeedback = { phase: 'stopping' }
      } else if (turnEnded && !pending.silent) {
        // A tool/control turn becomes idle before its hidden action result and
        // final user-facing reply. Keep the same visible feedback until then.
        replyFeedback = { phase: 'syncing', message: '正在处理并准备回复…' }
      } else if (!pending.silent && Date.now() - pending.startedAt > 30_000) {
        replyFeedback = { phase: 'delayed', message: '回复时间较长，仍在等待并自动检查…' }
      }
    }
    update({
      session: history.session,
      replyMode: history.session.replyMode ?? '',
      messages: mergeMessages(snapshot.messages, history.messages, full)
        .filter((message) => isVisibleChatMessage(message, history.members ?? snapshot.members))
        .map((message) => pendingTurns.current.get(id)?.toolUseId === message.id ? { ...message, answered: true } : message),
      members: history.members ?? snapshot.members,
      reminders: history.reminders ?? snapshot.reminders,
      remindersError: history.remindersError ?? '',
      cursor: history.cursor ?? snapshot.cursor,
      lastIdleEventId: history.idleEventId ?? snapshot.lastIdleEventId,
      loaded: true, loading: false, slowLoading: false, refreshing: false, error: '',
      pending: pendingTurns.current.has(id),
      stopping: pendingTurns.current.has(id) && snapshot.stopping,
      replyFeedback,
      ...(['idle', 'terminated'].includes(history.session.status.toLowerCase()) ? { thinking: false } : {}),
      ...(stopped || suppressStoppedError.current ? { turnError: '' } : history.turnError !== null ? { turnError: history.turnError } : {}),
    })
  }, [clearStoppedLater, update])

  const readCloud = useCallback(async ({ full = false }: { full?: boolean } = {}) => {
    if (!mounted.current || !isAppVisible() || sendLock.current?.scope === scopeRef.current) return
    const id = pinnedId
    if (!id) {
      // 解绑后清空会话状态，SSE 与轮询随 selectedId 归零一起停掉。
      cancelRead()
      pendingTurns.current.clear()
      loadedScope.current = scope
      update(current.current.selectedId ? { ...initialState, loading: false } : { loading: false, slowLoading: false, refreshing: false })
      return
    }
    cancelRead()
    const version = readVersion.current
    const abort = new AbortController()
    controller.current = abort
    const isCurrent = () => mounted.current && isAppVisible() && scopeRef.current === scope && version === readVersion.current && !abort.signal.aborted
    const changed = id !== current.current.selectedId || loadedScope.current !== scope
    if (loadedScope.current !== scope) pendingTurns.current.clear()
    loadedScope.current = scope
    const initializing = changed || !current.current.loaded
    if (changed) {
      suppressStoppedError.current = false
      update({
        selectedId: id, session: null, messages: [], members: [], reminders: [], remindersError: '', cursor: null, lastIdleEventId: null,
        loaded: false, loading: true, slowLoading: false, refreshing: false, submitting: false, error: '', thinking: false, turnError: '', replyFeedback: null, stopping: false,
        pending: pendingTurns.current.has(id),
      })
    } else {
      update({ loading: initializing, slowLoading: false, refreshing: full && !initializing,
        ...(initializing ? { error: '' } : {}) })
    }
    if (initializing) {
      loadNoticeTimer.current = setTimeout(() => {
        if (isCurrent() && !current.current.loaded) update({ slowLoading: true })
      }, INITIAL_LOAD_NOTICE_MS)
      // Show verified recent rows while the server replays the complete cloud
      // history. The full response replaces this preview and unlocks sending.
      void qoderApi.getCachedMessages(id, abort.signal).then(({ messages }) => {
        if (isCurrent() && current.current.selectedId === id && !current.current.loaded && messages.length) {
          update({ messages: messages.filter((message) => !isTurnCancellationMarker(message) && (message.sender === 'ai' || message.userId === selfUserId || current.current.members.some((member) => member.userId === message.userId))) })
        }
      }).catch(() => {})
    }
    try {
      const lastCare = full || changed ? undefined : current.current.messages.filter((message) => message.id.startsWith('evt_care_')).reduce<Message | undefined>((latest, message) => !latest || (message.createdAt ?? '') > (latest.createdAt ?? '') || message.createdAt === latest.createdAt && message.id > latest.id ? message : latest, undefined);
      const history = await qoderApi.getMessages(id, full || changed ? null : current.current.cursor, abort.signal, lastCare?.id,
        initializing ? INITIAL_LOAD_TIMEOUT_MS : undefined)
      if (!isCurrent() || current.current.selectedId !== id) return
      applyHistory(id, history, full || changed)
    } catch (error) {
      if (isCurrent() && error instanceof ApiError && error.code === 'session_forbidden') {
        void onSessionForbidden?.()
      }
      if (isCurrent()) {
        const pendingTurn = pendingTurns.current.get(id)
        update({
          error: pendingTurn ? '' : initializing && error instanceof ApiError && error.code === 'TIMEOUT'
            ? '连接云端超过 30 秒，请检查网络或服务状态后重试。' : errorMessage(error),
          loading: false, slowLoading: false, refreshing: false,
          ...(pendingTurn?.stopRequested ? { replyFeedback: { phase: 'stopping' } as ReplyFeedback }
            : pendingTurn && !pendingTurn.silent
            ? { replyFeedback: { phase: 'delayed', message: '回复还在同步，正在自动重试…' } as ReplyFeedback } : {}),
        })
      }
    } finally {
      if (loadNoticeTimer.current !== null && isCurrent()) {
        clearTimeout(loadNoticeTimer.current)
        loadNoticeTimer.current = null
      }
      if (isCurrent()) {
        controller.current = null
        schedule()
      }
    }
  }, [pinnedId, scope, onSessionForbidden, applyHistory, cancelRead, schedule, update])
  readRef.current = readCloud

  const reload = useCallback(() => readCloud({ full: true }), [readCloud])

  const saveReminder = useCallback(async (input: { title: string; dueAt: string; recipientIds: number[]; recurrence?: ReminderRecurrence }) => {
    const id = current.current.selectedId
    const operationScope = scopeRef.current
    if (!id || !current.current.loaded) throw new Error('请先绑定并加载共享空间，再添加提醒。')
    const { reminder } = await qoderApi.createReminder(id, input)
    if (mounted.current && current.current.selectedId === id && scopeRef.current === operationScope) {
      update({ reminders: [...current.current.reminders.filter((item) => item.id !== reminder.id), reminder] })
      void readRef.current()
    }
  }, [update])

  const changeReminder = useCallback(async (reminderId: string, status: 'completed' | 'scheduled' | 'cancelled') => {
    const id = current.current.selectedId
    const operationScope = scopeRef.current
    if (!id || !current.current.loaded) throw new Error('共享提醒还未加载，请稍后再试。')
    const { reminder } = await qoderApi.updateReminder(id, reminderId, status)
    if (mounted.current && current.current.selectedId === id && scopeRef.current === operationScope) {
      const currentReminder = { ...reminder, updatedAt: new Date().toISOString() }
      update({ reminders: current.current.reminders.map((item) => item.id === reminder.id ? currentReminder : item) })
      void readRef.current()
    }
  }, [update])

  const deleteReminder = useCallback(async (reminderId: string) => {
    const id = current.current.selectedId
    const operationScope = scopeRef.current
    if (!id || !current.current.loaded) throw new Error('共享提醒还未加载，请稍后再试。')
    await qoderApi.deleteReminder(id, reminderId)
    if (mounted.current && current.current.selectedId === id && scopeRef.current === operationScope) {
      update({ reminders: current.current.reminders.filter((item) => item.id !== reminderId) })
      void readRef.current()
    }
  }, [update])

  const sendMessage = useCallback(async (text: string, files: MiniFile[] = [], visibility: 'shared' | 'private' = 'shared', silent = false): Promise<boolean> => {
    const snapshot = current.current
    const draft = text.trim()
    if ((!draft && !files.length) || sendLock.current?.scope === scopeRef.current) return false
    if (!snapshot.selectedId || !snapshot.loaded || snapshot.error || snapshot.pending
      || snapshot.session?.status.toLowerCase() !== 'idle') {
      throw new Error('请等待会话同步完成或当前回复结束后再发送。')
    }
    if (draft.length > 2_000) throw new Error('这条消息有点长，请控制在 2000 字以内。')
    if (!selfUserId) throw new Error('账号信息还未加载，请稍后再发送。')
    const id = snapshot.selectedId
    const submissionScope = scopeRef.current
    suppressStoppedError.current = false
    const localId = `local_${Date.now()}_${++localMessageSequence.current}`
    const now = new Date()
    const optimistic: Message = {
      id: localId, sender: 'self', userId: selfUserId,
      displayName: snapshot.members.find((member) => member.userId === selfUserId)?.name,
      source: 'chat', kind: 'text', text: draft, visibility,
      files: files.map((file) => file.name), time: `${String((now.getUTCHours() + 8) % 24).padStart(2, '0')}:${String(now.getUTCMinutes()).padStart(2, '0')}`,
      createdAt: now.toISOString(), localStatus: 'sending',
    }
    let finishSubmission = () => {}
    const submissionDone = new Promise<void>((resolve) => { finishSubmission = resolve })
    const pendingTurn: PendingTurn = {
      startedAt: Date.now(),
      messageIds: new Set(snapshot.messages.map((message) => message.id)),
      idleEventId: snapshot.lastIdleEventId,
      silent, visibility, dispatched: false, submissionDone, finishSubmission,
    }
    const submissionLock = { scope: submissionScope, operation: Symbol() }
    sendLock.current = submissionLock
    cancelRead()
    pendingTurns.current.set(id, pendingTurn)
    update({ messages: mergeMessages(snapshot.messages, [optimistic]), submitting: true, pending: true, replyMode: silent ? 'silent' : '', error: '', turnError: '', refreshing: false,
      replyFeedback: { phase: 'sending' } })
    let shouldRead = false
    try {
      const result = await qoderApi.sendMessage(id, draft, files, visibility, () => {
        if (pendingTurn.stopRequested || !mounted.current || scopeRef.current !== submissionScope || current.current.selectedId !== id) return false
        pendingTurn.dispatched = true
        return true
      })
      shouldRead = true
      const acceptedTurn = pendingTurns.current.get(id)
      if (acceptedTurn === pendingTurn && result.replyMode === 'silent') acceptedTurn.silent = true
      if (mounted.current && current.current.selectedId === id && scopeRef.current === submissionScope) {
        update({ replyMode: result.replyMode ?? '', messages: mergeMessages(current.current.messages, result.messages)
          .map((message) => message.id === localId ? { ...message, localStatus: 'sent' as const } : message)
          .filter((message) => isVisibleChatMessage(message, current.current.members)),
        ...(current.current.replyFeedback?.phase === 'sending'
          ? { replyFeedback: silent || result.replyMode === 'silent'
            ? { phase: 'sent', message: '消息已发给对方' } as ReplyFeedback
            : { phase: 'waiting' } as ReplyFeedback }
          : {}) })
      }
      // An accepted POST is success even if the following history read fails.
      return true
    } catch (error) {
      if (error instanceof SendCancelledError) {
        if (pendingTurns.current.get(id) === pendingTurn) pendingTurns.current.delete(id)
        if (mounted.current && current.current.selectedId === id && scopeRef.current === submissionScope) {
          suppressStoppedError.current = true
          clearStoppedLater()
          update({
            messages: current.current.messages.filter((message) => message.id !== localId),
            pending: false, stopping: false, replyFeedback: { phase: 'stopped' },
          })
        }
        return false
      }
      const uncertain = error instanceof ApiError && (error.status >= 500 || !!error.code && ['TIMEOUT', 'NETWORK_ERROR', 'upstream_timeout', 'connection_failed', 'LOCAL_IMAGE_ERROR'].includes(error.code))
      const stopRequested = !!pendingTurns.current.get(id)?.stopRequested
      if (uncertain) shouldRead = true
      if (!uncertain) if (pendingTurns.current.get(id) === pendingTurn) pendingTurns.current.delete(id)
      if (mounted.current && current.current.selectedId === id && scopeRef.current === submissionScope) {
        update({
          messages: current.current.messages.map((message) => message.id === localId
            ? { ...message, localStatus: uncertain ? 'uncertain' : 'failed' } : message),
          pending: uncertain, stopping: uncertain && stopRequested, error: '', replyFeedback: uncertain
            ? stopRequested ? { phase: 'stopping' } : { phase: 'delayed', message: '消息发送状态待确认，正在自动检查…' }
            : null,
        })
      }
      if (uncertain) return true
      throw error
    } finally {
      if (sendLock.current === submissionLock) sendLock.current = null
      finishSubmission()
      if (mounted.current && current.current.selectedId === id && scopeRef.current === submissionScope) {
        update({ submitting: false })
        if (shouldRead) void readRef.current()
        else schedule()
      } else if (mounted.current && isAppVisible()) void readRef.current()
    }
  }, [cancelRead, clearStoppedLater, schedule, selfUserId, update])

  const stopTurn = useCallback(async (): Promise<void> => {
    const id = current.current.selectedId
    const submissionScope = scopeRef.current
    const pending = id ? pendingTurns.current.get(id) : undefined
    if (!id || !pending || pending.silent || stopLock.current?.scope === submissionScope) return
    const operationLock = { scope: submissionScope, operation: Symbol() }
    stopLock.current = operationLock
    pending.stopRequested = true
    update({ stopping: true, replyFeedback: { phase: 'stopping' } })
    try {
      if (!pending.dispatched) return
      const result = await qoderApi.cancelTurn(id, pending.visibility)
      if (result.status === 'idle') {
        await pending.submissionDone
        if (pendingTurns.current.get(id) !== pending || scopeRef.current !== submissionScope) return
        await qoderApi.cancelTurn(id, pending.visibility)
      }
      pending.cancelAccepted = true
      if (mounted.current && current.current.selectedId === id && scopeRef.current === submissionScope) void readRef.current({ full: true })
    } catch (error) {
      pending.stopRequested = false
      if (mounted.current && current.current.selectedId === id && scopeRef.current === submissionScope && pendingTurns.current.has(id)) {
        update({ stopping: false, replyFeedback: { phase: 'waiting' } })
      }
      throw error
    } finally {
      if (stopLock.current === operationLock) stopLock.current = null
    }
  }, [update])

  useEffect(() => {
    mounted.current = true
    void reload()
    return () => { mounted.current = false; cancelRead(); if (stoppedTimer.current) clearTimeout(stoppedTimer.current) }
  }, [cancelRead, reload])

  useEffect(() => onAppVisibilityChange((active) => {
    setVisible(active)
    if (active) void readRef.current()
    else { cancelRead(); update({ streaming: false }) }
  }), [cancelRead, update])

  /** 回答云端选择题（AskUserQuestion）：回传后本轮继续，所以按发送一样置忙并刷新。 */
  const answerAsk = useCallback(async (toolUseId: string, text: string): Promise<boolean> => {
    const snapshot = current.current
    const id = snapshot.selectedId
    const submissionScope = scopeRef.current
    suppressStoppedError.current = false
    if (!id || sendLock.current?.scope === scopeRef.current) throw new Error('会话还没准备好，请稍后再试。')
    if (pendingTurns.current.has(id)) throw new Error('上一次回答还在确认，请等待同步，避免重复发送。')
    if (snapshot.messages.find((message) => message.id === toolUseId)?.answered) throw new Error('这道问题已经回答过了。')
    const submissionLock = { scope: submissionScope, operation: Symbol() }
    sendLock.current = submissionLock
    cancelRead()
    let finishSubmission = () => {}
    const submissionDone = new Promise<void>((resolve) => { finishSubmission = resolve })
    const pendingTurn: PendingTurn = {
      startedAt: Date.now(),
      messageIds: new Set(snapshot.messages.map((message) => message.id)),
      idleEventId: snapshot.lastIdleEventId,
      silent: false,
      visibility: snapshot.messages.find((message) => message.id === toolUseId)?.visibility === 'private' ? 'private' : 'shared',
      dispatched: true, toolUseId, submissionDone, finishSubmission,
    }
    pendingTurns.current.set(id, pendingTurn)
    update({ submitting: true, pending: true, error: '', replyFeedback: { phase: 'sending' } })
    try {
      await qoderApi.sendToolResult(id, toolUseId, text)
      if (mounted.current && current.current.selectedId === id && scopeRef.current === submissionScope) {
        // 增量刷新取不到更早的 ask 事件，先本地标记已回答，免得卡片还能再点一次。
        update({ messages: current.current.messages.map((message) => (message.id === toolUseId ? { ...message, answered: true } : message)),
          ...(current.current.replyFeedback?.phase === 'sending' ? { replyFeedback: { phase: 'waiting' } as ReplyFeedback } : {}) })
      }
      return true
    } catch (error) {
      const uncertain = error instanceof ApiError && (error.status >= 500 || ['TIMEOUT', 'NETWORK_ERROR', 'upstream_timeout', 'connection_failed', 'LOCAL_IMAGE_ERROR'].includes(error.code ?? ''))
      if (!uncertain && pendingTurns.current.get(id) === pendingTurn) pendingTurns.current.delete(id)
      if (mounted.current && current.current.selectedId === id && scopeRef.current === submissionScope) update({
        pending: uncertain, error: '',
        ...(uncertain ? { messages: current.current.messages.map((message) => message.id === toolUseId ? { ...message, answered: true } : message) } : {}),
        replyFeedback: uncertain ? { phase: 'delayed', message: '回答发送状态待确认，正在自动检查，请勿重复回答。' } : { phase: 'error', message: errorMessage(error) },
      })
      if (uncertain) return true
      throw error
    } finally {
      if (sendLock.current === submissionLock) sendLock.current = null
      finishSubmission()
      if (mounted.current && current.current.selectedId === id && scopeRef.current === submissionScope) {
        update({ submitting: false })
        void readRef.current()
      } else if (mounted.current && isAppVisible()) void readRef.current()
    }
  }, [cancelRead, update])

  const hasPrivateChannel = state.messages.some((message) => message.visibility === 'private')

  useEffect(() => {
    const id = state.selectedId
    if (!state.loaded || !id || !visible) return
    const streamScope = scopeRef.current
    const source = qoderApi.stream(id, current.current.cursor)
    source.onopen = () => { if (isAppVisible() && scopeRef.current === streamScope) update({ streaming: true }) }
    source.onerror = () => { if (scopeRef.current === streamScope) update({ streaming: false }) }
    source.onmessage = (event) => {
      let item: CloudStreamEvent & { private?: boolean }
      try { item = JSON.parse(event.data) as CloudStreamEvent & { private?: boolean } } catch { return }
      if (!isAppVisible() || current.current.selectedId !== id || scopeRef.current !== streamScope) return
      const snapshot = current.current
      if (item.private && item.type === 'status') { void readRef.current({ full: true }); return }
      const nextCursor = item.private ? snapshot.cursor : item.id
      const pendingTurn = pendingTurns.current.get(id)
      const hasProactiveFeedback = snapshot.replyFeedback?.phase === 'proactive_reminder'
        || snapshot.replyFeedback?.phase === 'proactive_update'
      if (item.type === 'start') {
        const proactiveFeedback: ReplyFeedback | null = !pendingTurn && item.proactive
          ? { phase: item.proactive === 'reminder' ? 'proactive_reminder' : 'proactive_update' } : null
        if (item.kind === 'thinking') update({ thinking: true, ...(pendingTurn && !pendingTurn.silent && !pendingTurn.stopRequested ? { replyFeedback: { phase: 'thinking' } as ReplyFeedback } : proactiveFeedback ? { replyFeedback: proactiveFeedback } : {}) })
        else update({ thinking: false, ...(pendingTurn && !pendingTurn.silent && !pendingTurn.stopRequested ? { replyFeedback: { phase: 'replying' } as ReplyFeedback } : proactiveFeedback ? { replyFeedback: proactiveFeedback } : {}) })
      } else if (item.type === 'delta') {
        // The assistant streams a structured envelope. Only server-parsed final
        // messages are safe to display; keep the thinking indicator until then.
        update({ thinking: true, ...(pendingTurn && !pendingTurn.silent && !pendingTurn.stopRequested ? { replyFeedback: { phase: 'replying' } as ReplyFeedback } : {}) })
      } else if (item.type === 'message') {
        if (item.message && item.message.sender !== 'ai') update({ replyMode: item.message.replyMode ?? '' })
        if (item.message && isVisibleChatMessage(item.message, snapshot.members)) {
          const replied = !!pendingTurn && item.message.sender === 'ai' && item.message.source === 'chat'
            && !pendingTurn.messageIds.has(item.message.id)
          if (replied) pendingTurns.current.delete(id)
          update({ messages: mergeMessages(current.current.messages, [item.message]), cursor: nextCursor, thinking: false,
            pending: pendingTurns.current.has(id), stopping: pendingTurns.current.has(id) && snapshot.stopping, ...(item.message.sender === 'ai' ? { turnError: '' } : {}),
            ...(replied || item.message.sender === 'ai' && (snapshot.replyFeedback?.phase === 'proactive_reminder' || snapshot.replyFeedback?.phase === 'proactive_update')
              ? { replyFeedback: { phase: 'complete' } as ReplyFeedback } : {}) })
        }
        else update({ cursor: nextCursor })
      } else if (item.type === 'thinking_end') {
        update({ thinking: false, cursor: nextCursor,
          ...(pendingTurn && !pendingTurn.silent && !pendingTurn.stopRequested ? { replyFeedback: { phase: 'syncing' } as ReplyFeedback } : {}) })
      } else if (item.type === 'status') {
        const status = item.status
        const stopped = status === 'idle' && !!pendingTurn?.cancelAccepted && item.id !== pendingTurn.idleEventId
        if (status === 'terminated' || stopped) pendingTurns.current.delete(id)
        if (stopped) { suppressStoppedError.current = true; clearStoppedLater() }
        update({
          session: snapshot.session ? { ...snapshot.session, status } : null,
          cursor: nextCursor, pending: pendingTurns.current.has(id), stopping: pendingTurns.current.has(id) && snapshot.stopping,
          thinking: status === 'idle' || status === 'terminated' ? false : snapshot.thinking,
          ...(status === 'idle' ? { lastIdleEventId: item.id } : {}),
          ...(stopped ? { replyFeedback: { phase: 'stopped' } as ReplyFeedback, turnError: '' }
            : pendingTurn && !pendingTurn.silent && status === 'idle' ? { replyFeedback: { phase: 'syncing' } as ReplyFeedback } : {}),
          ...(!pendingTurn && status === 'idle' && snapshot.replyFeedback?.phase === 'complete' ? { replyFeedback: null } : {}),
          ...((pendingTurn || hasProactiveFeedback) && status === 'terminated' ? { replyFeedback: { phase: 'error', message: 'AI 会话已中断，请重试。' } as ReplyFeedback } : {}),
        })
        if (status === 'idle' || status === 'terminated') void readRef.current({ full: true })
      } else if (item.type === 'session_error') {
        if (pendingTurn && !pendingTurn.stopRequested) pendingTurns.current.delete(id)
        update({ turnError: snapshot.replyMode === 'silent' || pendingTurn?.stopRequested || suppressStoppedError.current ? '' : item.message, cursor: nextCursor, thinking: false,
          pending: pendingTurns.current.has(id), stopping: pendingTurns.current.has(id) && snapshot.stopping,
          ...(pendingTurn?.stopRequested ? { replyFeedback: { phase: 'stopping' } as ReplyFeedback }
            : pendingTurn || hasProactiveFeedback ? { replyFeedback: { phase: 'error', message: item.message || 'AI 回复失败，请重试。' } as ReplyFeedback } : {}) })
      }
    }
    const privateSource = hasPrivateChannel ? qoderApi.privateStream(id) : null
    if (privateSource) privateSource.onmessage = source.onmessage
    return () => { source.close(); privateSource?.close(); update({ streaming: false }) }
  }, [state.selectedId, state.loaded, hasPrivateChannel, visible, clearStoppedLater, update])

  const busy = state.pending || isRemoteBusy(state.session)
  const replyFeedback = state.replyFeedback?.phase === 'complete' ? null : state.replyFeedback
    ?? (busy || state.thinking ? { phase: 'waiting' } as ReplyFeedback : null)
  // 云端在等待工具应答时状态仍是 idle，但此时发消息会被上游拒 409，所以按忙处理。
  const awaitingAsk = state.messages.some((message) => message.kind === 'ask' && !message.answered)
  const canSend = state.loaded && !!state.selectedId && !state.error && !state.submitting
    && !busy && !awaitingAsk && state.session?.status.toLowerCase() === 'idle'
  const canStop = !!(state.selectedId && pendingTurns.current.get(state.selectedId) && !pendingTurns.current.get(state.selectedId)?.silent)
  return {
    selectedId: state.selectedId, session: state.session, pinned: !!pinnedId,
    messages: state.messages, loaded: state.loaded, loading: state.loading, slowLoading: state.slowLoading, refreshing: state.refreshing,
    members: state.members, reminders: state.reminders, remindersError: state.remindersError,
    error: state.error, submitting: state.submitting, stopping: state.stopping, busy, awaitingAsk, canSend, canStop,
    thinking: state.thinking, streaming: state.streaming, replyFeedback,
    silent: state.replyMode === 'silent',
    turnError: state.turnError,
    reload, sendMessage, stopTurn, answerAsk, saveReminder, changeReminder, deleteReminder,
  }
}
