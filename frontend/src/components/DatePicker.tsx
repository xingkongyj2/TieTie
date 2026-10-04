import { CalendarDays, ChevronDown, ChevronLeft, ChevronRight } from 'lucide-react'
import { useLayoutEffect, useRef, useState } from 'react'
import { Sheet } from './Sheet'
import './DatePicker.css'

interface Props { id: string; value: string; onChange: (value: string) => void; title?: string; clearLabel?: string; placeholder?: string; allowFuture?: boolean; disabled?: boolean }

const WEEKDAYS = ['日', '一', '二', '三', '四', '五', '六']
const pad = (n: number) => String(n).padStart(2, '0')
const stamp = (date: Date) => `${date.getFullYear()}.${pad(date.getMonth() + 1)}.${pad(date.getDate())}`
const dateLabel = (date: Date) => `${date.getFullYear()} 年 ${date.getMonth() + 1} 月 ${date.getDate()} 日`

function parseDate(value: string) {
  const match = /^(\d{4})\.(\d{2})\.(\d{2})$/.exec(value)
  if (!match) return null
  const [year, month, day] = match.slice(1).map(Number)
  const date = new Date(year, month - 1, day)
  date.setFullYear(year)
  return date.getFullYear() === year && date.getMonth() === month - 1 && date.getDate() === day ? date : null
}

function DateDialog({ id, title, value, clearLabel, allowFuture, onClose, onConfirm }: {
  id: string; title: string; value: string; clearLabel?: string; allowFuture: boolean; onClose: () => void; onConfirm: (value: string) => void
}) {
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  const parsed = parseDate(value)
  const initial = parsed && (allowFuture || parsed <= today) ? parsed : today
  const [draft, setDraft] = useState<Date | null>(clearLabel && !value ? null : initial)
  const [cursor, setCursor] = useState(initial)
  const [view, setView] = useState<'days' | 'years' | 'months'>('days')
  const yearsRoot = useRef<HTMLDivElement>(null)
  const monthHeading = useRef<HTMLButtonElement>(null)
  const restoreHeadingFocus = useRef(false)
  const year = cursor.getFullYear()
  const month = cursor.getMonth()
  const firstYear = Math.min(1900, initial.getFullYear())
  const lastYear = allowFuture ? Math.max(today.getFullYear() + 100, year) : today.getFullYear()
  const lead = new Date(year, month, 1).getDay()
  const daysInMonth = new Date(year, month + 1, 0).getDate()
  const lastMonth = !allowFuture && year === today.getFullYear() && month === today.getMonth()

  useLayoutEffect(() => {
    if (restoreHeadingFocus.current) {
      monthHeading.current?.focus({ preventScroll: true })
      restoreHeadingFocus.current = false
    }
    if (view !== 'years') return
    const root = yearsRoot.current
    const selected = root?.querySelector<HTMLElement>('[aria-pressed="true"]')
    if (root && selected) root.scrollTop = selected.offsetTop - (root.clientHeight - selected.offsetHeight) / 2
  }, [view])

  const selectYear = (nextYear: number) => {
    const nextMonth = !allowFuture && nextYear === today.getFullYear() ? Math.min(month, today.getMonth()) : month
    setCursor(new Date(nextYear, nextMonth, 1))
    restoreHeadingFocus.current = true
    setView('months')
  }

  return <Sheet title={title} onClose={onClose}>{(close) => <div className="date-picker-dialog" id={`${id}-calendar`}>
    <div className="date-picker-summary">
      <span>已选日期</span>
      <strong aria-live="polite">{draft ? stamp(draft) : '未设置'}</strong>
    </div>
    <div className="date-picker-panel">
      <div className="date-picker-head">
        <button type="button" className="date-picker-nav" aria-label={view === 'days' ? '上一个月' : '返回日历'}
          disabled={view === 'days' && year === firstYear && month === 0}
          onClick={() => view === 'days' ? setCursor(new Date(year, month - 1, 1)) : setView('days')}>
          <ChevronLeft size={18} aria-hidden="true" />
        </button>
        <div className="date-picker-heading">
          <button type="button" className={view === 'years' ? 'is-active' : ''} aria-label={`选择年份，当前 ${year} 年`} aria-expanded={view === 'years'} onClick={() => setView(view === 'years' ? 'days' : 'years')}>{year} 年<ChevronDown size={13} aria-hidden="true" /></button>
          <button type="button" ref={monthHeading} className={view === 'months' ? 'is-active' : ''} aria-label={`选择月份，当前 ${month + 1} 月`} aria-expanded={view === 'months'} onClick={() => setView(view === 'months' ? 'days' : 'months')}>{month + 1} 月<ChevronDown size={13} aria-hidden="true" /></button>
        </div>
        <button type="button" className="date-picker-nav" aria-label={view === 'days' ? '下一个月' : '返回日历'} disabled={view === 'days' && lastMonth}
          onClick={() => view === 'days' ? setCursor(new Date(year, month + 1, 1)) : setView('days')}>
          {view === 'days' ? <ChevronRight size={18} aria-hidden="true" /> : <CalendarDays size={17} aria-hidden="true" />}
        </button>
      </div>
      <div className="date-picker-body">
        {view === 'years' ? <div className="date-picker-options" ref={yearsRoot} role="group" aria-label="选择年份">
          {Array.from({ length: lastYear - firstYear + 1 }, (_, index) => firstYear + index).map((option) => <button type="button" key={option} aria-label={`${option} 年`} aria-pressed={option === year} className={option === year ? 'is-selected' : ''} onClick={() => selectYear(option)}>{option} 年</button>)}
        </div> : view === 'months' ? <div className="date-picker-options date-picker-months" role="group" aria-label="选择月份">
          {Array.from({ length: 12 }, (_, option) => <button type="button" key={option} aria-label={`${option + 1} 月`} aria-pressed={option === month} className={option === month ? 'is-selected' : ''}
            disabled={!allowFuture && year === today.getFullYear() && option > today.getMonth()} onClick={() => { setCursor(new Date(year, option, 1)); restoreHeadingFocus.current = true; setView('days') }}>{option + 1} 月</button>)}
        </div> : <div role="group" aria-label={`选择日期，${year} 年 ${month + 1} 月`}>
          <div className="date-picker-week">{WEEKDAYS.map((day) => <span key={day}>{day}</span>)}</div>
          <div className="date-picker-days">
            {Array.from({ length: 42 }, (_, index) => {
              const day = index - lead + 1
              if (day < 1 || day > daysInMonth) return <span className="is-blank" key={index} />
              const date = new Date(year, month, day)
              const selected = draft !== null && stamp(draft) === stamp(date)
              const isToday = stamp(today) === stamp(date)
              return <button type="button" key={index} disabled={!allowFuture && date > today}
                className={`${selected ? 'is-selected' : ''} ${isToday ? 'is-today' : ''}`} aria-label={dateLabel(date)} aria-pressed={selected} onClick={() => setDraft(date)}>{day}</button>
            })}
          </div>
        </div>}
      </div>
    </div>
    {clearLabel && <button type="button" className="secondary-button date-picker-clear" onClick={() => setDraft(null)}>{clearLabel}</button>}
    <button type="button" className="primary-button date-picker-confirm" onClick={() => { onConfirm(draft ? stamp(draft) : ''); close() }}>确定</button>
  </div>}</Sheet>
}

/** 日期统一在弹窗内选择，确认后才更新表单。 */
export function DatePicker({ id, title = '选择日期', clearLabel, placeholder = 'YYYY.MM.DD', value, onChange, allowFuture = false, disabled = false }: Props) {
  const [open, setOpen] = useState(false)
  const show = () => { if (!disabled) setOpen(true) }
  return <div className="date-picker">
    <div className="date-picker-field">
      <input className="line-input" id={id} type="text" readOnly inputMode="none" autoComplete="off" placeholder={placeholder}
        disabled={disabled} value={value} aria-haspopup="dialog" aria-expanded={open} aria-controls={`${id}-calendar`} onClick={show}
        onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ' || event.altKey && event.key === 'ArrowDown') { event.preventDefault(); show() } }} />
      <button type="button" className="date-picker-toggle" disabled={disabled} aria-label={title} aria-haspopup="dialog" aria-controls={`${id}-calendar`} aria-expanded={open} onClick={show}>
        <CalendarDays size={17} aria-hidden="true" />
      </button>
    </div>
    {open && <DateDialog id={id} title={title} clearLabel={clearLabel} value={value} allowFuture={allowFuture} onClose={() => setOpen(false)} onConfirm={onChange} />}
  </div>
}
