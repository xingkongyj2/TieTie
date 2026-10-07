import assert from 'node:assert/strict'
import test from 'node:test'
import { ReplyPresentation, replyStatusText, type ReplyFeedback } from '../src/lib/replyPresentation'
import { matchesMountedPrefix, mountedMessageKey } from '../src/lib/chatMounting'
import { ChatScrollPolicy } from '../src/lib/chatScroll'
import type { Message } from '../src/types'

const user: Message = { id: 'user-1', sender: 'self', source: 'chat', text: '查天气', time: '09:41', createdAt: '2026-10-07T01:41:00Z' }
const answer: Message = { id: 'cloud-1', sender: 'ai', source: 'chat', text: '北戴河今天晴，20℃。', time: '09:41', createdAt: '2026-10-07T01:41:10Z' }
const pending = { startedAt: Date.parse(user.createdAt!), silent: false, visibility: 'shared' as const }
const base = { owner: '20:session', messages: [user], loaded: true, busy: true, silent: false, turnError: '', pending, feedback: { phase: 'sending' } as ReplyFeedback | null }

test('thinking/replying never rewind or remove the mounted row during tool idle, polling or reconnect', () => {
  const presentation = new ReplyPresentation()
  const first = presentation.present(base)
  assert.equal(first.length, 2)
  const slot = first[1]
  assert.equal(replyStatusText(slot.replyStatus!, '贴贴'), '贴贴正在思考中')
  for (const phase of ['waiting', 'thinking', 'syncing', 'delayed'] as const) {
    assert.strictEqual(presentation.present({ ...base, feedback: { phase } }), first)
  }
  const replying = presentation.present({ ...base, feedback: { phase: 'replying' } })
  assert.equal(replyStatusText(replying[1].replyStatus!, '贴贴'), '贴贴正在回复中')
  assert.equal(replying[1].renderKey, slot.renderKey)
  for (const phase of ['syncing', 'thinking', 'waiting', 'delayed', 'replying'] as const) {
    assert.strictEqual(presentation.present({ ...base, busy: false, feedback: { phase } }), replying)
  }
  assert.strictEqual(presentation.present({ ...base, busy: false, feedback: null }), replying)
})

test('final reply replaces the same row and scrolls once to its beginning, including refreshed history', () => {
  const presentation = new ReplyPresentation()
  const scroll = new ChatScrollPolicy()
  const first = presentation.present(base)
  const mountedKeys = first.map(mountedMessageKey)
  scroll.observe(base.owner, first, true, true)
  scroll.complete(scroll.pending!)
  const ready = { ...base, pending: undefined, messages: [user, answer], feedback: { phase: 'complete' } as ReplyFeedback }
  const final = presentation.present(ready)
  assert.equal(final.length, 2)
  assert.equal(final[1].replyStatus, undefined)
  assert.equal(final[1].id, answer.id)
  assert.equal(final[1].renderKey, first[1].renderKey)
  assert.ok(matchesMountedPrefix(mountedKeys, final), 'native batch must keep its already mounted row')
  scroll.observe(base.owner, final, true, false)
  assert.deepEqual(scroll.pending, { kind: 'reply', key: first[1].renderKey })
  scroll.complete(scroll.pending!)
  for (const busy of [true, false, true]) {
    const refreshed = presentation.present({ ...ready, busy, feedback: null, messages: [user, { ...answer }] })
    assert.equal(refreshed.length, 2, 'late running status must not recreate a waiting module')
    assert.equal(refreshed[1].renderKey, first[1].renderKey)
    scroll.observe(base.owner, refreshed, true, false)
    assert.equal(scroll.pending, null)
  }
})

test('first welcome survives idle until its actual reply and keeps the welcome label', () => {
  const presentation = new ReplyPresentation()
  const waiting = { ...base, messages: [], pending: undefined, feedback: null }
  const first = presentation.present(waiting)
  assert.equal(first.length, 1)
  assert.equal(replyStatusText(first[0].replyStatus!, '贴贴'), '贴贴正在为你们准备欢迎语')
  assert.strictEqual(presentation.present({ ...waiting, busy: false }), first)
  const welcome = { ...answer, createdAt: new Date().toISOString() }
  const final = presentation.present({ ...waiting, messages: [welcome], busy: false })
  assert.equal(final.length, 1)
  assert.equal(final[0].renderKey, first[0].renderKey)
  assert.equal(final[0].replyStatus, undefined)
})

test('reply across midnight preserves the mounted day group without changing authoritative event time', () => {
  const presentation = new ReplyPresentation()
  const nightUser = { ...user, createdAt: '2026-10-07T15:59:58Z' }
  const night = { ...base, messages: [nightUser], pending: { ...pending, startedAt: Date.parse(nightUser.createdAt) } }
  const waiting = presentation.present(night)
  const afterMidnight = { ...answer, createdAt: '2026-10-07T16:00:04Z' }
  const final = presentation.present({ ...night, messages: [nightUser, afterMidnight], pending: undefined, feedback: { phase: 'complete' } })
  assert.ok(matchesMountedPrefix(waiting.map(mountedMessageKey), final))
  assert.equal(afterMidnight.createdAt, '2026-10-07T16:00:04Z')
})

test('unrelated reminders, private replies and old backfilled messages do not consume the chat placeholder', () => {
  const presentation = new ReplyPresentation()
  const first = presentation.present(base)
  const messages: Message[] = [
    { ...answer, id: 'old', createdAt: '2026-10-06T01:41:00Z' }, user,
    { ...answer, id: 'reminder', source: 'reminder' },
    { ...answer, id: 'private', visibility: 'private' },
  ]
  const rows = presentation.present({ ...base, messages })
  assert.strictEqual(rows.at(-1), first[1])
  const final = presentation.present({ ...base, messages: [...messages, answer], pending: undefined, feedback: { phase: 'complete' } })
  assert.equal(final.at(-1)!.renderKey, first[1].renderKey)
  assert.equal(final.filter(row => row.replyStatus).length, 0)
})

test('stopping failure resumes the previous reply stage; terminal feedback and account changes stay scoped', () => {
  const presentation = new ReplyPresentation()
  const first = presentation.present({ ...base, feedback: { phase: 'replying' } })
  assert.equal(presentation.present({ ...base, feedback: { phase: 'stopping' } }).at(-1)!.replyStatus!.phase, 'stopping')
  const resumed = presentation.present({ ...base, feedback: { phase: 'waiting' } })
  assert.equal(resumed.at(-1)!.replyStatus!.phase, 'replying')
  assert.equal(resumed.at(-1)!.renderKey, first.at(-1)!.renderKey)
  const failed = { ...base, busy: false, pending: undefined, feedback: { phase: 'error', message: '请重试' } as ReplyFeedback }
  const error = presentation.present(failed)
  assert.equal(error.at(-1)!.replyStatus!.phase, 'error')
  assert.strictEqual(presentation.present({ ...failed, busy: true }), error)
  assert.strictEqual(presentation.present({ ...failed, feedback: null }), error)
  assert.deepEqual(presentation.present({ ...base, owner: '21:other', messages: [], pending: undefined, busy: false, feedback: null }), [])
})

test('silent sends never show an AI row; a new user turn gets a new stable row', () => {
  const presentation = new ReplyPresentation()
  assert.deepEqual(presentation.present({ ...base, pending: { ...pending, silent: true }, silent: true }), [user])
  const first = presentation.present(base)
  presentation.present({ ...base, pending: undefined, messages: [user, answer], feedback: { phase: 'complete' } })
  const user2 = { ...user, id: 'user-2', createdAt: '2026-10-07T01:42:00Z' }
  const next = presentation.present({ ...base, messages: [user, answer, user2], pending: { ...pending, startedAt: Date.parse(user2.createdAt) } })
  assert.equal(next.length, 4)
  assert.notEqual(next.at(-1)!.renderKey, first.at(-1)!.renderKey)
  assert.equal(next[1].renderKey, first[1].renderKey)
})
