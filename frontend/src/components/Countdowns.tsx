import { CalendarDays, Clock3, CloudMoon, Compass, Flower2, Gem, Leaf, Mountain, Orbit, Repeat2, Sparkles, Star, Sun, Wind } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { countdownApi, type Countdown } from '../api/countdowns'
import { TabLoading } from './TabLoading'
import { cardSeed, recordCardStyles } from '../lib/cardAppearance'
import './Countdowns.css'

const countdownGraphics = [Flower2, Orbit, Sparkles, Leaf, Star, Sun, Compass, Gem, Wind, CloudMoon, Mountain, Clock3]
function countdownAppearance(id: string) {
 const seed = cardSeed(id)
 return { Graphic: countdownGraphics[(seed >>> 16) % countdownGraphics.length] }
}

export function Countdowns({ sessionId }: { sessionId?: string }) {
 const [items, setItems] = useState<Countdown[]>([])
 const [next, setNext] = useState('')
 const [error, setError] = useState('')
 const [loading, setLoading] = useState(true)
 const pages = useRef(1)
 const generation = useRef(0)
 const mounted = useRef(true)
 const reload = useCallback(async () => {
  if (!sessionId) return
  const seq = ++generation.current
  try {
   const rows: Countdown[] = []; let cursor = ''
   for (let i = 0; i < pages.current; i++) { const page = await countdownApi.list(sessionId, cursor); rows.push(...page.items); cursor = page.nextCursor; if (!cursor) break }
   if (mounted.current && seq === generation.current) { setItems(rows); setNext(cursor); setError('') }
  } catch (e) { if (mounted.current && seq === generation.current) setError(e instanceof Error ? e.message : '倒计时暂时没加载出来。') }
  finally { if (mounted.current && seq === generation.current) setLoading(false) }
 }, [sessionId])
 useEffect(() => { mounted.current = true; pages.current = 1; setItems([]); setLoading(true); void reload(); const timer = setInterval(() => { void reload() }, 30_000); const focus = () => { void reload() }; window.addEventListener('focus', focus); return () => { mounted.current = false; generation.current++; clearInterval(timer); window.removeEventListener('focus', focus) } }, [reload])
 if (!sessionId) return null
 if (loading) return <TabLoading />
 if (!items.length && !error) return <TabLoading empty text="暂无倒计时" />
 const styles = recordCardStyles(items)
 return <div className="countdowns">
  {error && <div className="cloud-error" role="alert">{error}<button type="button" onClick={() => void reload()}>重试</button></div>}
  {items.map((item) => {
   const { Graphic } = countdownAppearance(item.id)
   return <article className="countdown-card" key={item.id} aria-label={item.title} style={styles.get(item.id)}>
    <Graphic className="countdown-art" size={148} strokeWidth={.8} aria-hidden="true" />
    <div className="countdown-card-top"><span className="countdown-icon"><Graphic size={21} strokeWidth={1.5} aria-hidden="true" /></span><span className="countdown-repeat">{item.repeat === 'annual' ? <><Repeat2 size={11} />每年重复</> : '提醒一次'}</span></div>
    <h2>{item.title}</h2><p className="countdown-count-label">{item.expired ? '这个日期已经过去' : item.daysRemaining === 0 ? '就是今天' : '距离这一天还有'}</p>
    <div className="countdown-number"><strong>{item.expired ? Math.abs(item.daysRemaining) : item.daysRemaining}</strong><span>{item.expired ? '天前' : '天'}</span></div>
    <div className="countdown-card-bottom"><time dateTime={item.nextDate}><CalendarDays size={13} />{item.nextDate.replaceAll('-', '.')}</time></div>
    {item.leapAdjusted && <p className="countdown-leap-note">非闰年的 2 月 29 日按 2 月 28 日计算</p>}
   </article>
  })}
  {next && <button type="button" className="countdown-more" onClick={() => { pages.current++; void reload() }}>查看更多</button>}
 </div>
}
