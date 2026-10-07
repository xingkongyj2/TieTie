export type VoiceRecognitionPhase = 'idle' | 'preparing' | 'listening' | 'processing'

export interface VoiceRecognitionSnapshot {
  phase: VoiceRecognitionPhase
  transcript: string
  elapsedMs: number
  cancelIntent: boolean
}

export interface RecognitionResult {
  result?: string
  tempFilePath?: string
  duration?: number
  fileSize?: number
}

export interface RecognitionError {
  retcode?: number
  msg?: string
  errMsg?: string
}

export interface RecognitionManager {
  onStart?: (event?: unknown) => void
  onRecognize?: (event: RecognitionResult) => void
  onStop?: (event: RecognitionResult) => void
  onError?: (event: RecognitionError) => void
  start(options: { duration: number; lang: 'zh_CN' }): void
  stop(): void
  cancel?(): void
}

export interface VoiceRecognitionClock {
  now(): number
  setTimeout(callback: () => void, delayMs: number): unknown
  clearTimeout(timer: unknown): void
}

export interface VoiceRecognitionDependencies {
  // false means permission was just granted: a fresh press is required.
  authorize(): Promise<boolean>
  getManager(): RecognitionManager
  onResult(text: string): void
  onError(message: string): void
  clock?: VoiceRecognitionClock
}

export const VOICE_MAX_DURATION_MS = 60_000
export const VOICE_START_TIMEOUT_MS = 15_000
export const VOICE_PROCESS_TIMEOUT_MS = 20_000

const idleSnapshot = (): VoiceRecognitionSnapshot => ({ phase: 'idle', transcript: '', elapsedMs: 0, cancelIntent: false })
const defaultClock: VoiceRecognitionClock = {
  now: () => Date.now(),
  setTimeout: (callback, delayMs) => setTimeout(callback, delayMs),
  clearTimeout: timer => clearTimeout(timer as ReturnType<typeof setTimeout>),
}

interface Session {
  manager: RecognitionManager
  startInvoked: boolean
  started: boolean
  startedAt: number
  released: boolean
  cancelled: boolean
  cancelIntent: boolean
  terminal: boolean
  stopSent: boolean
  earlyStopSent: boolean
  cancelSent: boolean
  releasedDuringAuthorization?: boolean
  startupTimer?: unknown
  processingTimer?: unknown
  tickTimer?: unknown
  maximumTimer?: unknown
}

// Native managers are singletons. The lease must survive component/account changes.
// A timeout alone never releases it: callbacks contain no request identifier.
const managerLeases = new WeakMap<RecognitionManager, Session>()

export function voiceRecognitionErrorMessage(error: unknown): string {
  const value = error && typeof error === 'object' ? error as RecognitionError & { message?: string } : {}
  const message = `${value.msg ?? ''} ${value.errMsg ?? ''} ${value.message ?? ''}`
  // Only fixed messages may describe cloud failures; remote details can contain signed URLs.
  if (value.retcode === 4002) return '语音服务认证失败，请稍后重试'
  if (value.retcode === 4003) return '语音服务尚未开通，请稍后再试'
  if (value.retcode === 4004) return '语音服务额度不足，请稍后再试'
  if (value.retcode === 4005) return '语音服务暂不可用，请稍后再试'
  if (value.retcode === 4006) return '语音识别使用较频繁，请稍后再试'
  if (value.retcode === 4007) return '录音格式暂不支持，请更新微信后重试'
  if (value.retcode === 4008 || value.retcode === 4009 || value.retcode === 5000 || value.retcode === 5001 || value.retcode === 5002) return '网络不太稳定，请检查网络后重试'
  if (value.retcode === -40001) return '语音识别使用较频繁，请稍后再试'
  if (value.retcode === -40002) return '登录已过期，请重新登录'
  if (value.retcode === -30011) return '上一段语音还在结束，请稍后再试'
  if (value.retcode === -30012) return '录音已结束，请重新按住说话'
  if (/auth|permission|deny|denied|scope\.record|授权|权限/i.test(message)) return '需要开启麦克风权限后才能语音输入'
  if (/network|connect|socket|网络/i.test(message)) return '网络不太稳定，请检查网络后重试'
  if (/not supported|not available|plugin|requirePlugin|插件|不支持/i.test(message)) return '语音服务暂不可用，请更新微信或稍后重试'
  if (/too short|too small|短|无声|silence/i.test(message)) return '没有听清，请按住后说久一点再试'
  return '语音识别失败，请重新按住说话'
}

const dependencyErrorMessage = (error: unknown) => error instanceof Error && error.message.trim()
  ? error.message.trim()
  : voiceRecognitionErrorMessage(error)

export function createVoiceRecognition(deps: VoiceRecognitionDependencies) {
  const clock = deps.clock ?? defaultClock
  const listeners = new Set<() => void>()
  let snapshot = idleSnapshot()
  let active: Session | null = null
  let disposed = false
  let authorizationGeneration = 0

  const update = (next: VoiceRecognitionSnapshot) => {
    snapshot = next
    for (const listener of listeners) listener()
  }
  const clearTimer = (session: Session, key: 'startupTimer' | 'processingTimer' | 'tickTimer' | 'maximumTimer') => {
    if (session[key] !== undefined) clock.clearTimeout(session[key])
    session[key] = undefined
  }
  const clearTimers = (session: Session) => {
    clearTimer(session, 'startupTimer')
    clearTimer(session, 'processingTimer')
    clearTimer(session, 'tickTimer')
    clearTimer(session, 'maximumTimer')
  }
  const release = (session: Session) => {
    session.terminal = true
    clearTimers(session)
    if (managerLeases.get(session.manager) === session) managerLeases.delete(session.manager)
    if (active === session) {
      active = null
      update(idleSnapshot())
    }
  }
  const abandon = (session: Session, message?: string) => {
    const shouldNotify = active === session && !session.cancelled && !disposed
    session.cancelled = true
    session.released = true
    clearTimer(session, 'processingTimer')
    clearTimer(session, 'tickTimer')
    clearTimer(session, 'maximumTimer')
    if (session.started || session.manager.cancel) clearTimer(session, 'startupTimer')
    if (active === session) {
      active = null
      update(idleSnapshot())
    }
    if (!session.startInvoked) release(session)
    else if (session.manager.cancel && !session.cancelSent && !session.terminal) {
      session.cancelSent = true
      // The adapter may synchronously emit onStop. Cancellation and detachment
      // must happen first so a final transcript can never be delivered here.
      try { session.manager.cancel() } catch { /* Keep the lease until native termination. */ }
    }
    if (message && shouldNotify) deps.onError(message)
  }
  const stop = (session: Session, forceBeforeStart = false) => {
    if (session.terminal || session.cancelSent) return
    if (session.started) {
      if (session.stopSent) return
      session.stopSent = true
    } else {
      if (!forceBeforeStart || session.earlyStopSent) return
      session.earlyStopSent = true
    }
    try {
      session.manager.stop()
    } catch {
      abandon(session, '录音结束失败，请重新进入小程序后重试')
    }
  }
  const processingTimeout = (session: Session) => {
    if (session.processingTimer !== undefined) return
    session.processingTimer = clock.setTimeout(() => {
      session.processingTimer = undefined
      if (session.terminal) return
      abandon(session, '语音识别超时，请重新进入小程序后重试')
      stop(session, true)
    }, VOICE_PROCESS_TIMEOUT_MS)
  }
  const elapsed = (session: Session) => Math.min(VOICE_MAX_DURATION_MS, Math.max(0, clock.now() - session.startedAt))
  const finishSession = (session: Session) => {
    if (session.terminal || session.released) return
    session.released = true
    if (session.cancelIntent || !session.startInvoked) {
      session.releasedDuringAuthorization = !session.cancelIntent && !session.startInvoked
      abandon(session)
      stop(session, true)
      return
    }
    clearTimer(session, 'tickTimer')
    clearTimer(session, 'maximumTimer')
    if (active === session) update({ ...snapshot, phase: 'processing', elapsedMs: session.started ? elapsed(session) : 0 })
    processingTimeout(session)
    // Tencent can abort pending setup; WechatSI must wait for onStart.
    stop(session, !!session.manager.cancel)
  }
  const tick = (session: Session) => {
    session.tickTimer = clock.setTimeout(() => {
      session.tickTimer = undefined
      if (active !== session || session.released || session.terminal) return
      update({ ...snapshot, elapsedMs: elapsed(session) })
      tick(session)
    }, 1000)
  }
  const attachCallbacks = (session: Session) => {
    session.manager.onStart = () => {
      if (session.terminal || session.started) return
      session.started = true
      session.startedAt = clock.now()
      clearTimer(session, 'startupTimer')
      if (session.cancelled || session.released || disposed || active !== session) {
        stop(session)
        return
      }
      update({ ...snapshot, phase: 'listening', elapsedMs: 0 })
      tick(session)
      session.maximumTimer = clock.setTimeout(() => finishSession(session), VOICE_MAX_DURATION_MS)
    }
    session.manager.onRecognize = event => {
      if (session.terminal || session.cancelled || active !== session || disposed) return
      if (typeof event.result === 'string') update({ ...snapshot, transcript: event.result })
    }
    session.manager.onStop = event => {
      if (session.terminal) return
      const shouldDeliver = active === session && !session.cancelled && !session.cancelIntent && !disposed
      const text = typeof event.result === 'string' ? event.result.trim() : ''
      release(session)
      if (!shouldDeliver) return
      if (text) deps.onResult(text)
      else deps.onError('没有听清，请按住后再说一次')
    }
    session.manager.onError = error => {
      if (session.terminal) return
      // Duplicate start reports an existing live task, not a drained recorder.
      if (error.retcode === -30011) {
        abandon(session, voiceRecognitionErrorMessage(error))
        stop(session, true)
        return
      }
      // An early stop can race the delayed start acknowledgement.
      if (error.retcode === -30012 && !session.started && session.earlyStopSent) return
      const shouldNotify = active === session && !session.cancelled && !disposed
      release(session)
      if (shouldNotify) deps.onError(voiceRecognitionErrorMessage(error))
    }
  }

  return {
    getSnapshot: () => snapshot,
    subscribe(listener: () => void) {
      if (disposed) return () => {}
      listeners.add(listener)
      return () => { listeners.delete(listener) }
    },
    async start(): Promise<void> {
      if (disposed || active) return
      const authorizationAttempt = ++authorizationGeneration
      let manager: RecognitionManager
      try {
        manager = deps.getManager()
      } catch (error) {
        deps.onError(dependencyErrorMessage(error))
        return
      }
      if (managerLeases.has(manager)) {
        deps.onError('上一段语音还在结束，请稍后再试')
        return
      }
      const session: Session = {
        manager, startInvoked: false, started: false, startedAt: 0,
        released: false, cancelled: false, cancelIntent: false, terminal: false,
        stopSent: false, earlyStopSent: false, cancelSent: false,
      }
      active = session
      managerLeases.set(manager, session)
      update({ ...idleSnapshot(), phase: 'preparing' })
      session.startupTimer = clock.setTimeout(() => {
        session.startupTimer = undefined
        if (session.terminal || session.started) return
        abandon(session, session.startInvoked ? '录音启动超时，请重新进入小程序后重试' : '麦克风授权超时，请重新按住说话')
        stop(session, true)
      }, VOICE_START_TIMEOUT_MS)
      try {
        const authorized = await deps.authorize()
        // A permission dialog normally outlives the original finger press.
        // Its ready hint may survive that release, but never a context change.
        if (!authorized && session.releasedDuringAuthorization
          && authorizationAttempt === authorizationGeneration && !disposed) {
          deps.onError('麦克风已准备好，请重新按住说话')
          return
        }
        if (active !== session || session.cancelled || session.terminal || disposed) return
        if (!authorized || session.released) {
          release(session)
          if (!authorized && !disposed) deps.onError('麦克风已准备好，请重新按住说话')
          return
        }
        attachCallbacks(session)
        session.startInvoked = true
        manager.start({ duration: VOICE_MAX_DURATION_MS, lang: 'zh_CN' })
      } catch (error) {
        if (session.terminal || session.cancelled) return
        abandon(session, session.startInvoked ? voiceRecognitionErrorMessage(error) : dependencyErrorMessage(error))
        stop(session, true)
      }
    },
    setCancelIntent(value: boolean) {
      if (!active || active.released || active.cancelled) return
      active.cancelIntent = value
      update({ ...snapshot, cancelIntent: value })
    },
    finish() {
      if (active) finishSession(active)
    },
    cancel() {
      authorizationGeneration++
      if (!active) return
      const session = active
      abandon(session)
      stop(session, true)
    },
    dispose() {
      if (disposed) return
      authorizationGeneration++
      disposed = true
      if (active) {
        const session = active
        abandon(session)
        stop(session, true)
      }
      listeners.clear()
    },
  }
}

export type VoiceRecognitionController = ReturnType<typeof createVoiceRecognition>
