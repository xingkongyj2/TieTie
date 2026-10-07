const backgrounds = [
  'https://mayjimages.s3.bitiful.net/tietie/login-paper-blue-v1.jpg',
  'https://mayjimages.s3.bitiful.net/tietie/login-blue-collage-v2.png',
] as const

let background: string | undefined
let hidden = false
const subscribers = new Set<() => void>()
const chooseBackground = (): string => backgrounds[Math.random() < 0.5 ? 0 : 1]

/** Keep one choice throughout a foreground visit, including login form changes. */
export function getLoginBackground(): string {
  background ??= chooseBackground()
  return background
}

export function subscribeLoginBackground(callback: () => void): () => void {
  subscribers.add(callback)
  return () => { subscribers.delete(callback) }
}

export function showLoginBackground(): void {
  if (background !== undefined && !hidden) return
  background = chooseBackground()
  hidden = false
  subscribers.forEach(callback => callback())
}

export function hideLoginBackground(): void {
  hidden = true
}
