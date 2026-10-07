import assert from 'node:assert/strict'
import test from 'node:test'
import { register } from 'node:module'
import { createElement } from 'react'
import { ReplyPresentation, type ReplyFeedback } from '../src/lib/replyPresentation'
import type { Member, Message } from '../src/types'

test('actual ChatMessage retains native row, avatar and name when feedback becomes the final reply', async () => {
  process.env.TARO_ENV = 'weapp'
  process.env.SUPPORT_TARO_POLYFILL = 'disabled'
  for (const name of ['INNER_HTML', 'ADJACENT_HTML', 'CLONE_NODE', 'TEMPLATE_CONTENT', 'SIZE_APIS', 'CONTAINS', 'MUTATION_OBSERVER']) {
    (globalThis as Record<string, unknown>)[`ENABLE_${name}`] = false
  }
  // Match webpack's WeChat component alias instead of loading H5 web components.
  const nativeComponents = new URL('../node_modules/@tarojs/components/mini/index.js', import.meta.url).href
  register(`data:text/javascript,${encodeURIComponent(`
    export function resolve(specifier, context, nextResolve) {
      if (specifier === '@tarojs/components') return { url: ${JSON.stringify(nativeComponents)}, shortCircuit: true };
      return nextResolve(specifier, context);
    }
    export function load(url, context, nextLoad) {
      if (url.endsWith('.css')) return { format: 'module', source: '', shortCircuit: true };
      return nextLoad(url, context);
    }
  `)}`, import.meta.url)
  const { TaroRootElement } = await import('@tarojs/runtime')
  const { createRoot, flushSync } = await import('@tarojs/react')
  const { ChatMessage } = await import('../src/components/ChatMessage')
  const calls: Record<string, unknown>[] = []
  const nativeRoot = new TaroRootElement()
  nativeRoot.ctx = { setData(data: Record<string, unknown>, callback: () => void) { calls.push(data); callback() } }
  const renderer = createRoot(nativeRoot)
  const user: Message = { id: 'user', sender: 'self', userId: 20, source: 'chat', text: '你好', time: '09:41', createdAt: '2026-10-07T01:41:00Z' }
  const members = [{ id: 'ai', name: '贴贴' }, { id: 'self', userId: 20, name: '微信用户' }] as Member[]
  const presentation = new ReplyPresentation()
  const pending = { startedAt: Date.parse(user.createdAt!), silent: false, visibility: 'shared' as const }
  const base = { owner: '20:session', messages: [user], pending, loaded: true, busy: true, silent: false, turnError: '', feedback: { phase: 'sending' } as ReplyFeedback | null }
  const render = async (rows: Message[]) => {
    const children = rows.map(message => createElement(ChatMessage, {
      key: message.renderKey ?? message.id, message, members, onError() {}, onOpenImage() {}, async onAnswer() {},
    }))
    flushSync(() => renderer.render(createElement('view', { className: 'messages' }, children)))
    if (nativeRoot.pendingUpdate) await new Promise<void>(resolve => nativeRoot.enqueueUpdateCallback(resolve))
  }
  try {
    const first = presentation.present(base)
    await render(first)
    const list = nativeRoot.childNodes[0]
    const row = list.childNodes[1]
    const avatar = row.childNodes[0]
    const content = row.childNodes[1]
    const meta = content.childNodes[0]
    assert.match(row.textContent, /贴贴.*正在思考中/)
    const initialId = row.id
    calls.length = 0
    for (const phase of ['replying', 'syncing', 'thinking', 'delayed'] as const) {
      await render(presentation.present({ ...base, feedback: { phase } }))
      assert.strictEqual(list.childNodes[1], row)
      assert.strictEqual(row.childNodes[0], avatar)
      assert.strictEqual(content.childNodes[0], meta)
      assert.match(row.textContent, /贴贴正在回复中/)
    }
    const answer: Message = { id: 'cloud', sender: 'ai', source: 'chat', text: '**你好呀**，今天想聊什么？', time: '09:41', createdAt: '2026-10-07T01:41:08Z' }
    await render(presentation.present({ ...base, messages: [user, answer], pending: undefined, feedback: { phase: 'complete' } }))
    assert.strictEqual(list.childNodes[1], row)
    assert.strictEqual(row.childNodes[0], avatar)
    assert.strictEqual(content.childNodes[0], meta)
    assert.equal(row.id, initialId)
    assert.match(row.textContent, /你好呀/)
    assert.doesNotMatch(row.textContent, /正在思考|正在回复/)
    const weather: Message = { ...answer, weatherCards: [{
      mode: 'query', region: { provinceCode: '130000', province: '河北省', cityCode: '130300', city: '秦皇岛市', districtCode: '130304', district: '北戴河区', codeSystem: 'GB/T2260' },
      precision: 'district', recipientIds: [20], recipientNames: ['微信用户'],
      day: { date: '2026-10-07', min: 16, max: 20, code: 0, rainChance: 0, wind: 8, uv: 2 },
      description: '晴', comparison: '', alerts: [], clothing: '', hours: [], reminders: [], moreReminders: 0,
      airAvailable: false, generatedAt: answer.createdAt!, source: 'test',
    }] }
    await render(presentation.present({ ...base, messages: [user, weather], pending: undefined, feedback: null }))
    assert.strictEqual(list.childNodes[1], row)
    assert.strictEqual(row.childNodes[0], avatar)
    assert.strictEqual(content.childNodes[0], meta)
    assert.match(row.textContent, /刚刚查到的天气.*北戴河区/)
    assert.ok(calls.every(update => !Object.keys(update).some(path => path === 'root.cn' || path === 'root.cn.[0].cn')), 'status/final changes must not replace the history list')
  } finally {
    flushSync(() => renderer.unmount())
  }
})
