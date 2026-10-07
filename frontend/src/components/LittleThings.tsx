import { Image, ScrollView } from '@tarojs/components'
import { useState } from 'react'
import type { RelationshipState } from '../types'
import { ReminderBoard } from './Tools'
import { Anniversaries } from './Anniversaries'
import type { AnniversaryState } from '../hooks/useAnniversaries'
import { reminderPhase } from '../lib/reminders'
import { Countdowns } from './Countdowns'
import { CareWithAnniversary } from './CareModes'
import { EmptyTabState } from './EmptyTabState'
import './LittleThings.css'

interface Props {
  sessionId?: string
  selfId?: number
  onEditRegion: () => void
  anniversaries: AnniversaryState
  state: RelationshipState
  reminderState?: RelationshipState
  onToggle: (id: string) => Promise<void>
  onCancel?: (id: string) => Promise<void>
  onDelete?: (id: string) => Promise<void>
  reminderNotice?: string
  remindersLoading?: boolean
  onReloadReminders?: () => Promise<void>
  notify: (text: string) => void
}

export function LittleThings({ sessionId, selfId, onEditRegion, state, anniversaries, reminderState = state, onToggle, onCancel, onDelete, reminderNotice, remindersLoading, onReloadReminders, notify }: Props) {
  const [activeTab, setActiveTab] = useState<'care' | 'reminders' | 'anniversary' | 'countdown'>('reminders')
  const [activeFilter, setActiveFilter] = useState<'both' | 'self' | 'partner' | null>(null)
  const [countdownPlaceholder, setCountdownPlaceholder] = useState(true)
  const [carePlaceholder, setCarePlaceholder] = useState(true)
  const remindersPlaceholder = !sessionId || !reminderState.reminders.length && !remindersLoading && !reminderNotice
  const anniversaryPlaceholder = !sessionId || !anniversaries.anniversaries.length && !anniversaries.featured && (!anniversaries.spaceCreatedAt || Number.isNaN(Date.parse(anniversaries.spaceCreatedAt))) && !anniversaries.error
  const placeholder = activeTab === 'reminders' ? remindersPlaceholder : activeTab === 'countdown' ? countdownPlaceholder : activeTab === 'care' ? carePlaceholder : anniversaryPlaceholder
  const pendingReminders = reminderState.reminders.filter((reminder) => reminderPhase(reminder) === 'pending')
  const pendingCount = pendingReminders.length
  const reminderStats = [
    { assignee: 'both', audience: '我们', className: 'things-stat-all' },
    { assignee: 'self', audience: '我', className: 'things-stat-self' },
    { assignee: 'partner', audience: 'TA', className: 'things-stat-partner' },
  ] as const
  const tabs = [{ id: 'reminders', label: '待办' }, { id: 'countdown', label: '倒计时' }, { id: 'anniversary', label: '纪念日' }, { id: 'care', label: '贴贴' }] as const
  return <section className="tab-page things-page" aria-label="待办">
      <header className="things-header">
        <div className="things-heading">
          <h1 className="sr-only">待办</h1>
          <div className="things-tabs" role="tablist" aria-label="待办分类">
        {tabs.map(({ id, label }, index) => <button type="button" role="tab" key={id} id={`things-${id}-tab`} aria-controls={`things-${id}-panel`} aria-selected={activeTab === id} tabIndex={activeTab === id ? 0 : -1} className={activeTab === id ? 'is-active' : ''} onClick={() => setActiveTab(id)} onKeyDown={(event) => {
          let next = index
          if (event.key === 'ArrowRight') next = (index + 1) % tabs.length
          else if (event.key === 'ArrowLeft') next = (index + tabs.length - 1) % tabs.length
          else if (event.key === 'Home') next = 0
          else if (event.key === 'End') next = tabs.length - 1
          else return
          event.preventDefault()
          setActiveTab(tabs[next].id)
        }}><span className="things-tab-label">{label}</span></button>)}
          </div>
        </div>
      </header>
    <ScrollView scrollY enhanced showScrollbar={false} className={`tab-page-scroll things-scroll${placeholder ? ' is-placeholder' : ''}`}>
      <div className="things-scroll-content">
      <section role="tabpanel" id="things-reminders-panel" aria-labelledby="things-reminders-tab" className={`things-tab-panel${activeTab === 'reminders' ? ' is-active' : ''}${remindersPlaceholder ? ' is-placeholder' : ''}`} hidden={activeTab !== 'reminders'} style={{ display: activeTab === 'reminders' ? 'flex' : 'none', flexDirection: 'column', flex: 1 }}>
        {remindersPlaceholder ? <EmptyTabState kind="reminders" title="还没有待办" example="明天早上九点提醒我们出门" /> : <>
        <section className="things-hero" aria-label="待办概览">
          <div className="things-hero-top">
            <div className="things-hero-copy">
              <h2 aria-label={`全部还有 ${pendingCount} 件待完成`}>
                <span className="things-hero-count-line"><span>全部还有</span><strong className="things-hero-count">{pendingCount}</strong><span className="things-hero-count-unit">件</span></span>
                <span className="things-hero-title">待完成</span>
              </h2>
            </div>
            <div className="things-buddy-scene" aria-hidden="true">
              <Image className="h5-img things-buddy" src="/assets/ip/paper-buddies-v2/todo-duo.png" mode="aspectFit" />
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
        {remindersLoading && !reminderState.reminders.length ? <p className="empty-note" role="status">正在同步共享提醒…</p> : <ReminderBoard state={reminderState} pendingAssignee={activeFilter} onToggle={onToggle} onCancel={onCancel} onDelete={onDelete} notify={notify} />}
        </>}
      </section>
      <section role="tabpanel" id="things-countdown-panel" aria-labelledby="things-countdown-tab" className={`things-tab-panel${activeTab === 'countdown' ? ' is-active' : ''}${countdownPlaceholder ? ' is-placeholder' : ''}`} hidden={activeTab !== 'countdown'} style={{ display: activeTab === 'countdown' ? 'flex' : 'none', flexDirection: 'column', flex: 1 }}>{activeTab === 'countdown' && <Countdowns sessionId={sessionId} onPlaceholderChange={setCountdownPlaceholder} />}</section>
      <section role="tabpanel" id="things-anniversary-panel" aria-labelledby="things-anniversary-tab" className={`things-tab-panel${activeTab === 'anniversary' ? ' is-active' : ''}${anniversaryPlaceholder ? ' is-placeholder' : ''}`} hidden={activeTab !== 'anniversary'} style={{ display: activeTab === 'anniversary' ? 'flex' : 'none', flexDirection: 'column', flex: 1 }}>
        {sessionId ? <Anniversaries state={anniversaries} notify={notify} /> : activeTab === 'anniversary' && <EmptyTabState kind="anniversary" title="还没有纪念日" example="我们是 2025 年 5 月 20 日在一起的" />}
      </section>
      <section role="tabpanel" id="things-care-panel" aria-labelledby="things-care-tab" className={`things-tab-panel${activeTab === 'care' ? ' is-active' : ''}${carePlaceholder ? ' is-placeholder' : ''}`} hidden={activeTab !== 'care'} style={{ display: activeTab === 'care' ? 'flex' : 'none', flexDirection: 'column', flex: 1 }}>
        {activeTab === 'care' && <CareWithAnniversary key={sessionId ?? 'unbound'} sessionId={sessionId} selfId={selfId} onEditRegion={onEditRegion} notify={notify} onPlaceholderChange={setCarePlaceholder} />}
      </section>
      </div>
    </ScrollView>
  </section>
}
