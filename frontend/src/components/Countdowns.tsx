import { CalendarDays, Clock3, CloudMoon, Compass, Flower2, Gem, Leaf, Mountain, Orbit, Repeat2, Sparkles, Star, Sun, Wind } from 'lucide-react'
import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { countdownApi, type Countdown } from '../api/countdowns'
import { cardSeed, recordCardStyles } from '../lib/cardAppearance'
import './Countdowns.css'

const countdownGraphics = [Flower2, Orbit, Sparkles, Leaf, Star, Sun, Compass, Gem, Wind, CloudMoon, Mountain, Clock3]
function countdownAppearance(id: string) {
 const seed = cardSeed(id)
 return { Graphic: countdownGraphics[(seed >>> 16) % countdownGraphics.length] }
}

function CountdownEmpty() {
 const id = `countdown-empty-${useId().replace(/:/g, '')}`
 return <div className="countdown-placeholder" role="status">
  <svg className="countdown-empty-art" viewBox="0 0 112 112" fill="none" aria-hidden="true">
   <defs>
    <linearGradient id={`${id}-glass`} x1="37" y1="28" x2="74" y2="85" gradientUnits="userSpaceOnUse"><stop stopColor="#F5FAFF" /><stop offset="1" stopColor="#E0ECFC" stopOpacity=".65" /></linearGradient>
    <linearGradient id={`${id}-frame`} x1="33" y1="20" x2="81" y2="95" gradientUnits="userSpaceOnUse"><stop stopColor="#91B6E9" /><stop offset="1" stopColor="#638FD0" /></linearGradient>
    <linearGradient id={`${id}-sand`} x1="46" y1="36" x2="64" y2="85" gradientUnits="userSpaceOnUse"><stop stopColor="#F2D7A3" /><stop offset="1" stopColor="#DBB777" /></linearGradient>
   </defs>
   <path d="M88 60C93 69 87 83 79 88" stroke="#CFDDF0" strokeWidth="1.3" strokeLinecap="round" strokeDasharray="2 5" />
   <path d="M25 73L27 78L32 80L27 82L25 87L23 82L18 80L23 78Z" fill="#EED8B2" />
   <path d="M87 25V35M82 30H92" stroke="#A8C4E9" strokeWidth="1.6" strokeLinecap="round" />
   <circle cx="26" cy="35" r="2" fill="#C5D8F1" />
   <g transform="rotate(-8 56 56)">
    <path d="M39 27H73C73 41 66 47 58 54C56 56 56 58 58 60C66 67 73 73 73 88H39C39 73 46 67 54 60C56 58 56 56 54 54C46 47 39 41 39 27Z" fill={`url(#${id}-glass)`} stroke="#9BB9E0" strokeWidth="1.6" />
    <path d="M44 36H68C66 42 61 46 56 50C51 46 46 42 44 36Z" fill={`url(#${id}-sand)`} />
    <path d="M42 86C47 81 50 73 56 73C62 73 65 81 70 86H42Z" fill={`url(#${id}-sand)`} />
    <path d="M56 57V69" stroke="#DEBD84" strokeWidth="1.8" strokeLinecap="round" />
    <path d="M43 30C43 40 47 45 50 48M43 79C44 74 47 69 50 66" stroke="white" strokeWidth="2.4" strokeLinecap="round" />
    <rect x="33" y="20" width="46" height="7" rx="3.5" fill={`url(#${id}-frame)`} />
    <rect x="33" y="88" width="46" height="7" rx="3.5" fill={`url(#${id}-frame)`} />
    <path d="M38 22H72M38 90H72" stroke="#C4D9F5" strokeWidth="1.2" strokeLinecap="round" />
   </g>
  </svg>
  <p>暂无倒计时</p>
 </div>
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
 if (!sessionId || (!loading && !items.length && !error)) return <CountdownEmpty />
 const styles = recordCardStyles(items)
 return <div className="countdowns">
  {error && <div className="cloud-error" role="alert">{error}<button type="button" onClick={() => void reload()}>重试</button></div>}
  {loading && <p className="empty-note" role="status">正在打开倒计时…</p>}
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
