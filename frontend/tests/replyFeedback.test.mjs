import test from 'node:test'
import assert from 'node:assert/strict'
import { preserveSavedFeedback, savedReminderFeedback } from '../src/lib/replyFeedback.ts'

const reminder = {
  id: 'rem_1', sessionId: 'sess_1', title: '喝温水', dueAt: '2026-10-05T00:00:00Z',
  recipientIds: [1], createdBy: 1, status: 'scheduled',
}

test('verified confirmation shows the actual reminder time in Asia/Shanghai', () => {
  assert.deepEqual(savedReminderFeedback([reminder]), {
    phase: 'saved', message: '已设置：2026年10月5日 08:00 · 喝温水。AI 回复稍后补上。',
  })
})

test('midnight stays 00:00, regardless of browser timezone', () => {
  assert.match(savedReminderFeedback([{ ...reminder, dueAt: '2026-10-04T16:00:00Z' }]).message, /10月5日 00:00/)
})

test('empty or invalid reminder rows cannot produce a saved confirmation', () => {
  assert.equal(savedReminderFeedback([]), null)
  assert.equal(savedReminderFeedback([{ ...reminder, dueAt: 'invalid' }]), null)
  assert.equal(savedReminderFeedback([{ ...reminder, id: '' }]), null)
})

test('save remains visible across progress events and read failures until final reply', () => {
  const saved = savedReminderFeedback([reminder])
  for (const phase of ['waiting', 'thinking', 'replying', 'syncing', 'delayed', 'error']) {
    assert.equal(preserveSavedFeedback(saved, { phase, message: 'cloud progress' }), saved)
  }
  assert.deepEqual(preserveSavedFeedback(saved, { phase: 'complete' }), { phase: 'complete' })
  assert.equal(preserveSavedFeedback(saved, null), null)
})

test('multiple verified reminders retain each actual title and time', () => {
  const saved = savedReminderFeedback([reminder, { ...reminder, id: 'rem_2', title: '散步', dueAt: '2026-10-05T10:30:00Z' }])
  assert.match(saved.message, /^已设置 2 条提醒：/)
  assert.match(saved.message, /08:00 · 喝温水；2026年10月5日 18:30 · 散步/)
})
