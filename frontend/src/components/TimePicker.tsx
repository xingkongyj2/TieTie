import { useEffect, useId, useRef, useState } from 'react'
import { ScrollView, View } from '@tarojs/components'
import { nextFrame } from '../lib/platform'
import { Sheet } from './Sheet'
import './TimePicker.css'

const ROW_HEIGHT = 44
const pad = (value: number) => String(value).padStart(2, '0')

function TimeWheel({ label, count, value, onChange, disabled }: {
  label: string; count: number; value: number; onChange: (value: number) => void; disabled: boolean
}) {
  const id = useId().replace(/:/g, '')
  const [scrollTop, setScrollTop] = useState(value * ROW_HEIGHT)
  const latestScroll = useRef(value * ROW_HEIGHT)
  const touching = useRef(false)
  const snapTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(() => () => { if (snapTimer.current) clearTimeout(snapTimer.current) }, [])
  const bounded = (next: number) => Math.max(0, Math.min(count - 1, next))
  const pick = (next: number) => {
    if (disabled) return
    const selection = bounded(next)
    onChange(selection)
    setScrollTop(latestScroll.current)
    nextFrame(() => setScrollTop(selection * ROW_HEIGHT))
  }
  const scheduleSnap = () => {
    if (snapTimer.current) clearTimeout(snapTimer.current)
    snapTimer.current = setTimeout(() => {
      snapTimer.current = null
      if (!disabled && !touching.current) pick(Math.round(latestScroll.current / ROW_HEIGHT))
    }, 160)
  }
  return <div className="time-picker-column">
    <span className="time-picker-label">{label}</span>
    <ScrollView className="time-picker-wheel" scrollY enhanced showScrollbar={false} scrollTop={scrollTop}
      style={{ padding: 0, height: '220px', pointerEvents: disabled ? 'none' : undefined, opacity: disabled ? .6 : 1 }}
      onTouchStart={() => { touching.current = true; if (snapTimer.current) clearTimeout(snapTimer.current) }}
      onTouchEnd={() => { touching.current = false; scheduleSnap() }}
      onTouchCancel={() => { touching.current = false; scheduleSnap() }}
      onScroll={(event) => {
        latestScroll.current = event.detail.scrollTop
        if (disabled) return
        const selection = bounded(Math.round(event.detail.scrollTop / ROW_HEIGHT))
        onChange(selection)
        if (!touching.current && Math.abs(event.detail.scrollTop - selection * ROW_HEIGHT) > .5) scheduleSnap()
      }}>
      <View style={{ height: '88px' }} />
      {Array.from({ length: count }, (_, index) => <button type="button" role="option" id={`${id}-${index}`} key={index}
        aria-selected={index === value} disabled={disabled}
        className={`time-picker-option ${index === value ? 'is-selected' : ''}`}
        onClick={() => pick(index)}>{pad(index)}</button>)}
      <View style={{ height: '88px' }} />
    </ScrollView>
  </div>
}

export function TimePicker({ title, value, onClose, onConfirm }: {
  title: string; value: string; onClose: () => void; onConfirm: (time: string) => Promise<boolean>
}) {
  const [hour, setHour] = useState(() => Math.max(0, Math.min(23, Number(value.slice(0, 2)) || 0)))
  const [minute, setMinute] = useState(() => Math.max(0, Math.min(59, Number(value.slice(3, 5)) || 0)))
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
