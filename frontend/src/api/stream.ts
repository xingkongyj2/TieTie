import { SSEParser, Utf8Decoder } from '../lib/sse'
import { isAccountTokenInvalid } from './transport'

export interface CloudStream {
  onopen: (() => void) | null
  onerror: (() => void) | null
  onmessage: ((event: { data: string }) => void) | null
  close(): void
}
interface StreamResponse { statusCode: number; data: unknown; header?: Record<string, unknown> }
export interface ChunkTask {
  abort(): void
  onHeadersReceived?(callback: (response: { header: Record<string, unknown>; statusCode?: number }) => void): void
  onChunkReceived?(callback: (result: { data: ArrayBuffer }) => void): void
}
export interface ChunkRequestOptions {
  url: string; method: 'GET'; header: Record<string, string>; timeout: number
  enableChunked: true; responseType: 'arraybuffer'; dataType: 'text'
  success(response: StreamResponse): void
  fail(error: { errMsg?: string }): void
}
export interface StreamDependencies {
  url(path: string): string
  getToken(): string | null
  invalidateToken(expected: string): void
  request(options: ChunkRequestOptions): ChunkTask
  isVisible(): boolean
  onVisibilityChange(callback: (visible: boolean) => void): () => void
  retryMs?: number
  transformData?(data: string): Promise<string>
  maxFrameSize?: number
}

export function createAuthenticatedStream(deps: StreamDependencies, path: string, after: string | null): CloudStream {
  let closed = false
  let blocked = false
  let cursor = after
  let epoch = 0
  let task: ChunkTask | undefined
  let retry: ReturnType<typeof setTimeout> | undefined
  let disposeVisibility = () => {}
  const stop = () => {
    epoch++
    clearTimeout(retry); retry = undefined
    const previous = task; task = undefined
    previous?.abort()
  }
  const stream: CloudStream = {
    onopen: null, onerror: null, onmessage: null,
    close() { if (closed) return; closed = true; stop(); disposeVisibility() },
  }
  const connect = () => {
    if (closed || blocked || !deps.isVisible() || task) return
    const token = deps.getToken()
    if (!token) return
    const version = ++epoch
    let finished = false
    let opened = false
    let streamResponse = false
    let rejectedBody = ''
    let delivery = Promise.resolve()
    const errorDecoder = new Utf8Decoder()
    const current = () => !closed && version === epoch && deps.isVisible()
    const parser = new SSEParser(event => {
      if (!deps.transformData) { cursor = event.id; stream.onmessage?.({ data: event.data }); return }
      delivery = delivery.then(async () => {
        if (!current() || finished) return
        const data = await deps.transformData!(event.data)
        if (!current() || finished) return
        cursor = event.id
        stream.onmessage?.({ data })
      }).catch(() => { done() })
    }, cursor, deps.maxFrameSize)
    const done = (reconnect = true) => {
      if (!current() || finished) return
      finished = true
      if (!deps.transformData) cursor = parser.cursor
      stop()
      stream.onerror?.()
      if (reconnect && !blocked && !closed && deps.isVisible()) retry = setTimeout(connect, deps.retryMs ?? 3_000)
    }
    const headers: Record<string, string> = { Accept: 'text/event-stream', Authorization: `Bearer ${token}` }
    if (cursor) headers['Last-Event-ID'] = cursor
    try {
      task = deps.request({ url: deps.url(path), method: 'GET', header: headers, timeout: 120_000,
        enableChunked: true, responseType: 'arraybuffer', dataType: 'text',
        success(response) {
          if (!current() || finished) return
          if (response.statusCode === 401 || response.statusCode === 403) {
            blocked = true
            let payload: unknown = response.data
            if (payload instanceof ArrayBuffer) payload = new Utf8Decoder().decode(payload)
            if (typeof payload !== 'string' || !payload) payload = rejectedBody || payload
            if (typeof payload === 'string') { try { payload = JSON.parse(payload) } catch { payload = null } }
            const code = (payload as { error?: { code?: string } } | null)?.error?.code
            if (isAccountTokenInvalid(response.statusCode, code)) deps.invalidateToken(token)
            done(false)
          } else if (deps.transformData) void delivery.then(() => { done() })
          else done()
        },
        fail() { done() },
      })
      if (!current() || finished) { task.abort(); task = undefined; return }
      if (!task.onChunkReceived || !task.onHeadersReceived) { done(false); return }
      task.onHeadersReceived(response => {
        if (!current() || finished) return
        const contentType = Object.entries(response.header).find(([key]) => key.toLowerCase() === 'content-type')?.[1]
        streamResponse = String(contentType ?? '').toLowerCase().includes('text/event-stream')
        if (streamResponse && (!response.statusCode || response.statusCode >= 200 && response.statusCode < 300)) {
          opened = true; stream.onopen?.()
        }
      })
      task.onChunkReceived(result => {
        if (!current() || finished) return
        if (!streamResponse) {
          rejectedBody = (rejectedBody + errorDecoder.decode(result.data)).slice(0, 16_384)
          return
        }
        if (!opened) { opened = true; stream.onopen?.() }
        try { parser.push(result.data); if (!deps.transformData) cursor = parser.cursor } catch { done() }
      })
    } catch { done(false) }
  }
  disposeVisibility = deps.onVisibilityChange(visible => { if (!visible) stop(); else connect() })
  // Let the caller install its callbacks before the first connection.
  void Promise.resolve().then(connect)
  return stream
}
