import { BriefcaseBusiness, CakeSlice, CalendarDays, Clock3, Flag, Flower2, Gem, Gift, GraduationCap, Hand, House, Leaf, Moon, Mountain, Music, PartyPopper, PawPrint, Pin, RefreshCw, Sparkles, Star, Trophy, UsersRound } from 'lucide-react'
import { useState, type CSSProperties } from 'react'
import type { Anniversary } from '../api/anniversaries'
import type { AnniversaryState } from '../hooks/useAnniversaries'
import { recordCardStyle, recordCardStyles } from '../lib/cardAppearance'
import { TabLoading } from './TabLoading'
import { EmptyTabState } from './EmptyTabState'
import './Anniversaries.css'

const kindLabels: Record<Anniversary['kind'], string> = { together: '在一起', birthday: '生日', wedding: '结婚', first_meet: '初次相遇', other: '纪念日' }
function anniversaryGraphic(item: Anniversary | null) {
  if (!item) return CalendarDays
  const typeGraphics = { birthday: CakeSlice, wedding: Gem, together: UsersRound, first_meet: Hand }
  if (item.kind !== 'other') return typeGraphics[item.kind]
  // Other anniversaries share the existing schema; their title supplies the motif.
  const themes = [
    { match: /生日|birthday/i, icon: CakeSlice },
    { match: /旅行|旅游|出游|露营|登山|trip|travel/i, icon: Mountain },
    { match: /毕业|入学|录取|graduat/i, icon: GraduationCap },
    { match: /入职|工作|升职|创业|job/i, icon: BriefcaseBusiness },
    { match: /搬家|入住|新家|house/i, icon: House },
    { match: /相遇|见面|认识|相识|meet/i, icon: Hand },
    { match: /比赛|获奖|运动|跑步|马拉松/i, icon: Trophy },
    { match: /宠物|猫|狗|领养/i, icon: PawPrint },
    { match: /礼物|送礼|gift/i, icon: Gift },
    { match: /音乐|演唱会|concert|music/i, icon: Music },
    { match: /节日|跨年|庆祝|festival/i, icon: PartyPopper },
  ]
  const theme = themes.find(({ match }) => match.test(item.title))
  if (theme) return theme.icon
  // A stable fallback keeps each record's graphic consistent after reloads.
  const graphics = [Sparkles, Flag, Flower2, Star, Leaf, Moon]
  const hash = Array.from(item.id).reduce((value, char) => (value * 31 + char.codePointAt(0)!) >>> 0, 0)
  return graphics[hash % graphics.length]
}
const shanghaiDate = (value: Date) => { const parts = new Intl.DateTimeFormat('en-US', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(value); return ['year', 'month', 'day'].map(type => parts.find(part => part.type === type)?.value).join('-') }
const stamp = (date: string) => { const [year, month, day] = date.split('-').map(Number); const value = new Date(0); value.setUTCFullYear(year, month - 1, day); value.setUTCHours(0, 0, 0, 0); return value.getTime() }
function elapsed(date: string) { return Math.round((stamp(shanghaiDate(new Date())) - stamp(date)) / 86400000) }

function Hero({ item, spaceCreatedAt, style, onPin, pinDisabled = false, pinning = false, originPinned = true }: { item: Anniversary | null; spaceCreatedAt: string; style?: CSSProperties; onPin?: () => void; pinDisabled?: boolean; pinning?: boolean; originPinned?: boolean }) {
  const created = new Date(spaceCreatedAt)
  const valid = !Number.isNaN(created.getTime())
  const date = item?.date || (valid ? shanghaiDate(created) : '')
  if (!date) return null
  const days = elapsed(date)
  const Graphic = anniversaryGraphic(item)
  const pinned = item ? item.pinned : originPinned
  return <article className={`anniversary-feature ${pinned ? 'is-pinned' : ''}`} style={style ?? recordCardStyle(item?.id || `origin:${spaceCreatedAt}`, item ? undefined : 'blue')} aria-label={item ? `${onPin ? '纪念日' : '首页展示'}：${item.title}` : '专属空间开始'}>
    {item ? <Graphic className="anniversary-feature-art" size={132} strokeWidth={0.8} aria-hidden="true" /> : <span className="anniversary-origin-circle" aria-hidden="true" />}
    <div className="anniversary-feature-top"><span className="anniversary-calendar"><Graphic size={22} aria-hidden="true" /></span>{onPin ? <button type="button" className="anniversary-feature-tag anniversary-pin" aria-label={item ? `${pinned ? '取消置顶' : '置顶'}：${item.title}` : '置顶：专属空间开始'} aria-pressed={pinned} disabled={pinDisabled || (!item && pinned)} onClick={onPin}><Pin size={12} aria-hidden="true" />{pinning ? '保存中' : pinned ? '已置顶' : '置顶'}</button> : <span className="anniversary-feature-tag">{pinned ? <><Pin size={12} aria-hidden="true" />已置顶</> : item ? kindLabels[item.kind] : '空间起点'}</span>}</div>
    <h2>{item?.title || '专属空间开始'}</h2>
    <p className="anniversary-count-label">{days >= 0 ? '已经' : '还有'}</p>
    <div className="anniversary-day-count"><strong>{Math.abs(days)}</strong><span>天</span></div>
    <div className="anniversary-feature-date"><Clock3 size={14} aria-hidden="true" /><time dateTime={date}>{date.replaceAll('-', '.')}{!item && valid ? ` · ${new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false }).format(created)}` : ''}</time></div>
  </article>
}

export function Anniversaries({ state, compact = false, notify }: { state: AnniversaryState; compact?: boolean; notify: (message: string) => void }) {
  const [busy, setBusy] = useState<string | null>(null)
  const pin = async (item: Anniversary) => {
    if (busy) return
    setBusy(item.id)
    try { await state.pin(item.id, !item.pinned); notify(item.pinned ? '已取消置顶，首页显示「专属空间开始」' : '已置顶，首页“小纪念”会展示这个日子') }
    catch (problem) { notify(problem instanceof Error ? problem.message : '置顶没保存成功，请再试一次。') }
    finally { setBusy(null) }
  }
  const pinOrigin = async () => {
    if (busy || !state.featured) return
    setBusy('origin')
    try { await state.pin(state.featured.id, false); notify('已置顶「专属空间开始」') }
    catch (problem) { notify(problem instanceof Error ? problem.message : '置顶没保存成功，请再试一次。') }
    finally { setBusy(null) }
  }
  const items = [...new Map([...(state.featured ? [state.featured] : []), ...state.anniversaries].map(item => [item.id, { ...item, pinned: item.id === state.featured?.id }])).values()].sort((a, b) => Number(b.pinned) - Number(a.pinned) || (Date.parse(b.createdAt) || 0) - (Date.parse(a.createdAt) || 0) || b.id.localeCompare(a.id))
  const styles = recordCardStyles(items, ['blue'])
  const originCard = <Hero key="origin" item={null} spaceCreatedAt={state.spaceCreatedAt} originPinned={!state.featured} onPin={() => void pinOrigin()} pinDisabled={busy !== null} pinning={busy === 'origin'} />
  const cards = items.map(item => <Hero key={item.id} item={item} style={styles.get(item.id)} spaceCreatedAt={state.spaceCreatedAt} onPin={() => void pin(item)} pinDisabled={busy !== null} pinning={busy === item.id} />)
  const hasOrigin = !!state.spaceCreatedAt && !Number.isNaN(new Date(state.spaceCreatedAt).getTime())
  if (!compact && !items.length && !hasOrigin && !state.error) return state.loading ? <TabLoading />
    : <EmptyTabState kind="anniversary" title="还没有纪念日" example="我们是 2025 年 5 月 20 日在一起的" />
  return <div className={`anniversaries ${compact ? 'is-compact' : ''}`}>
    {state.error && <div className="anniversary-error" role="alert"><span>{state.error}</span><button type="button" disabled={state.loading} onClick={() => void state.reload()} aria-label="刷新纪念日"><RefreshCw size={15} /></button></div>}
    {compact ? <Hero item={state.featured} style={state.featured ? styles.get(state.featured.id) : undefined} spaceCreatedAt={state.spaceCreatedAt} /> : <div className="anniversary-list">
      {state.featured ? [...cards, originCard] : [originCard, ...cards]}
    </div>}
    {!compact && state.nextCursor && <button className="anniversary-more" disabled={state.loading} onClick={() => void state.loadMore()}>{state.loading ? '正在加载…' : '查看更多纪念日'}</button>}
  </div>
}
