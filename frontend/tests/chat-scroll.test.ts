import assert from 'node:assert/strict'
import test from 'node:test'
import { ChatScrollPolicy, chatMessageAnchor, chatTargetScrollTop } from '../src/lib/chatScroll'
import type { Message } from '../src/types'

const row = (id: string, sender: Message['sender'], patch: Partial<Message> = {}): Message => ({
  id, sender, text: sender === 'ai' ? '长回复第一段\n\n'.repeat(150) : '你好', time: '09:34', ...patch,
})

test('first welcome and completed replies open at the beginning, once, even after reading older messages', () => {
  const policy = new ChatScrollPolicy()
  policy.observe('space', [], true, true)
  assert.equal(policy.pending, null)
  const welcome = row('welcome', 'ai')
  policy.observe('space', [welcome], true, false)
  assert.deepEqual(policy.pending, { kind: 'reply', key: 'welcome' })
  policy.complete(policy.pending!)
  const sent = row('local_send', 'self')
  policy.observe('space', [welcome, sent], true, true)
  assert.deepEqual(policy.pending, { kind: 'bottom' })
  policy.complete(policy.pending!)
  // Repeated history reconciliations during generation never issue a scroll.
  for (let i = 0; i < 30; i++) {
    policy.observe('space', [{ ...welcome }, { ...sent }], true, false)
    assert.equal(policy.pending, null)
  }
  const reply = row('reply', 'ai')
  policy.observe('space', [welcome, sent, reply], true, false)
  const target = policy.pending!
  assert.deepEqual(target, { kind: 'reply', key: 'reply' })
  // Mounting/touching can defer the request; polling must not replace it.
  policy.cancelBottom()
  policy.observe('space', [welcome, sent, { ...reply }], true, false)
  assert.equal(policy.pending, target)
  policy.complete(target)
  for (let i = 0; i < 30; i++) {
    policy.observe('space', [welcome, sent, { ...reply, text: reply.text + i }], true, true)
    assert.equal(policy.pending, null)
  }
})

test('streaming does not follow the bottom; finalization anchors the same row', () => {
  const policy = new ChatScrollPolicy()
  const sent = row('sent', 'self')
  policy.observe('space', [sent], true, true)
  policy.complete(policy.pending!)
  for (let i = 0; i < 10; i++) {
    policy.observe('space', [sent, row('reply', 'ai', { streaming: true, text: '生成'.repeat(i) })], true, true)
    assert.equal(policy.pending, null)
  }
  policy.observe('space', [sent, row('reply', 'ai')], true, false)
  assert.deepEqual(policy.pending, { kind: 'reply', key: 'reply' })
})

test('cached previews, old backfilled rows and local-send echoes do not trigger extra scrolls', () => {
  const policy = new ChatScrollPolicy()
  const latest = row('latest', 'ai')
  policy.observe('space', [latest], false, true)
  assert.equal(policy.pending, null)
  policy.observe('space', [row('older', 'ai'), latest], true, true)
  assert.deepEqual(policy.pending, { kind: 'reply', key: 'latest' })
  policy.complete(policy.pending!)
  const send = row('local_send', 'self')
  policy.observe('space', [latest, send], true, true)
  policy.complete(policy.pending!)
  policy.observe('space', [row('backfilled', 'ai'), latest, row('cloud_echo', 'self', { renderKey: send.id })], true, false)
  assert.equal(policy.pending, null)
  assert.equal(chatMessageAnchor(send), chatMessageAnchor(row('cloud_echo', 'self', { renderKey: send.id })))
})

test('manual gestures cancel bottom requests, and switching spaces discards stale reply targets', () => {
  const policy = new ChatScrollPolicy()
  policy.observe('space-a', [row('reply-a', 'ai')], true, true)
  const oldTarget = policy.pending!
  policy.observe('space-b', [], false, true)
  assert.equal(policy.pending, null)
  policy.observe('space-b', [row('reply-b', 'ai')], true, true)
  policy.complete(oldTarget)
  assert.deepEqual(policy.pending, { kind: 'reply', key: 'reply-b' })
  policy.requestBottom()
  policy.cancelBottom()
  assert.equal(policy.pending, null)
})

test('long replies use their start coordinate rather than the end; short replies stay within content bounds', () => {
  const viewport = { top: 150, height: 500 }
  // User is currently at scrollTop=300; the new reply starts at content y=1000.
  const reply = { top: 850 }
  const bottom = { top: 2650, height: 1 }
  assert.equal(chatTargetScrollTop('reply', 300, viewport, reply, bottom), 988)
  assert.equal(chatTargetScrollTop('bottom', 300, viewport, reply, bottom), 2301)
  // Scrolling up before completion changes viewport coordinates, not the target.
  assert.equal(chatTargetScrollTop('reply', 0, viewport, { top: 1150 }, { top: 2950, height: 1 }), 988)
  assert.equal(chatTargetScrollTop('reply', 0, viewport, { top: 450 }, { top: 550, height: 1 }), 0)
  assert.equal(chatTargetScrollTop('reply', 300, viewport, reply, { top: 1200, height: 1 }), 851)
})
