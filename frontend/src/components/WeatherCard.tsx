import { ScrollView } from '@tarojs/components'
import { useState } from 'react'
import { ChevronDown, Cloud, CloudFog, CloudLightning, CloudRain, CloudSnow, CloudSun, Moon, Shirt, Sun, Umbrella, Wind } from './Icons'
import type { WeatherCardData, WeatherHour, WeatherView } from '../api/care'
import './WeatherCard.css'

function WeatherIcon({ code }: { code: number }) {
 const Icon = code < 0 ? Cloud : code === 0 ? Sun : code < 3 ? CloudSun : code === 3 ? Cloud : code === 45 || code === 48 ? CloudFog : code >= 95 ? CloudLightning : code >= 71 && code <= 77 || code >= 85 && code <= 86 ? CloudSnow : CloudRain
 return <Icon size={44} strokeWidth={1.4} aria-hidden="true" />
}
const shanghaiTime = (value: string) => { const stamp = Date.parse(value); return Number.isNaN(stamp) ? '暂无' : new Date(stamp + 8 * 60 * 60_000).toISOString().slice(11, 16) }
const rounded = (value: number | null | undefined, suffix = '') => value == null ? '暂无' : `${Math.round(value)}${suffix}`
const defaultMetrics = ['temperature', 'feels_like', 'rain', 'wind', 'humidity', 'visibility', 'fog', 'pm25', 'aqi', 'uv', 'clothing']
function WeatherDetails({ card, view, separate }: { card: WeatherCardData; view: WeatherView; separate: boolean }) {
 const has = (metric: string) => view.metrics.includes(metric)
 const rows: { key: string; label: string; value: (hour: WeatherHour) => string }[] = [
  { key: 'temperature', label: '气温', value: (h: WeatherHour) => rounded(h.temperature, '°') },
  { key: 'feels_like', label: '体感', value: (h: WeatherHour) => rounded(h.feelsLike, '°') },
  { key: 'rain', label: '降水概率', value: (h: WeatherHour) => rounded(h.rainChance, '%') },
  { key: 'wind', label: '风速 km/h', value: (h: WeatherHour) => rounded(h.wind) },
  { key: 'humidity', label: '湿度', value: (h: WeatherHour) => rounded(h.humidity, '%') },
  { key: 'visibility', label: '能见度', value: (h: WeatherHour) => h.visibility == null ? '暂无' : `${(h.visibility / 1000).toFixed(1)} km` },
  { key: 'aqi', label: card.aqiLabel || '中国 AQI', value: (h: WeatherHour) => rounded(h.aqi) },
  { key: 'uv', label: '紫外线', value: (h: WeatherHour) => rounded(h.uv) },
 ].filter((row) => has(row.key))
 const air = card.currentAir
 const current = card.currentWeather
 return <section className="weather-personal-details">
  {separate && <h3 className="weather-personal-heading">{view.recipientNames.map((name) => `@${name}`).join(' ')} 关注的天气</h3>}
  {has('temperature') && separate && <p className="weather-personal-temperature">{rounded(card.day.min)} — {rounded(card.day.max)}°C</p>}
  {current && ['temperature', 'feels_like', 'humidity', 'wind', 'visibility'].some(has) && <div className="weather-current-air"><h3>此刻的天气</h3><div>{has('temperature') && <span>气温 <strong>{rounded(current.temperature, '°C')}</strong></span>}{has('feels_like') && <span>体感 <strong>{rounded(current.feelsLike, '°C')}</strong></span>}{has('humidity') && <span>湿度 <strong>{rounded(current.humidity, '%')}</strong></span>}{has('wind') && <span>风速 <strong>{rounded(current.wind, ' km/h')}</strong></span>}{has('visibility') && <span>能见度 <strong>{current.visibility == null ? '暂无' : `${(current.visibility / 1000).toFixed(1)} km`}</strong></span>}</div><p>查询于 {shanghaiTime(current.retrievedAt)}，与日期预报分开展示。</p></div>}
  {view.comparisons.length > 0 ? <ul className="weather-comparisons">{view.comparisons.map((line, i) => <li key={`${line.metric}-${i}`}>{line.text}</li>)}</ul> : has('temperature') && card.comparison && <p className="weather-comparison">{card.comparison}</p>}
  {(has('rain') || has('wind')) && <div className="weather-metrics">{has('rain') && <span><Umbrella size={13} />降水概率<strong>{rounded(card.day.rainChance, '%')}</strong></span>}{has('wind') && <span><Wind size={13} />风速上限<strong>{rounded(card.day.wind, ' km/h')}</strong></span>}</div>}
  {card.hours.length > 0 && rows.length > 0 && <ScrollView className="weather-hour-scroll" scrollX><table className="weather-hours" style={{ display: 'block' }}><caption className="sr-only">{card.day.date} 分时天气预报</caption><thead style={{ display: 'block' }}><tr style={{ display: 'flex' }}><th scope="col" style={{ flex: '0 0 25%' }}>时段</th>{card.hours.map((hour) => <th scope="col" style={{ flex: '1 1 0' }} key={hour.time}>{hour.time.slice(11, 16)}</th>)}</tr></thead><tbody style={{ display: 'block' }}>{rows.map((row) => <tr key={row.key} style={{ display: 'flex' }}><th scope="row" style={{ flex: '0 0 25%' }}>{row.label}</th>{card.hours.map((hour) => <td style={{ flex: '1 1 0' }} key={hour.time}>{row.value(hour)}</td>)}</tr>)}</tbody></table></ScrollView>}
  {air && (has('pm25') || has('aqi')) && <div className="weather-current-air"><h3>此刻的空气</h3><div>{has('pm25') && <span>PM2.5 <strong>{rounded(air.pm25)}</strong><small> μg/m³</small></span>}{has('aqi') && <span>{air.aqiLabel} <strong>{rounded(air.aqi)}</strong></span>}</div>{has('pm25') && card.airComparison && <p>{card.airComparison}</p>}<p>查询于 {shanghaiTime(air.retrievedAt)}，实时空气与日期预报分开展示。</p></div>}
  {has('fog') && !view.alerts.some((line) => line.includes('雾')) && <p className="weather-note">当前预报没有明确的雾天气信号。</p>}
  {(has('aqi') || has('pm25')) && <p className="weather-note">{has('pm25') ? 'PM2.5 显示实时值，详细预报暂不可用。' : ''}{has('aqi') ? '空气预报只覆盖接口返回的时段，暂无值不代表空气好。' : ''}</p>}
  <p className="weather-preference-hint">{view.preferenceHint}</p>
 </section>
}
function WeatherBrief({ card, view, separate }: { card: WeatherCardData; view: WeatherView; separate: boolean }) {
 const query = card.mode.startsWith('query')
 const [expanded, setExpanded] = useState(false)
 const lines = (view.summary ?? (view.alerts.length ? view.alerts : view.metrics.includes('temperature') && card.comparison ? [card.comparison] : [])).slice(0, 3)
 return <section className="weather-personal-summary">
  {separate && <h3 className="weather-personal-heading">{view.recipientNames.map((name) => `@${name}`).join(' ')} 的天气重点</h3>}
  {separate && view.metrics.includes('temperature') && <p className="weather-personal-temperature">{rounded(card.day.min)} — {rounded(card.day.max)}°C</p>}
  {lines.length > 0 && <ul className="weather-summary-lines">{lines.map((line, index) => <li key={index}>{line}</li>)}</ul>}
  {query && card.mode !== 'query_tomorrow' && card.currentAir && (view.metrics.includes('pm25') || view.metrics.includes('aqi')) && <div className="weather-query-air" aria-label="实时空气质量">{view.metrics.includes('pm25') && <span>PM2.5 <strong>{rounded(card.currentAir.pm25)}</strong><small> μg/m³</small></span>}{view.metrics.includes('aqi') && <span>{card.currentAir.aqiLabel} <strong>{rounded(card.currentAir.aqi)}</strong></span>}</div>}
  {view.clothing && <div className="weather-clothing"><Shirt size={17} aria-hidden="true" /><div><h3>{card.mode === 'night' || card.mode === 'query_tomorrow' ? '明天' : '今天'}怎么穿</h3><p>{view.clothing}</p>{view.localAdvice && <p className="weather-local-advice">{view.localAdvice}</p>}</div></div>}
  <details className={`weather-more${expanded ? ' is-expanded' : ''}`} open={expanded}><summary role="button" aria-expanded={expanded} onClick={(event) => { event.preventDefault(); setExpanded((value) => !value) }}>天气明细<ChevronDown size={13} style={{ transform: expanded ? 'rotate(180deg)' : 'rotate(0deg)' }} aria-hidden="true" /></summary>{expanded && <WeatherDetails card={card} view={view} separate={false} />}</details>
 </section>
}
export function WeatherCard({ card }: { card: WeatherCardData }) {
 const night = card.mode === 'night'
 const query = card.mode.startsWith('query')
 const tomorrow = night || card.mode === 'query_tomorrow'
 const current = card.mode === 'query' ? card.currentWeather : undefined
 const location = [card.region.province, card.region.city === card.region.province ? '' : card.region.city, card.region.district].filter(Boolean).join(' · ')
 const views = card.views?.length ? card.views : [{ recipientIds: card.recipientIds, recipientNames: card.recipientNames, metrics: defaultMetrics, comparisons: [], alerts: card.alerts, clothing: card.clothing, preferenceHint: '想精简一点，可以直接告诉我你关注哪些天气指标。' }]
 const showTemperature = card.mode === 'morning' || views.length === 1 && views[0].metrics.includes('temperature')
 return <article className={`weather-card ${night ? 'weather-night' : 'weather-morning'}`} aria-label={`${query ? '查询' : night ? '晚安' : '早安'}天气 · ${location}`}>
  <header className="weather-card-header"><span>{query ? <CloudSun size={14} /> : night ? <Moon size={14} /> : <Sun size={14} />}{query ? '刚刚查到的天气' : night ? '晚安，明天也从容一点' : '早安，开启今天的小日常'}</span><time dateTime={card.day.date}>{card.day.date.slice(5).replace('-', '.')}</time></header>
  <div className="weather-recipients">{card.recipientNames.map((name, i) => <span key={card.recipientIds[i]}>@{name}</span>)}</div>
  <div className="weather-main"><div><p className="weather-location">{location}</p>{showTemperature && <div className="weather-temperature">{current?.temperature != null ? <>{Math.round(current.temperature)}<small>°C</small></> : <>{Math.round(card.day.min)}<span> — </span>{Math.round(card.day.max)}<small>°C</small></>}</div>}<p className="weather-description">{current ? '现在' : tomorrow ? '明天' : '今天'} · {current?.description || card.description}</p>{current && showTemperature && <p className="weather-note">今天 {rounded(card.day.min)} — {rounded(card.day.max)}°C{views[0].metrics.includes('feels_like') && current.feelsLike != null ? ` · 体感 ${rounded(current.feelsLike, '°C')}` : ''}</p>}</div><div className="weather-symbol"><WeatherIcon code={current?.code ?? card.day.code} /></div></div>
  {card.queryNotice && <p className="weather-note weather-query-notice" role="status">{card.queryNotice}</p>}
  {card.precision === 'city' && card.region.district && <p className="weather-note">区县坐标暂缺，使用{card.region.city}范围预报</p>}
  {night || query ? views.map((view, i) => <WeatherBrief key={i} card={card} view={view} separate={views.length > 1} />) : <div className="weather-day-reminders"><h3>今天的提醒</h3>{card.reminders.length ? <ul>{card.reminders.map((reminder, i) => <li key={`${reminder.time}-${i}`}><time>{reminder.time}</time><span>{card.recipientIds.length > 1 && <small>{reminder.recipientIds.map((id) => card.recipientNames[card.recipientIds.indexOf(id)]).filter(Boolean).map((name) => `@${name}`).join(' ')} </small>}{reminder.title}</span></li>)}</ul> : <p>今天没有待提醒事项，按自己的节奏来。</p>}{card.moreReminders > 0 && <p>还有至少 {card.moreReminders} 项，请到待办页查看。</p>}</div>}
  <footer className="weather-updated">{query ? '更新' : '预报'} · {shanghaiTime(card.generatedAt)} 查询</footer>
 </article>
}
