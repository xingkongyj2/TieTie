export interface MentionRange { start: number; end: number; name: string }
const mentionBoundary = (text: string, at: number, end: number) =>
  !/[A-Za-z0-9_.+-]/u.test(text[at - 1] ?? '')
  && (end === text.length || /[\s\p{P}]/u.test(text[end]) && text[end] !== '_' && text[end] !== '.')
export function mentionRanges(text: string, names: string[]): MentionRange[] {
  const known = [...new Set(names.filter(Boolean))].sort((a, b) => b.length - a.length)
  const result: MentionRange[] = []
  for (let at = 0; at < text.length; at++) {
    if (text[at] !== '@') continue
    const name = known.find((value) => text.startsWith(value, at + 1) && mentionBoundary(text, at, at + value.length + 1))
    if (name) { result.push({ start: at, end: at + name.length + 1, name }); at += name.length }
  }
  return result
}
export function completePartnerMention(previous: string, next: string, name: string): { text: string; caret: number } | null {
  if (!name) return null
  let start = 0
  while (start < previous.length && start < next.length && previous[start] === next[start]) start++
  let oldEnd = previous.length; let newEnd = next.length
  while (oldEnd > start && newEnd > start && previous[oldEnd - 1] === next[newEnd - 1]) { oldEnd--; newEnd-- }
  if (next.slice(start, newEnd) !== '@' || /[A-Za-z0-9_.+-]/u.test(next[start - 1] ?? '')) return null
  const token = `@${name} `
  if (next.length - 1 + token.length > 2000) return null
  return { text: next.slice(0, start) + token + next.slice(newEnd), caret: start + token.length }
}
export function expandMentionSelection(text: string, start: number, end: number, names: string[]): [number, number] {
  for (const range of mentionRanges(text, names)) {
    if (start < range.end && end > range.start || start === end && start > range.start && start < range.end) {
      start = Math.min(start, range.start); end = Math.max(end, range.end)
    }
  }
  return [start, end]
}
export function deleteMention(text: string, start: number, end: number, direction: 'backward' | 'forward', names: string[]): { text: string; caret: number } | null {
  const ranges = mentionRanges(text, names)
  if (start !== end) {
    const expanded = expandMentionSelection(text, start, end, names)
    if (expanded[0] === start && expanded[1] === end && !ranges.some((r) => r.start >= start && r.end <= end)) return null
    return { text: text.slice(0, expanded[0]) + text.slice(expanded[1]), caret: expanded[0] }
  }
  const range = ranges.find((r) => direction === 'backward'
    ? start > r.start && (start <= r.end || start === r.end + 1 && text[r.end] === ' ')
    : start >= r.start && start < r.end)
  if (!range) return null
  const finish = start === range.end + 1 ? start : range.end
  return { text: text.slice(0, range.start) + text.slice(finish), caret: range.start }
}
// Covers mouse selection, mobile edits and paste in addition to keyboard keys.
export function atomicMentionEdit(previous: string, next: string, names: string[]): string {
  let start = 0
  while (start < previous.length && start < next.length && previous[start] === next[start]) start++
  let oldEnd = previous.length; let newEnd = next.length
  while (oldEnd > start && newEnd > start && previous[oldEnd - 1] === next[newEnd - 1]) { oldEnd--; newEnd-- }
  const [from, to] = expandMentionSelection(previous, start, oldEnd, names)
  return previous.slice(0, from) + next.slice(start, newEnd) + previous.slice(to)
}
