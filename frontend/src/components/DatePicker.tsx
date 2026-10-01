import { CalendarDays, ChevronLeft, ChevronRight } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import './DatePicker.css'

interface Props { id: string; value: string; onChange: (value: string) => void }

const WEEKDAYS = ['日', '一', '二', '三', '四', '五', '六']
const pad = (n: number) => String(n).padStart(2, '0')
const stamp = (year: number, month: number, day: number) => `${year}.${pad(month + 1)}.${pad(day)}`

/** 只留数字并按 YYYY.MM.DD 补点，边敲边成型。 */
function mask(raw: string) {
  const digits = raw.replace(/\D/g, '').slice(0, 8)
  if (digits.length > 6) return `${digits.slice(0, 4)}.${digits.slice(4, 6)}.${digits.slice(6)}`
  return digits.length > 4 ? `${digits.slice(0, 4)}.${digits.slice(4)}` : digits
}

/** 自绘日期控件：文本框可直接敲数字，点右侧图标或聚焦时展开月历。值用 YYYY.MM.DD 显示串。 */
export function DatePicker({ id, value, onChange }: Props) {
  const root = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const [cursor, setCursor] = useState(() => new Date())
  const today = new Date()
  const selected = /^(\d{4})\.(\d{2})\.(\d{2})$/.exec(value)
  const year = cursor.getFullYear()
  const month = cursor.getMonth()
  const daysInMonth = new Date(year, month + 1, 0).getDate()
  const lead = new Date(year, month, 1).getDay()
  const atThisMonth = year === today.getFullYear() && month === today.getMonth()

  useEffect(() => {
    if (!open) return
    setCursor(selected ? new Date(Number(selected[1]), Number(selected[2]) - 1, Number(selected[3])) : new Date())
    const onDown = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false) }
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') setOpen(false) }
    document.addEventListener('pointerdown', onDown)
    document.addEventListener('keydown', onKey)
    return () => { document.removeEventListener('pointerdown', onDown); document.removeEventListener('keydown', onKey) }
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  const pick = (day: number) => { onChange(stamp(year, month, day)); setOpen(false) }
  const shift = (delta: number) => setCursor(new Date(year, month + delta, 1))

  return <div className="date-picker" ref={root}>
    <div className="date-picker-field">
      <input className="line-input" id={id} type="text" inputMode="numeric" autoComplete="off" placeholder="YYYY.MM.DD" maxLength={10}
        value={value} onFocus={() => setOpen(true)} onClick={() => setOpen(true)} onChange={(event) => onChange(mask(event.target.value))} />
      <button type="button" className="date-picker-toggle" aria-label={open ? '收起日历' : '展开日历'} aria-expanded={open} onClick={() => setOpen((v) => !v)}>
        <CalendarDays size={16} aria-hidden="true" />
      </button>
    </div>
    {open && <div className="date-picker-panel" role="group" aria-label={`选择日期，${year} 年 ${month + 1} 月`}>
      <div className="date-picker-head">
        <button type="button" aria-label="上一个月" onClick={() => shift(-1)}><ChevronLeft size={17} aria-hidden="true" /></button>
        <strong>{year} 年 {month + 1} 月</strong>
        <button type="button" aria-label="下一个月" disabled={atThisMonth} onClick={() => shift(1)}><ChevronRight size={17} aria-hidden="true" /></button>
      </div>
      <div className="date-picker-week">{WEEKDAYS.map((day) => <span key={day}>{day}</span>)}</div>
      <div className="date-picker-days">
        {Array.from({ length: lead }, (_, i) => <span className="is-blank" key={`blank-${i}`} />)}
        {Array.from({ length: daysInMonth }, (_, i) => i + 1).map((day) => {
          const isToday = atThisMonth && day === today.getDate()
          const isSelected = !!selected && Number(selected[1]) === year && Number(selected[2]) === month + 1 && Number(selected[3]) === day
          return <button type="button" key={day} disabled={atThisMonth && day > today.getDate()}
            className={`${isSelected ? 'is-selected' : ''} ${isToday ? 'is-today' : ''}`} aria-pressed={isSelected} onClick={() => pick(day)}>{day}</button>
        })}
      </div>
    </div>}
  </div>
}
