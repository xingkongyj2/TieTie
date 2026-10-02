import { ChevronDown, Cloud, CloudFog, CloudLightning, CloudRain, CloudSnow, CloudSun, Moon, Shirt, Sun, Umbrella, Wind } from 'lucide-react'
import type { WeatherCardData, WeatherHour, WeatherView } from '../api/care'
import './WeatherCard.css'

function WeatherIcon({ code }: { code: number }) {
 const Icon = code < 0 ? Cloud : code === 0 ? Sun : code < 3 ? CloudSun : code === 3 ? Cloud : code === 45 || code === 48 ? CloudFog : code >= 95 ? CloudLightning : code >= 71 && code <= 77 || code >= 85 && code <= 86 ? CloudSnow : CloudRain
 return <Icon size={44} strokeWidth={1.4} aria-hidden="true" />
}
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
 return <section className="weather-personal-details">
  {separate && <h3 className="weather-personal-heading">{view.recipientNames.map((name) => `@${name}`).join(' ')} 关注的天气</h3>}
  {has('temperature') && separate && <p className="weather-personal-temperature">{rounded(card.day.min)} — {rounded(card.day.max)}°C</p>}
  {view.comparisons.length > 0 ? <ul className="weather-comparisons">{view.comparisons.map((line, i) => <li key={`${line.metric}-${i}`}>{line.text}</li>)}</ul> : has('temperature') && card.comparison && <p className="weather-comparison">{card.comparison}</p>}
  {(has('rain') || has('wind')) && <div className="weather-metrics">{has('rain') && <span><Umbrella size={13} />降水概率<strong>{rounded(card.day.rainChance, '%')}</strong></span>}{has('wind') && <span><Wind size={13} />风速上限<strong>{rounded(card.day.wind, ' km/h')}</strong></span>}</div>}
  {card.hours.length > 0 && rows.length > 0 && <div className="weather-hour-scroll"><table className="weather-hours"><caption className="sr-only">明天分时天气预报</caption><thead><tr><th scope="col">时段</th>{card.hours.map((hour) => <th scope="col" key={hour.time}>{hour.time.slice(11, 16)}</th>)}</tr></thead><tbody>{rows.map((row) => <tr key={row.key}><th scope="row">{row.label}</th>{card.hours.map((hour) => <td key={hour.time}>{row.value(hour)}</td>)}</tr>)}</tbody></table></div>}
  {air && (has('pm25') || has('aqi')) && <div className="weather-current-air"><h3>此刻的空气</h3><div>{has('pm25') && <span>PM2.5 <strong>{rounded(air.pm25)}</strong><small> μg/m³</small></span>}{has('aqi') && <span>{air.aqiLabel} <strong>{rounded(air.aqi)}</strong></span>}</div>{has('pm25') && card.airComparison && <p>{card.airComparison}</p>}<p>查询于 {new Date(air.retrievedAt).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false })}，实时空气不代表明天。</p></div>}
  {has('fog') && !view.alerts.some((line) => line.includes('雾')) && <p className="weather-note">当前预报没有明确的雾天气信号。</p>}
  {(has('aqi') || has('pm25')) && <p className="weather-note">{has('pm25') ? 'PM2.5 显示实时值，详细预报暂不可用。' : ''}{has('aqi') ? '空气预报只覆盖接口返回的时段，暂无值不代表空气好。' : ''}</p>}
  <p className="weather-preference-hint">{view.preferenceHint}</p>
 </section>
}
function WeatherBrief({ card, view, separate }: { card: WeatherCardData; view: WeatherView; separate: boolean }) {
 const lines = (view.summary ?? (view.alerts.length ? view.alerts : view.metrics.includes('temperature') && card.comparison ? [card.comparison] : [])).slice(0, 3)
 return <section className="weather-personal-summary">
  {separate && <h3 className="weather-personal-heading">{view.recipientNames.map((name) => `@${name}`).join(' ')} 的天气重点</h3>}
  {separate && view.metrics.includes('temperature') && <p className="weather-personal-temperature">{rounded(card.day.min)} — {rounded(card.day.max)}°C</p>}
  {lines.length > 0 && <ul className="weather-summary-lines">{lines.map((line, index) => <li key={index}>{line}</li>)}</ul>}
  {view.clothing && <div className="weather-clothing"><Shirt size={17} aria-hidden="true" /><div><h3>明天怎么穿</h3><p>{view.clothing}</p>{view.localAdvice && <p className="weather-local-advice">{view.localAdvice}</p>}</div></div>}
  <details className="weather-more"><summary>天气明细<ChevronDown size={13} aria-hidden="true" /></summary><WeatherDetails card={card} view={view} separate={false} /></details>
 </section>
}
export function WeatherCard({ card }: { card: WeatherCardData }) {
 const night = card.mode === 'night'
 const location = [card.region.province, card.region.city === card.region.province ? '' : card.region.city, card.region.district].filter(Boolean).join(' · ')
 const views = card.views?.length ? card.views : [{ recipientIds: card.recipientIds, recipientNames: card.recipientNames, metrics: defaultMetrics, comparisons: [], alerts: card.alerts, clothing: card.clothing, preferenceHint: '想精简一点，可以直接告诉我你关注哪些天气指标。' }]
 const showTemperature = !night || views.length === 1 && views[0].metrics.includes('temperature')
 return <article className={`weather-card ${night ? 'weather-night' : 'weather-morning'}`} aria-label={`${night ? '晚安' : '早安'}天气 · ${location}`}>
  <header className="weather-card-header"><span>{night ? <Moon size={14} /> : <Sun size={14} />}{night ? '晚安，明天也从容一点' : '早安，开启今天的小日常'}</span><time dateTime={card.day.date}>{card.day.date.slice(5).replace('-', '.')}</time></header>
  <div className="weather-recipients">{card.recipientNames.map((name, i) => <span key={card.recipientIds[i]}>@{name}</span>)}</div>
  <div className="weather-main"><div><p className="weather-location">{location}</p>{showTemperature && <div className="weather-temperature">{Math.round(card.day.min)}<span> — </span>{Math.round(card.day.max)}<small>°C</small></div>}<p className="weather-description">{night ? '明天' : '今天'} · {card.description}</p></div><div className="weather-symbol"><WeatherIcon code={card.day.code} /></div></div>
  {card.precision === 'city' && card.region.district && <p className="weather-note">区县坐标暂缺，使用{card.region.city}范围预报</p>}
  {night ? views.map((view, i) => <WeatherBrief key={i} card={card} view={view} separate={views.length > 1} />) : <div className="weather-day-reminders"><h3>今天的提醒</h3>{card.reminders.length ? <ul>{card.reminders.map((reminder, i) => <li key={`${reminder.time}-${i}`}><time>{reminder.time}</time><span>{card.recipientIds.length > 1 && <small>{reminder.recipientIds.map((id) => card.recipientNames[card.recipientIds.indexOf(id)]).filter(Boolean).map((name) => `@${name}`).join(' ')} </small>}{reminder.title}</span></li>)}</ul> : <p>今天没有待提醒事项，按自己的节奏来。</p>}{card.moreReminders > 0 && <p>还有至少 {card.moreReminders} 项，请到提醒页查看。</p>}</div>}
  <footer className="weather-updated">预报 · {new Date(card.generatedAt).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false })} 查询</footer>
 </article>
}
