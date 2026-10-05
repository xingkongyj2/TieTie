import assert from 'node:assert/strict'
import test from 'node:test'
import { SSEParser, Utf8Decoder, type SSEEvent } from '../src/lib/sse'
import { createAuthenticatedStream, type ChunkRequestOptions } from '../src/api/stream'

const bytes = (text: string): ArrayBuffer => Uint8Array.from(Buffer.from(text)).buffer
test('UTF-8 decoder keeps Chinese and emoji intact across every byte boundary', () => {
  const decoder = new Utf8Decoder(); let decoded = ''
  for (const byte of Buffer.from('你好，贴贴🌤️')) decoded += decoder.decode(new Uint8Array([byte]))
  assert.equal(decoded, '你好，贴贴🌤️')
})
test('SSE handles split CRLF, comments, BOM, multiline data, and complete-frame cursors', () => {
  const events: SSEEvent[] = []; const parser = new SSEParser(event => events.push(event), 'evt_old')
  const wire = '\ufeff: ping\r\nid: evt_1\r\ndata: 你好\r\ndata: 🌤️\r\n\r\n'
  for (const byte of Buffer.from(wire)) parser.push(new Uint8Array([byte]))
  assert.deepEqual(events, [{ data: '你好\n🌤️', id: 'evt_1' }])
  parser.push(bytes('id: evt_incomplete\ndata: partial'))
  assert.equal(parser.cursor, 'evt_1')
  assert.equal(events.length, 1)
})
test('SSE null IDs are ignored and explicit blank IDs reset reconnect cursor', () => {
  const parser = new SSEParser(() => {}, 'evt_start')
  parser.push(bytes('id: invalid\0id\ndata: one\n\n')); assert.equal(parser.cursor, 'evt_start')
  parser.push(bytes('id:\ndata: two\n\n')); assert.equal(parser.cursor, null)
})
test('SSE bounds complete multiline frames, including many short lines', () => {
  const parser = new SSEParser(() => {}, null, 40)
  assert.throws(() => parser.push(bytes('data: 12345\ndata: 12345\ndata: 12345\ndata: 12345\n\n')), /过大/)
})
function streamFixture(transformData?: (data: string) => Promise<string>) {
  const requests: ChunkRequestOptions[] = []
  const handlers: { headers?: (value: { header: Record<string, unknown> }) => void; chunk?: (value: { data: ArrayBuffer }) => void; aborts: number }[] = []
  const invalid: string[] = []
  let visible = true
  let listener: ((value: boolean) => void) | undefined
  const stream = createAuthenticatedStream({ url: path => `https://api.example${path}`, getToken: () => 'jwt', invalidateToken: token => invalid.push(token), retryMs: 5,
    transformData,
    isVisible: () => visible, onVisibilityChange: callback => { listener = callback; return () => { listener = undefined } },
    request: options => {
      requests.push(options); const h: typeof handlers[number] = { aborts: 0 }; handlers.push(h)
      return { abort: () => { h.aborts++ }, onHeadersReceived: callback => { h.headers = callback }, onChunkReceived: callback => { h.chunk = callback } }
    },
  }, '/api/qoder/sessions/space/stream', 'evt_old')
  return { stream, requests, handlers, invalid, visibility: (value: boolean) => { visible = value; listener?.(value) } }
}
test('stream authenticates, resumes from last complete frame, and pauses while hidden', async () => {
  const f = streamFixture(); const events: string[] = []; f.stream.onmessage = event => events.push(event.data)
  await Promise.resolve()
  assert.equal(f.requests[0].header.Authorization, 'Bearer jwt')
  assert.equal(f.requests[0].header['Last-Event-ID'], 'evt_old')
  f.handlers[0].headers?.({ header: { 'Content-Type': 'text/event-stream' } })
  f.handlers[0].chunk?.({ data: bytes('id: evt_new\ndata: 你好\n\nid: incomplete\ndata: half') })
  assert.deepEqual(events, ['你好'])
  f.visibility(false); assert.equal(f.handlers[0].aborts, 1)
  f.requests[0].fail({ errMsg: 'abort' }); assert.equal(f.requests.length, 1)
  f.visibility(true); assert.equal(f.requests.length, 2)
  assert.equal(f.requests[1].header['Last-Event-ID'], 'evt_new')
  f.stream.close(); f.visibility(true); assert.equal(f.requests.length, 2)
})
test('stream 401 decodes chunked JSON and distinguishes upstream credentials', async () => {
  for (const code of ['authentication_failed', 'token_expired']) {
    const f = streamFixture(); await Promise.resolve()
    const payload = bytes(JSON.stringify({ error: { code } }))
    f.handlers[0].headers?.({ header: { 'Content-Type': 'application/json' } })
    f.handlers[0].chunk?.({ data: payload })
    f.requests[0].success({ statusCode: 401, data: new ArrayBuffer(0) })
    assert.deepEqual(f.invalid, code === 'authentication_failed' ? [] : ['jwt'])
    f.visibility(false); f.visibility(true); assert.equal(f.requests.length, 1)
    f.stream.close()
  }
})
test('stream reconnects network failures with the committed cursor, then stops retrying when closed', async () => {
  const f = streamFixture()
  await Promise.resolve()
  f.handlers[0].headers?.({ header: { 'Content-Type': 'text/event-stream' } })
  f.handlers[0].chunk?.({ data: bytes('id: evt_completed\ndata: complete\n\nid: evt_half\ndata: partial') })
  f.requests[0].fail({ errMsg: 'connection reset' })
  await new Promise(resolve => setTimeout(resolve, 20))
  assert.equal(f.requests.length, 2)
  assert.equal(f.requests[1].header['Last-Event-ID'], 'evt_completed')
  f.stream.close(); f.requests[1].fail({ errMsg: 'abort' })
  await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(f.requests.length, 2)
})
test('unsupported chunk APIs stop the stream and leave the polling fallback active', async () => {
  let requests = 0; let aborted = 0; let errors = 0
  const stream = createAuthenticatedStream({ url: value => value, getToken: () => 'jwt', invalidateToken: () => {},
    isVisible: () => true, onVisibilityChange: () => () => {}, retryMs: 1,
    request: () => { requests++; return { abort: () => { aborted++ } } },
  }, '/api/sse', null)
  stream.onerror = () => { errors++ }
  await Promise.resolve(); await new Promise(resolve => setTimeout(resolve, 5))
  assert.equal(requests, 1); assert.equal(aborted, 1); assert.equal(errors, 1)
  stream.close()
})
test('async image transformations preserve event order and commit only delivered cursors', async () => {
  let complete: (() => void) | undefined
  const f = streamFixture(async data => {
    if (data === 'image') await new Promise<void>(resolve => { complete = resolve })
    return `local:${data}`
  })
  const events: string[] = []; f.stream.onmessage = event => events.push(event.data)
  await Promise.resolve()
  f.handlers[0].headers?.({ header: { 'Content-Type': 'text/event-stream' } })
  f.handlers[0].chunk?.({ data: bytes('id: evt_image\ndata: image\n\nid: evt_after\ndata: after\n\n') })
  await Promise.resolve()
  assert.deepEqual(events, [])
  complete!()
  await new Promise(resolve => setTimeout(resolve, 0))
  assert.deepEqual(events, ['local:image', 'local:after'])
  f.visibility(false); f.visibility(true)
  assert.equal(f.requests[1].header['Last-Event-ID'], 'evt_after')
  f.stream.close()
})
test('closing/pausing while image conversion is pending prevents stale rendering and cursor skips', async () => {
  let complete: (() => void) | undefined
  const f = streamFixture(data => new Promise(resolve => { complete = () => resolve(data) }))
  const events: string[] = []; f.stream.onmessage = event => events.push(event.data)
  await Promise.resolve()
  f.handlers[0].headers?.({ header: { 'Content-Type': 'text/event-stream' } })
  f.handlers[0].chunk?.({ data: bytes('id: evt_pending\ndata: image\n\n') })
  await Promise.resolve()
  f.visibility(false)
  complete!(); await new Promise(resolve => setTimeout(resolve, 0))
  f.visibility(true)
  assert.equal(f.requests[1].header['Last-Event-ID'], 'evt_old')
  assert.deepEqual(events, [])
  f.stream.close()
})
