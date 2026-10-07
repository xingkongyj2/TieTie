import Taro from '@tarojs/taro'
import { voiceApi, type VoiceSession } from '../api/voice'
import { ApiError } from '../api/transport'
import { AbortController, type AbortSignalLike } from './abort'
import type { RecognitionError, RecognitionManager } from './voiceRecognition'

export interface VoiceRecorder {
  start(options: { duration: number; sampleRate: 16000; numberOfChannels: 1; format: 'PCM'; frameSize: number }): void
  stop(): void
  onStart(callback: () => void): void
  onStop(callback: () => void): void
  onError(callback: () => void): void
  onFrameRecorded(callback: (event: { frameBuffer: ArrayBuffer; isLastFrame: boolean }) => void): void
  onInterruptionBegin?(callback: () => void): void
}
export interface VoiceSocket {
  send(options: { data: string | ArrayBuffer; success: () => void; fail: () => void }): unknown
  close(options: { code: number; reason: string; fail?: () => void }): unknown
  onOpen(callback: () => void): void
  onMessage(callback: (event: { data: unknown }) => void): void
  onError(callback: () => void): void
  onClose(callback: () => void): void
}
export interface TencentVoiceDependencies {
  getSession(signal: AbortSignalLike): Promise<VoiceSession>
  getRecorder(): VoiceRecorder
  connect(url: string): VoiceSocket | Promise<VoiceSocket>
}
interface Attempt {
  abort: AbortController
  socket?: VoiceSocket
  nativeInvoked: boolean
  nativeDrained: boolean
  stopping: boolean
  endSent: boolean
  cancelled: boolean
  terminal: boolean
  error?: RecognitionError
  final: boolean
  handshake: boolean
  voiceId: string
  parts: Map<number, { text: string; stable: boolean }>
  queuedBytes: number
  sendQueue: Promise<void>
  timeout?: ReturnType<typeof setTimeout>
  sinks: Pick<RecognitionManager, 'onStart' | 'onRecognize' | 'onStop' | 'onError'>
}
function consumeRejection(value: unknown) {
  const task = value as { catch?: (callback: () => void) => unknown } | undefined
  if (typeof task?.catch === 'function') void task.catch(() => {})
}

// A cancelled native recording retains its lease until native onStop/onError.
export function createTencentVoiceManager(deps: TencentVoiceDependencies): RecognitionManager {
  let active: Attempt | undefined
  let recorder: VoiceRecorder | undefined
  const current = (attempt: Attempt) => active === attempt && !attempt.terminal
  const transcript = (attempt: Attempt) => [...attempt.parts].sort((a, b) => a[0] - b[0]).map(([, part]) => part.text).join('')
  const close = (attempt: Attempt) => {
    if (!attempt.socket) return
    try { consumeRejection(attempt.socket.close({ code: 1000, reason: 'complete', fail: () => {} })) } catch { /* Already closed. */ }
  }
  const complete = (attempt: Attempt) => {
    if (!current(attempt) || (attempt.nativeInvoked && !attempt.nativeDrained)) return
    attempt.terminal = true
    clearTimeout(attempt.timeout)
    attempt.abort.abort()
    active = undefined
    close(attempt)
    if (attempt.cancelled) attempt.sinks.onStop?.({ result: '' })
    else if (attempt.error) attempt.sinks.onError?.(attempt.error)
    else attempt.sinks.onStop?.({ result: transcript(attempt) })
  }
  const stopNative = (attempt: Attempt) => {
    if (!attempt.nativeInvoked || attempt.nativeDrained) return
    try { recorder?.stop() } catch { /* A delayed native onStart retries stop. */ }
  }
  const fail = (attempt: Attempt, code = 4009) => {
    if (!current(attempt) || attempt.cancelled || attempt.error) return
    attempt.error = { retcode: code, msg: code === 4009 ? 'network connection failed' : 'speech service failed' }
    attempt.abort.abort()
    close(attempt)
    stopNative(attempt)
    complete(attempt)
  }
  const send = (attempt: Attempt, data: string | ArrayBuffer): Promise<void> => new Promise((resolve, reject) => {
    if (!current(attempt) || attempt.cancelled || attempt.error || !attempt.socket) { resolve(); return }
    try { consumeRejection(attempt.socket.send({ data, success: resolve, fail: () => reject(new Error('speech send failed')) })) }
    catch { reject(new Error('speech send failed')) }
  })
  const enqueue = (attempt: Attempt, data: string | ArrayBuffer) => {
    const bytes = typeof data === 'string' ? 0 : data.byteLength
    attempt.queuedBytes += bytes
    if (attempt.queuedBytes > 64_000) { fail(attempt); return }
    attempt.sendQueue = attempt.sendQueue.then(() => send(attempt, data)).then(() => { attempt.queuedBytes -= bytes }).catch(() => { fail(attempt) })
  }
  const end = (attempt: Attempt) => {
    if (!current(attempt) || attempt.cancelled || attempt.error || attempt.endSent) return
    attempt.endSent = true
    // Queue end after the final PCM frames have been sent.
    enqueue(attempt, JSON.stringify({ type: 'end' }))
  }
  const ensureRecorder = () => {
    if (recorder) return recorder
    recorder = deps.getRecorder()
    recorder.onStart(() => {
      const attempt = active
      if (!attempt || !attempt.nativeInvoked || attempt.nativeDrained) return
      if (attempt.cancelled || attempt.error || attempt.stopping) { stopNative(attempt); return }
      attempt.sinks.onStart?.()
    })
    recorder.onFrameRecorded(event => {
      const attempt = active
      if (!attempt || !attempt.nativeInvoked || attempt.nativeDrained || attempt.cancelled || attempt.error || attempt.endSent) return
      if (!(event.frameBuffer instanceof ArrayBuffer)) { fail(attempt, 4007); return }
      enqueue(attempt, event.frameBuffer)
    })
    recorder.onStop(() => {
      const attempt = active
      if (!attempt || !attempt.nativeInvoked || attempt.nativeDrained) return
      attempt.nativeDrained = true
      attempt.stopping = true
      if (attempt.cancelled || attempt.error || attempt.final) complete(attempt)
      else end(attempt)
    })
    recorder.onError(() => {
      const attempt = active
      if (!attempt || !attempt.nativeInvoked) return
      attempt.nativeDrained = true
      if (!attempt.cancelled) attempt.error = { retcode: 4007, msg: 'recording failed' }
      complete(attempt)
    })
    recorder.onInterruptionBegin?.(() => { if (active) fail(active) })
    return recorder
  }
  const connect = async (attempt: Attempt, duration: number) => {
    try {
      const session = await deps.getSession(attempt.abort.signal)
      if (!current(attempt) || attempt.cancelled) return
      // Prevent malformed API replies from uploading audio to another host.
      if (!/^wss:\/\/asr\.cloud\.tencent\.com\/asr\/v2\/[0-9]+\?/.test(session.url)
        || !/^[0-9a-f]{32}$/.test(session.voiceId) || !(Date.parse(session.expiresAt) > Date.now())) { fail(attempt, 4002); return }
      attempt.voiceId = session.voiceId
      const socket = await deps.connect(session.url)
      if (!current(attempt) || attempt.cancelled) {
        const closeLate = () => { try { consumeRejection(socket.close({ code: 1000, reason: 'cancelled', fail: () => {} })) } catch {} }
        closeLate(); socket.onOpen(closeLate)
        return
      }
      attempt.socket = socket
      socket.onOpen(() => { if (!current(attempt) || attempt.cancelled || attempt.error) close(attempt) })
      socket.onError(() => { if (current(attempt)) fail(attempt) })
      socket.onClose(() => { if (current(attempt) && !attempt.cancelled && !attempt.error && !attempt.final) fail(attempt) })
      socket.onMessage(event => {
        if (!current(attempt) || attempt.cancelled || attempt.error) return
        let message: { code?: number; voice_id?: string; final?: number; result?: { index?: number; slice_type?: number; voice_text_str?: string } }
        try { message = JSON.parse(typeof event.data === 'string' ? event.data : '') } catch { fail(attempt, 4001); return }
        if (!message || typeof message !== 'object' || typeof message.code !== 'number') { fail(attempt, 4001); return }
        if (message.code !== 0) { fail(attempt, message.code); return }
        if (message.voice_id !== attempt.voiceId) { fail(attempt, 4002); return }
        if (!attempt.handshake) {
          attempt.handshake = true
          if (attempt.stopping) { complete(attempt); return }
          try {
            const native = ensureRecorder()
            attempt.nativeInvoked = true
            native.start({ duration, sampleRate: 16000, numberOfChannels: 1, format: 'PCM', frameSize: 4 })
          } catch { attempt.nativeDrained = true; fail(attempt, 4007); return }
        }
        const result = message.result
        if (result && Number.isInteger(result.index) && result.index! >= 0 && result.index! < 2000
          && typeof result.voice_text_str === 'string' && [0, 1, 2].includes(result.slice_type!)) {
          const existing = attempt.parts.get(result.index!)
          if (!existing?.stable) attempt.parts.set(result.index!, { text: result.voice_text_str.slice(0, 10_000), stable: result.slice_type === 2 })
          attempt.sinks.onRecognize?.({ result: transcript(attempt) })
        }
        if (message.final === 1) { attempt.final = true; stopNative(attempt); complete(attempt) }
      })
    } catch (error) {
      if (current(attempt) && !attempt.cancelled) {
        const code = error instanceof ApiError
          ? error.status === 401 ? -40002 : error.status === 429 ? 4006 : error.status >= 500 ? 4005 : 4009
          : 4009
        fail(attempt, code)
      }
    }
  }
  const manager: RecognitionManager = {
    start(options) {
      if (active) { manager.onError?.({ retcode: -30011 }); return }
      const attempt: Attempt = {
        abort: new AbortController(), nativeInvoked: false, nativeDrained: false, stopping: false,
        endSent: false, cancelled: false, terminal: false, final: false, handshake: false,
        voiceId: '', parts: new Map(), queuedBytes: 0, sendQueue: Promise.resolve(),
        sinks: { onStart: manager.onStart, onStop: manager.onStop, onRecognize: manager.onRecognize, onError: manager.onError },
      }
      active = attempt
      attempt.timeout = setTimeout(() => fail(attempt), Math.min(options.duration, 60_000) + 25_000)
      void connect(attempt, Math.max(1, Math.min(options.duration, 60_000)))
    },
    stop() {
      const attempt = active
      if (!attempt || attempt.terminal || attempt.stopping) return
      attempt.stopping = true
      if (!attempt.nativeInvoked) { attempt.abort.abort(); complete(attempt) }
      else stopNative(attempt)
    },
    cancel() {
      const attempt = active
      if (!attempt || attempt.terminal || attempt.cancelled) return
      attempt.cancelled = true; attempt.abort.abort(); close(attempt); stopNative(attempt); complete(attempt)
    },
  }
  return manager
}
let singleton: RecognitionManager | undefined
export function getTencentRecognitionManager(): RecognitionManager {
  if (!singleton) singleton = createTencentVoiceManager({
    getSession: signal => voiceApi.getSession(signal),
    getRecorder: () => Taro.getRecorderManager() as unknown as VoiceRecorder,
    connect: async url => await Taro.connectSocket({ url, tcpNoDelay: true }) as unknown as VoiceSocket,
  })
  return singleton
}
