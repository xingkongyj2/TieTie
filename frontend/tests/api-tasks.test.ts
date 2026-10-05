import assert from 'node:assert/strict'
import test from 'node:test'
import { consumeTaskRejection } from '../src/api/task'
import { AbortController } from '../src/lib/abort'
import { ApiError, createTransport } from '../src/api/transport'
import { createAuthenticatedStream, type ChunkRequestOptions } from '../src/api/stream'

const flushRejections = () => new Promise(resolve => setTimeout(resolve, 0))
test('callback bridge retains the same PromiseTask and its native methods', async () => {
  let reject!: (error: Error) => void
  const abort = () => reject(new Error('The user aborted a request.'))
  const task = Object.assign(new Promise((_resolve, rejected) => { reject = rejected }), { abort, onChunkReceived: () => {}, onHeadersReceived: () => {} })
  const consumed = consumeTaskRejection(task)
  assert.equal(consumed, task)
  assert.equal(consumed.abort, abort)
  consumed.abort()
  // node:test fails this case if the PromiseTask rejection is unhandled.
  await flushRejections()
})
test('cancelled transport consumes duplicate task rejection and keeps its AbortError', async () => {
  let requests = 0; let aborts = 0
  const request = createTransport({ url: path => path, getToken: () => null, invalidateToken: () => {},
    nativeRequest: options => {
      requests++
      let reject!: (error: Error) => void
      const task = Object.assign(new Promise((_resolve, rejected) => { reject = rejected }), {
        abort: () => { aborts++; options.fail({ errMsg: 'request:fail abort' }); reject(new Error('The user aborted a request.')) },
      })
      return consumeTaskRejection(task)
    },
  })
  const controller = new AbortController()
  const result = request('/api/test', { signal: controller.signal })
  controller.abort()
  await assert.rejects(result, { name: 'AbortError' })
  await flushRejections()
  assert.equal(requests, 1); assert.equal(aborts, 1)
})
test('callback network errors keep the business error while the task promise is consumed', async () => {
  let fail!: () => void
  const request = createTransport({ url: path => path, getToken: () => null, invalidateToken: () => {},
    nativeRequest: options => {
      let reject!: (error: Error) => void
      const task = Object.assign(new Promise((_resolve, rejected) => { reject = rejected }), { abort: () => {} })
      fail = () => { options.fail({ errMsg: 'network error' }); reject(new Error('network error')) }
      return consumeTaskRejection(task)
    },
  })
  const result = request('/api/test')
  fail()
  await assert.rejects(result, error => error instanceof ApiError && error.code === 'NETWORK_ERROR')
  await flushRejections()
})
test('closing a chunk stream aborts one original PromiseTask without an unhandled rejection', async () => {
  let requests = 0; let aborts = 0
  let headers: ((response: { header: Record<string, unknown> }) => void) | undefined
  let chunks: ((response: { data: ArrayBuffer }) => void) | undefined
  const events: string[] = []
  const stream = createAuthenticatedStream({ url: path => path, getToken: () => 'jwt', invalidateToken: () => {}, isVisible: () => true, onVisibilityChange: () => () => {},
    request: (options: ChunkRequestOptions) => {
      requests++
      let reject!: (error: Error) => void
      return consumeTaskRejection(Object.assign(new Promise((_resolve, rejected) => { reject = rejected }), {
        abort: () => { aborts++; options.fail({ errMsg: 'request:fail abort' }); reject(new Error('The user aborted a request.')) },
        onHeadersReceived: (callback: typeof headers) => { headers = callback },
        onChunkReceived: (callback: typeof chunks) => { chunks = callback },
      }))
    },
  }, '/api/stream', null)
  stream.onmessage = event => events.push(event.data)
  await Promise.resolve()
  headers!({ header: { 'Content-Type': 'text/event-stream' } })
  chunks!({ data: Uint8Array.from(Buffer.from('id: evt_1\ndata: hello\n\n')).buffer })
  assert.deepEqual(events, ['hello'])
  stream.close()
  await flushRejections()
  assert.equal(requests, 1); assert.equal(aborts, 1)
})
