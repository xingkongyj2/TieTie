/**
 * Return a stable local-calendar key for comparing dates.
 *
 * Date objects created by native mini-program runtimes can retain a time of
 * day. Comparing their timestamps would then incorrectly mark a calendar day
 * as being in the future, so date picker limits must compare calendar keys.
 */
const pad = (value: number) => String(value).padStart(2, '0')

export const calendarDayKey = (date: Date) =>
  `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`

export const isCalendarDayAfter = (date: Date, reference: Date) =>
  calendarDayKey(date) > calendarDayKey(reference)
