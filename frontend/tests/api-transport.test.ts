import assert from 'node:assert/strict'
import test from 'node:test'
import { AbortController } from '../src/lib/abort'
import { ApiError, createTransport, type NativeRequestOptions } from '../src/api/transport'

function fixture() {
  let options: NativeRequestOptions | undefined
  let aborts = 0
  let requests = 0
  const invalid: string[] = []
  const request = createTransport({ url: path => `https://api.example${path}`, getToken: () => 'account-jwt', invalidateToken: token => invalid.push(token),
    nativeRequest: value => { requests++; options = value; return { abort: () => { aborts++ } } },
  })
  return { request, invalid, get options() { assert.ok(options); return options }, get aborts() { return aborts }, get requests() { return requests } }
}
test('JSON transport preserves JWT, care cursor, method and JSON attachment payload', async () => {
  const f = fixture()
  const body = { text: '提醒我们', visibility: 'private', attachments: [{ kind: 'file', name: '任务.txt', content: '下周见' }] }
  const pending = f.request('/api/qoder/sessions/space/messages', { method: 'POST', body, headers: { 'X-Tietie-Care-After': 'evt_care_1' } })
  assert.equal(f.options.header.Authorization, 'Bearer account-jwt')
  assert.equal(f.options.header['Content-Type'], 'application/json')
  assert.equal(f.options.header['X-Tietie-Care-After'], 'evt_care_1')
  assert.equal(f.options.method, 'POST')
  assert.deepEqual(JSON.parse(f.options.data!), body)
  f.options.success({ statusCode: 200, data: '{"messages":[]}' })
  assert.deepEqual(await pending, { messages: [] })
})
test('caller authorization casing is respected and HTTP 204 resolves without parsing', async () => {
  const f = fixture(); const pending = f.request('/api/a', { method: 'DELETE', headers: { authorization: 'Bearer custom' } })
  assert.equal(f.options.header.Authorization, undefined)
  assert.equal(f.options.header.authorization, 'Bearer custom')
  f.options.success({ statusCode: 204, data: '' }); assert.equal(await pending, undefined)
})
test('upstream 401 does not invalidate account JWT; expired account 401 does', async () => {
  for (const code of ['authentication_failed', 'token_expired']) {
    const f = fixture(); const pending = f.request('/api/a')
    f.options.success({ statusCode: 401, data: { error: { code, message: '验证失败' } } })
    await assert.rejects(pending, error => error instanceof ApiError && error.code === code && error.status === 401)
    assert.deepEqual(f.invalid, code === 'authentication_failed' ? [] : ['account-jwt'])
  }
})
test('a cancelled read aborts native task and ignores late expired-token responses', async () => {
  const f = fixture(); const controller = new AbortController()
  const pending = f.request('/api/a', { signal: controller.signal })
  controller.abort(); assert.equal(f.aborts, 1)
  f.options.success({ statusCode: 401, data: {} })
  await assert.rejects(pending, { name: 'AbortError' }); assert.deepEqual(f.invalid, [])
})
test('pre-cancelled reads never dispatch a native request', async () => {
  const f = fixture(); const controller = new AbortController(); controller.abort()
  await assert.rejects(f.request('/api/a', { signal: controller.signal }), { name: 'AbortError' })
  assert.equal(f.aborts, 0)
})
test('POST timeout is uncertain, aborts the task, and never retries the submission', async () => {
  const f = fixture(); const pending = f.request('/api/qoder/sessions/space/messages', { method: 'POST', body: {}, timeoutMs: 5 })
  await assert.rejects(pending, error => error instanceof ApiError && error.code === 'TIMEOUT' && error.message.includes('可能已送达'))
  assert.equal(f.aborts, 1)
  assert.equal(f.requests, 1)
})
test('network failures preserve account token and tell POST callers to confirm history', async () => {
  const f = fixture(); const pending = f.request('/api/qoder/sessions/space/messages', { method: 'POST' })
  f.options.fail({ errMsg: 'request:fail connection reset' })
  await assert.rejects(pending, error => error instanceof ApiError && error.code === 'NETWORK_ERROR' && error.message.includes('可能已送达'))
  assert.deepEqual(f.invalid, [])
  assert.equal(f.requests, 1)
})

const authFailures = [
  { path: '/api/auth/login', network: '登录连接失败，请检查网络和服务地址后重试。', timeout: '登录请求超时，请检查网络和服务地址后重试。' },
  { path: '/api/auth/wechat', network: '登录连接失败，请检查网络和服务地址后重试。', timeout: '登录请求超时，请检查网络和服务地址后重试。' },
  { path: '/api/auth/register', network: '注册连接失败，请检查网络和服务地址，也可尝试登录确认账号状态。', timeout: '注册请求超时，请检查网络和服务地址，也可尝试登录确认账号状态。' },
]
test('missing WeChat login routes explain a service update without retrying or losing the account token', async () => {
  for (const statusCode of [404, 405]) for (const data of [
    { error: { code: 'unsupported_route', message: '不支持此会话操作。' } },
    'Not Found',
  ]) {
    const f = fixture()
    const pending = f.request('/api/auth/wechat', { method: 'POST', body: { code: 'fresh-code' } })
    assert.equal(f.options.url, 'https://api.example/api/auth/wechat')
    assert.equal(f.options.method, 'POST')
    f.options.success({ statusCode, data })
    await assert.rejects(pending, e => e instanceof ApiError && e.status === statusCode
      && e.message === '当前服务暂不支持微信登录，请联系管理员更新服务。')
    assert.equal(f.requests, 1)
    assert.deepEqual(f.invalid, [])
  }
})
test('WeChat configuration and code errors, and unknown chat routes preserve their specific messages', async () => {
  for (const response of [
    { path: '/api/auth/wechat', statusCode: 503, code: 'wechat_login_unavailable', message: '微信登录尚未配置，请联系管理员。' },
    { path: '/api/auth/wechat', statusCode: 401, code: 'invalid_wechat_code', message: '微信登录凭证已失效，请重新点击登录。' },
    { path: '/api/qoder/sessions/space/messages', statusCode: 404, code: 'unsupported_route', message: '不支持此会话操作。' },
  ]) {
    const f = fixture()
    const pending = f.request(response.path, { method: 'POST' })
    f.options.success({ statusCode: response.statusCode, data: { error: { code: response.code, message: response.message } } })
    await assert.rejects(pending, e => e instanceof ApiError && e.code === response.code && e.message === response.message)
    assert.equal(f.requests, 1)
  }
})
test('subscription result failures ask to save again rather than send a chat message', async () => {
  for (const timeout of [false, true]) {
    const f = fixture()
    const pending = f.request('/api/account/wechat-subscription', { method: 'POST', body: { requestId: 'same-attempt' } })
    f.options.fail({ errMsg: timeout ? 'request:fail timeout' : 'request:fail connection reset' })
    await assert.rejects(pending, e => e instanceof ApiError && e.message === (timeout
      ? '订阅结果保存超时，请点击重试保存。' : '订阅结果保存失败，请检查网络后重试保存。'))
    assert.equal(f.requests, 1)
  }
})
test('login and registration native failures show account-specific network and timeout guidance', async () => {
  for (const auth of authFailures) for (const timeout of [false, true]) {
    const f = fixture(); const pending = f.request(auth.path, { method: 'POST', body: { username: 'test', password: 'test-password' } })
    f.options.fail({ errMsg: timeout ? 'request:fail timeout' : 'request:fail connection reset' })
    await assert.rejects(pending, error => error instanceof ApiError && error.status === 0
      && error.code === (timeout ? 'TIMEOUT' : 'NETWORK_ERROR') && error.message === (timeout ? auth.timeout : auth.network))
    assert.deepEqual(f.invalid, [])
    assert.equal(f.requests, 1)
  }
})
test('login and registration deadline failures abort once without chat delivery guidance or retries', async () => {
  for (const auth of authFailures) {
    const f = fixture(); const pending = f.request(auth.path, { method: 'POST', timeoutMs: 5 })
    await assert.rejects(pending, error => error instanceof ApiError && error.code === 'TIMEOUT' && error.message === auth.timeout)
    assert.equal(f.aborts, 1)
    assert.equal(f.requests, 1)
    f.options.success({ statusCode: 401, data: {} })
    assert.deepEqual(f.invalid, [])
  }
})
test('synchronous login and registration request failures use the same account guidance', async () => {
  for (const auth of authFailures) {
    let requests = 0
    const request = createTransport({ url: path => path, getToken: () => null, invalidateToken: () => {},
      nativeRequest: () => { requests++; throw new Error('request setup failed') },
    })
    await assert.rejects(request(auth.path, { method: 'POST' }), error => error instanceof ApiError
      && error.code === 'NETWORK_ERROR' && error.message === auth.network)
    assert.equal(requests, 1)
  }
})
