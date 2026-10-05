import Taro from '@tarojs/taro'
import { apiURL } from '../config/api'

let loaded = false
let loading: Promise<void> | undefined
/** WOFF preserves the original glyphs and works on older iOS font loaders. */
export function loadTitleFont(): Promise<void> {
  if (loaded || process.env.TARO_ENV !== 'weapp') return Promise.resolve()
  if (loading) return loading
  let source: string
  try { source = apiURL('/api/assets/reminder-titles.woff') }
  catch { return Promise.resolve() }
  // iOS cannot load HTTP fonts; local development uses the bundled CSS font.
  if (!source.startsWith('https://')) return Promise.resolve()
  loading = Taro.loadFontFace({ global: true, family: 'TieTie Reminder Titles', source: `url("${source}")`, desc: { style: 'normal', weight: 'normal' } })
    .then(() => { loaded = true }).catch(() => {}).finally(() => { loading = undefined })
  return loading
}
