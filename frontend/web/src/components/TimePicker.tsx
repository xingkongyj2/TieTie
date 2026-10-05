import { useId, useLayoutEffect, useRef, useState } from 'react'
import { Sheet } from './Sheet'
import './TimePicker.css'

const ROW_HEIGHT = 44
const pad = (value: number) => String(value).padStart(2, '0')

function TimeWheel({ label, count, value, onChange, disabled }: {
  label: string; count: number; value: number; onChange: (value: number) => void; disabled: boolean
}) {
  const id = useId()
  const root = useRef<HTMLDivElement>(null)
  const initial = useRef(value)
  useLayoutEffect(() => { if (root.current) root.current.scrollTop = initial.current * ROW_HEIGHT }, [])
  const pick = (next: number) => {
    const bounded = Math.max(0, Math.min(count - 1, next))
    onChange(bounded)
    root.current?.scrollTo({ top: bounded * ROW_HEIGHT, behavior: 'instant' })
    root.current?.focus({ preventScroll: true })
  }
  return <div className="time-picker-column">
    <span className="time-picker-label">{label}</span>
    <div className="time-picker-wheel" ref={root} role="listbox" tabIndex={disabled ? -1 : 0}
      aria-label={label} aria-disabled={disabled} aria-activedescendant={`${id}-${value}`}
      onScroll={(event) => {
        if (!disabled) onChange(Math.max(0, Math.min(count - 1, Math.round(event.currentTarget.scrollTop / ROW_HEIGHT))))
      }}
      onKeyDown={(event) => {
        if (disabled) return
        const next = event.key === 'ArrowUp' ? value - 1 : event.key === 'ArrowDown' ? value + 1
          : event.key === 'PageUp' ? value - 5 : event.key === 'PageDown' ? value + 5
          : event.key === 'Home' ? 0 : event.key === 'End' ? count - 1 : null
        if (next !== null) { event.preventDefault(); pick(next) }
      }}>
      {Array.from({ length: count }, (_, index) => <button type="button" role="option" id={`${id}-${index}`} key={index}
        tabIndex={-1} aria-selected={index === value} disabled={disabled}
        className={`time-picker-option ${index === value ? 'is-selected' : ''}`}
        onClick={() => pick(index)}>{pad(index)}</button>)}
    </div>
  </div>
}

export function TimePicker({ title, value, onClose, onConfirm }: {
  title: string; value: string; onClose: () => void; onConfirm: (time: string) => Promise<boolean>
}) {
  const [hour, setHour] = useState(() => Number(value.slice(0, 2)))
  const [minute, setMinute] = useState(() => Number(value.slice(3, 5)))
  const [saving, setSaving] = useState(false)
  const submitting = useRef(false)
  return <Sheet title={title} onClose={onClose}>{(close) => <div className="time-picker">
    <div className="time-picker-dials">
      <div className="time-picker-selection" aria-hidden="true" />
      <TimeWheel label="小时" count={24} value={hour} onChange={setHour} disabled={saving} />
      <span className="time-picker-colon" aria-hidden="true">:</span>
      <TimeWheel label="分钟" count={60} value={minute} onChange={setMinute} disabled={saving} />
    </div>
    <button type="button" className="primary-button time-picker-confirm" disabled={saving} onClick={async () => {
      if (submitting.current) return
      submitting.current = true
      setSaving(true)
      try { if (await onConfirm(`${pad(hour)}:${pad(minute)}`)) close() }
      finally { submitting.current = false; setSaving(false) }
    }}>{saving ? '正在保存…' : '确定'}</button>
  </div>}</Sheet>
}
