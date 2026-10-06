import assert from 'node:assert/strict'
import test from 'node:test'
import { calendarDayKey, isCalendarDayAfter } from '../src/lib/datePicker'

test('date picker compares calendar days instead of timestamps', () => {
  const today = new Date(2026, 9, 7, 0, 0, 0, 0)
  assert.equal(calendarDayKey(today), '2026-10-07')

  // A date on the same calendar day must remain selectable even when its
  // native Date carries a later time of day.
  assert.equal(isCalendarDayAfter(new Date(2026, 9, 7, 23, 59), today), false)
  assert.equal(isCalendarDayAfter(new Date(2026, 9, 6, 23, 59), today), false)
  assert.equal(isCalendarDayAfter(new Date(2026, 9, 8, 0, 1), today), true)
})
