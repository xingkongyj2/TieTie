import { ChevronDown, Plus } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import type { RelationshipState, Reminder } from '../types';
import { Anniversaries } from './Anniversaries';
import type { AnniversaryState } from '../hooks/useAnniversaries';
import { ReminderCard } from './ReminderCard';
import { Sheet } from './Sheet';
import { compareReminderTime, reminderPhase } from '../lib/reminders';

export type ToolName = 'reminders' | 'anniversary';
interface Props { tool: ToolName; state: RelationshipState; anniversaries: AnniversaryState; onClose: () => void; onAdd: (input: Omit<Reminder, 'id' | 'completed'>) => Promise<void>; notify: (text: string) => void }
interface ReminderBoardProps { state: RelationshipState; onToggle: (id: string) => Promise<void>; onCancel?: (id: string) => Promise<void>; notify: (text: string) => void; pendingAssignee?: 'both' | 'self' | 'partner' | null }

export function ReminderBoard({ state, onToggle, onCancel, notify, pendingAssignee = null }: ReminderBoardProps) {
  const pending = state.reminders.filter((reminder) => reminderPhase(reminder) === 'pending' && (pendingAssignee === null || reminder.assignee === pendingAssignee)).sort(compareReminderTime);
  const completed = state.reminders.filter((reminder) => reminderPhase(reminder) !== 'pending').sort(compareReminderTime);
  const emptyNote = pendingAssignee === null
    ? completed.length ? '待完成的提醒都处理好啦。' : '还没有提醒，先添加一条吧。'
    : `暂时没有提醒${{ both: '我们', self: '我', partner: 'TA' }[pendingAssignee]}的待完成事项。`;
  return <div className="reminder-board">
    <section id="things-pending-list" aria-label="待完成" aria-live="polite">
      <div className="reminders-list">{pending.length ? pending.map((reminder) => <ReminderCard reminder={reminder} key={reminder.id} members={state.members} onToggle={onToggle} onCancel={onCancel} onError={notify} />) : <p className="empty-note">{emptyNote}</p>}</div>
    </section>
    <section aria-label="已完成">
      <div className="reminder-completed-divider"><h2><span className="reminder-title-lettering">已完成</span></h2></div>
      <div className="reminders-list">{completed.length ? completed.map((reminder) => <ReminderCard reminder={reminder} key={reminder.id} members={state.members} onToggle={onToggle} onError={notify} />) : <p className="empty-note">还没有已完成的提醒。</p>}</div>
    </section>
  </div>;
}

function ReminderCompose({ state, onAdd, onClose, notify }: Pick<Props, 'state' | 'onAdd' | 'onClose' | 'notify'>) {
  const [content, setContent] = useState('');
  const [assignee, setAssignee] = useState<'both' | 'self' | 'partner'>('both');
  const [time, setTime] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const title = content.trim();
    if (!title || busy) return;
    const due = new Date(time);
    if (!time || Number.isNaN(due.getTime()) || due.getTime() <= Date.now()) { setError('请选择一个未来的提醒时间。'); return; }
    setBusy(true);
    setError('');
    try { await onAdd({ title, assignee, time: due.toISOString() }); notify('共享提醒已安排，到点会在聊天里提醒'); onClose(); }
    catch (error) { setError(error instanceof Error ? error.message : '提醒添加失败，请再试一次。'); }
    finally { setBusy(false); }
  };
  return <form className="reminder-compose" onSubmit={(event) => void submit(event)}>
    <div className="reminder-editor">
      <label className="sr-only" htmlFor="quick-reminder-content">提醒内容</label>
      <textarea id="quick-reminder-content" rows={5} maxLength={500} placeholder="写下提醒内容…" value={content} disabled={busy} onChange={(event) => { setContent(event.target.value); setError(''); }} />
      <div className="reminder-editor-footer">
        <label className="reminder-mention"><span className="sr-only">提醒对象</span><select value={assignee} disabled={busy} onChange={(event) => setAssignee(event.target.value as typeof assignee)}><option value="both">@我们两人</option><option value="self">@我</option><option value="partner">@{state.members.find((member) => member.id === 'partner')?.name || '另一位成员'}</option></select><ChevronDown size={14} aria-hidden="true" /></label>
      </div>
    </div>
    <label className="reminder-time-field" htmlFor="quick-reminder-time"><span>提醒时间</span><input id="quick-reminder-time" type="datetime-local" required value={time} disabled={busy} onChange={(event) => { setTime(event.target.value); setError(''); }} /><small>按设备当前时区设置，到点会在共享聊天里提醒所选的人。</small></label>
    {error && <p className="form-error" role="alert">{error}</p>}
    <button className="primary-button" type="submit" disabled={busy || !content.trim() || !time}>{busy ? '正在添加…' : '添加提醒'}<Plus size={17} /></button>
  </form>;
}

export function Tools(props: Props) {
  const { tool, onClose } = props;
  const labels = { reminders: '添加提醒', anniversary: '我们的小纪念' };
  return <Sheet title={labels[tool]} onClose={onClose}>{(close) => tool === 'reminders' ? <ReminderCompose state={props.state} onAdd={props.onAdd} onClose={close} notify={props.notify} /> : <Anniversaries state={props.anniversaries} compact notify={props.notify} />}</Sheet>;
}
