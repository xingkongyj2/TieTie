import { Cake, CloudSun, Moon } from 'lucide-react'
import { useId, useState, type ReactNode } from 'react'
import type { AISettings, RelationshipState } from '../types'
import { Anniversary, ReminderBoard } from './Tools'
import { SpaceBuddy } from './SpaceBuddies'
import './LittleThings.css'

type CareKey = 'weatherCare' | 'anniversaryReminders' | 'quietHours'

interface Props {
  state: RelationshipState
  reminderState?: RelationshipState
  onSaveSettings: (settings: AISettings) => Promise<void>
  onToggle: (id: string) => Promise<void>
  onCancel?: (id: string) => Promise<void>
  reminderNotice?: string
  remindersLoading?: boolean
  onReloadReminders?: () => Promise<void>
  notify: (text: string) => void
}

function CareRow({ icon, title, description, checked, disabled, onChange }: { icon: ReactNode; title: string; description: string; checked: boolean; disabled: boolean; onChange: () => void }) {
  const descriptionId = useId()
  return <div className="setting-row"><span className="setting-icon">{icon}</span><div className="setting-copy"><strong>{title}</strong><p id={descriptionId}>{description}</p></div><button type="button" className={`toggle ${checked ? 'is-on' : ''}`} role="switch" aria-checked={checked} aria-label={title} aria-describedby={descriptionId} disabled={disabled} onClick={onChange}><span /></button></div>
}

export function LittleThings({ state, reminderState = state, onSaveSettings, onToggle, onCancel, reminderNotice, remindersLoading, onReloadReminders, notify }: Props) {
  const [busyKey, setBusyKey] = useState<CareKey | null>(null)
  const [activeTab, setActiveTab] = useState<'care' | 'reminders' | 'anniversary'>('reminders')
  const [activeFilter, setActiveFilter] = useState<'both' | 'self' | 'partner' | null>(null)
  const pendingReminders = reminderState.reminders.filter((reminder) => !reminder.completed && reminder.status !== 'cancelled')
  const pendingCount = pendingReminders.length
  const reminderStats = [
    { assignee: 'both', audience: '我们', className: 'things-stat-all' },
    { assignee: 'self', audience: '我', className: 'things-stat-self' },
    { assignee: 'partner', audience: '他', className: 'things-stat-partner' },
  ] as const
  const tabs = [{ id: 'reminders', label: '提醒' }, { id: 'anniversary', label: '纪念日' }, { id: 'care', label: '贴贴' }] as const
  const change = async (key: CareKey) => {
    if (busyKey) return
    setBusyKey(key)
    try {
      await onSaveSettings({ ...state.settings, [key]: !state.settings[key] })
      notify('提醒偏好已保存到此设备')
    } catch {
      notify('提醒偏好没保存成功，请再试一次。')
    } finally {
      setBusyKey(null)
    }
  }
  return <section className="tab-page things-page" aria-label="提醒">
      <header className="things-header">
        <div className="things-heading">
          <h1 className="sr-only">提醒</h1>
          <div className="things-tabs" role="tablist" aria-label="提醒分类">
        {tabs.map(({ id, label }, index) => <button type="button" role="tab" key={id} id={`things-${id}-tab`} aria-controls={`things-${id}-panel`} aria-selected={activeTab === id} tabIndex={activeTab === id ? 0 : -1} className={activeTab === id ? 'is-active' : ''} onClick={() => setActiveTab(id)} onKeyDown={(event) => {
          let next = index
          if (event.key === 'ArrowRight') next = (index + 1) % tabs.length
          else if (event.key === 'ArrowLeft') next = (index + tabs.length - 1) % tabs.length
          else if (event.key === 'Home') next = 0
          else if (event.key === 'End') next = tabs.length - 1
          else return
          event.preventDefault()
          setActiveTab(tabs[next].id)
          document.getElementById(`things-${tabs[next].id}-tab`)?.focus()
        }}>{label}</button>)}
          </div>
        </div>
      </header>
    <div className="tab-page-scroll things-scroll">
      <section role="tabpanel" id="things-reminders-panel" aria-labelledby="things-reminders-tab" className="things-tab-panel" hidden={activeTab !== 'reminders'}>
        <section className="things-hero" aria-label="提醒概览">
          <div className="things-hero-top">
            <div className="things-hero-copy">
              <h2 aria-label={`全部还有 ${pendingCount} 件待完成`}>
                <span className="things-hero-count-line"><span>全部还有</span><strong className="things-hero-count">{pendingCount}</strong><span className="things-hero-count-unit">件</span></span>
                <span className="things-hero-title">待完成</span>
              </h2>
            </div>
            <div className="things-buddy-scene" aria-hidden="true">
              <SpaceBuddy variant="ice" className="things-buddy" />
              <svg className="things-buddy-decor" viewBox="0 0 144 144" fill="none" focusable="false">
                <path d="M124 10V26M116 18H132" stroke="#93BCF0" strokeWidth="2.5" strokeLinecap="round" />
                <path d="M8 14V22M4 18H12" stroke="#EAB7CB" strokeWidth="1.8" strokeLinecap="round" />
                <path d="M14 115L17 122L24 125L17 128L14 135L11 128L4 125L11 122Z" fill="#F1D58D" fillOpacity=".8" />
              </svg>
            </div>
          </div>
        </section>
        <div className="things-stats" role="group" aria-label="按提醒对象筛选">
          {reminderStats.map(({ assignee, audience, className }) => {
            const count = pendingReminders.filter((reminder) => reminder.assignee === assignee).length
            return <button type="button" className={`things-stat ${className} ${activeFilter === assignee ? 'is-selected' : ''}`} key={assignee} aria-label={`@${audience}，${count}件待完成`} aria-pressed={activeFilter === assignee} aria-controls="things-pending-list" title={activeFilter === assignee ? '再次点击，显示全部待完成提醒' : `仅显示提醒${audience}的待完成事项`} onClick={() => setActiveFilter((current) => current === assignee ? null : assignee)}>
              <span className="things-stat-label">@<strong>{audience}</strong></span>
              <span className="things-stat-count"><strong>{count}</strong><span>件</span></span>
              <span className="things-stat-status">{activeFilter === assignee ? '筛选中' : '待完成'}</span>
            </button>
          })}
        </div>
        <div className="things-section-heading"><h2><span className="reminder-title-lettering">待完成</span></h2></div>
        {reminderNotice && <div className="cloud-error" role="alert"><span>{reminderNotice}</span>{onReloadReminders && <button disabled={remindersLoading} onClick={() => void onReloadReminders()}>刷新</button>}</div>}
        {remindersLoading && !reminderState.reminders.length ? <p className="empty-note" role="status">正在同步共享提醒…</p> : <ReminderBoard state={reminderState} pendingAssignee={activeFilter} onToggle={onToggle} onCancel={onCancel} notify={notify} />}
      </section>
      <section role="tabpanel" id="things-anniversary-panel" aria-labelledby="things-anniversary-tab" className="things-tab-panel" hidden={activeTab !== 'anniversary'}>
        <Anniversary state={state} />
      </section>
      <section role="tabpanel" id="things-care-panel" aria-labelledby="things-care-tab" className="things-tab-panel" hidden={activeTab !== 'care'}>
        <div className="form-card settings-card" aria-label="提醒偏好">
          <CareRow icon={<CloudSun size={19} />} title="天气关怀" description="希望天气变化时收到关心提示" checked={state.settings.weatherCare} disabled={busyKey !== null} onChange={() => void change('weatherCare')} />
          <CareRow icon={<Cake size={18} />} title="纪念日提醒" description="希望提前 3 天收到纪念日提示" checked={state.settings.anniversaryReminders} disabled={busyKey !== null} onChange={() => void change('anniversaryReminders')} />
          <CareRow icon={<Moon size={18} />} title="安静模式" description="希望在 23:00–08:00 暂停打扰" checked={state.settings.quietHours} disabled={busyKey !== null} onChange={() => void change('quietHours')} />
        </div>
      </section>
    </div>
  </section>
}
