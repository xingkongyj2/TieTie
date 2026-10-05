import type { CSSProperties } from 'react'

const palettes = ['blue', 'cream', 'pink', 'mint', 'lilac', 'peach', 'sky', 'sage'] as const
type Palette = typeof palettes[number]

// Allocate the least-used colors in creation order, so hash collisions cannot
// give a short list the same color. Pinning and display order do not affect it.
export function recordCardStyles(records: { id: string; createdAt: string }[], reserved: Palette[] = []) {
  const usage = new Map<Palette, number>(palettes.map(palette => [palette, 0]))
  for (const palette of reserved) usage.set(palette, usage.get(palette)! + 1)
  const styles = new Map<string, CSSProperties>()
  let previous: Palette | undefined
  const ordered = [...new Map(records.map(record => [record.id, record])).values()]
    .sort((a, b) => (Date.parse(a.createdAt) || 0) - (Date.parse(b.createdAt) || 0) || a.id.localeCompare(b.id))
  for (const record of ordered) {
    const minimum = Math.min(...usage.values())
    const start = cardSeed(record.id) % palettes.length
    const available = Array.from({ length: palettes.length }, (_, offset) => palettes[(start + offset) % palettes.length])
      .filter(candidate => usage.get(candidate) === minimum)
    const palette = available.find(candidate => candidate !== previous && !reserved.includes(candidate))
      ?? available.find(candidate => candidate !== previous) ?? available[0]
    usage.set(palette, minimum + 1)
    styles.set(record.id, recordCardStyle(record.id, palette))
    previous = palette
  }
  return styles
}

export function cardSeed(id: string) {
  let seed = 2166136261
  for (const character of id) seed = Math.imul(seed ^ character.codePointAt(0)!, 16777619) >>> 0
  return seed
}

// Shared by reminder stats, countdowns and anniversaries. A record's appearance
// stays the same when it is edited, pinned, reordered or shown on the home page.
export function recordCardStyle(id: string, preferredPalette?: Palette): CSSProperties {
  const seed = cardSeed(id)
  const palette = preferredPalette ?? palettes[seed % palettes.length]
  return {
    '--date-card-color': `var(--card-${palette}-color)`,
    '--date-card-background': `linear-gradient(${135 + seed % 21}deg, var(--card-${palette}-start), var(--card-${palette}-end))`,
    '--date-card-art-rotation': `${seed % 23 - 11}deg`,
  } as CSSProperties
}
