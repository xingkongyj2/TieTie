import { ChevronDown, Clock3, Plus } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import type { RelationshipState, Reminder } from '../types';
import { Anniversaries } from './Anniversaries';
import type { AnniversaryState } from '../hooks/useAnniversaries';
import { ReminderCard } from './ReminderCard';
import { Sheet } from './Sheet';
import { DatePicker } from './DatePicker';
import { TimePicker } from './TimePicker';
import { compareReminderTime, reminderPhase } from '../lib/reminders';

export type ToolName = 'reminders' | 'anniversary';
interface Props { tool: ToolName; state: RelationshipState; anniversaries: AnniversaryState; onClose: () => void; onAdd: (input: Omit<Reminder, 'id' | 'completed'>) => Promise<void>; notify: (text: string) => void }
interface ReminderBoardProps { state: RelationshipState; onToggle: (id: string) => Promise<void>; onCancel?: (id: string) => Promise<void>; notify: (text: string) => void; pendingAssignee?: 'both' | 'self' | 'partner' | null }

function finishedTime(reminder: Reminder): number {
  const value = reminder.taskCompletedAt || reminder.deliveredAt || reminder.updatedAt || reminder.time;
  const time = Date.parse(value ?? '');
  return Number.isNaN(time) ? Number.NEGATIVE_INFINITY : time;
}

export function ReminderBoard({ state, onToggle, onCancel, notify, pendingAssignee = null }: ReminderBoardProps) {
  const pending = state.reminders.filter((reminder) => reminderPhase(reminder) === 'pending' && (pendingAssignee === null || reminder.assignee === pendingAssignee)).sort(compareReminderTime);
  const completed = state.reminders.filter((reminder) => reminderPhase(reminder) !== 'pending').sort((a, b) => {
    const left = finishedTime(a), right = finishedTime(b);
    return left === right ? a.id.localeCompare(b.id) : left > right ? -1 : 1;
  });
  const completedDays: { key: string; label: string; reminders: Reminder[] }[] = [];
  for (const reminder of completed) {
    const time = finishedTime(reminder);
    const date = Number.isFinite(time) ? new Date(time) : null;
    const key = date ? `${date.getFullYear()}-${date.getMonth() + 1}-${date.getDate()}` : 'unknown';
    let day = completedDays.find((item) => item.key === key);
    if (!day) {
      day = { key, label: date ? date.toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' }) : '日期未记录', reminders: [] };
      completedDays.push(day);
    }
    day.reminders.push(reminder);
  }
  const emptyNote = pendingAssignee === null
    ? completed.length ? '待完成的提醒都处理好啦。' : '还没有提醒，先添加一条吧。'
    : `暂时没有提醒${{ both: '我们', self: '我', partner: 'TA' }[pendingAssignee]}的待完成事项。`;
  return <div className="reminder-board">
    <section id="things-pending-list" aria-label="待完成" aria-live="polite">
      <div className="reminders-list">{pending.length ? pending.map((reminder) => <ReminderCard reminder={reminder} key={reminder.id} members={state.members} onToggle={onToggle} onCancel={onCancel} onError={notify} />) : <p className="empty-note">{emptyNote}</p>}</div>
    </section>
    <section aria-label="已完成">
      <div className="reminder-completed-divider"><h2><span className="reminder-title-lettering">已完成</span></h2></div>
      {completedDays.length ? completedDays.map((day) => <div className="reminder-day-group" key={day.key}>
        <h3 className="reminder-day-heading"><span className="reminder-day-date">{day.label}</span><span className="reminder-day-count">{day.reminders.length} 条</span></h3>
        <div className="reminders-list">{day.reminders.map((reminder) => <ReminderCard reminder={reminder} key={reminder.id} members={state.members} onToggle={onToggle} onError={notify} />)}</div>
      </div>) : <p className="empty-note">还没有已完成的提醒。</p>}
    </section>
  </div>;
}

function ReminderCompose({ state, onAdd, onClose, notify, onPickTime }: Pick<Props, 'state' | 'onAdd' | 'onClose' | 'notify'> & { onPickTime: (value: string, onConfirm: (time: string) => void) => void }) {
  const [content, setContent] = useState('');
  const [assignee, setAssignee] = useState<'both' | 'self' | 'partner'>('both');
  const [time, setTime] = useState('');
  const [date, setDate] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const title = content.trim();
    if (!title || busy) return;
    const isoDate = date.replaceAll('.', '-');
    const due = new Date(`${isoDate}T${time}:00`);
    const localDate = Number.isNaN(due.getTime()) ? '' : `${String(due.getFullYear()).padStart(4, '0')}-${String(due.getMonth() + 1).padStart(2, '0')}-${String(due.getDate()).padStart(2, '0')}`;
    if (!/^\d{4}-\d{2}-\d{2}$/.test(isoDate) || !/^\d{2}:\d{2}$/.test(time) || localDate !== isoDate || due.getHours() !== Number(time.slice(0, 2)) || due.getMinutes() !== Number(time.slice(3, 5)) || due.getTime() <= Date.now()) { setError('请选择一个有效且未来的提醒时间。'); return; }
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
    <div className="reminder-time-field"><span>提醒时间</span><div className="reminder-date-time">
      <div><label className="sr-only" htmlFor="quick-reminder-date">提醒日期</label><DatePicker id="quick-reminder-date" title="选择提醒日期" value={date} allowFuture disabled={busy} onChange={(value) => { setDate(value); setError(''); }} /></div>
      <button type="button" className="reminder-clock" disabled={busy} aria-label={time ? `提醒时刻 ${time}` : '选择提醒时刻'} aria-haspopup="dialog" onClick={() => {
        const next = new Date(Date.now() + 60 * 60_000);
        const initial = time || `${String(next.getHours()).padStart(2, '0')}:${String(next.getMinutes()).padStart(2, '0')}`;
        onPickTime(initial, (value) => { setTime(value); setError(''); });
      }}><Clock3 size={15} aria-hidden="true" /><span>{time || '选择时间'}</span></button>
    </div><small>按设备当前时区设置，到点会在共享聊天里提醒所选的人。</small></div>
    {error && <p className="form-error" role="alert">{error}</p>}
    <button className="primary-button" type="submit" disabled={busy || !content.trim() || !date || !time}>{busy ? '正在添加…' : '添加提醒'}<Plus size={17} /></button>
  </form>;
}

export function Tools(props: Props) {
  const [timeEdit, setTimeEdit] = useState<{ value: string; onConfirm: (time: string) => void } | null>(null);
  const { tool, onClose } = props;
  const labels = { reminders: '添加提醒', anniversary: '我们的小纪念' };
  return <><Sheet title={labels[tool]} onClose={onClose}>{(close) => tool === 'reminders' ? <ReminderCompose state={props.state} onAdd={props.onAdd} onClose={close} notify={props.notify} onPickTime={(value, onConfirm) => setTimeEdit({ value, onConfirm })} /> : <Anniversaries state={props.anniversaries} compact notify={props.notify} />}</Sheet>{timeEdit && <TimePicker title="提醒时刻" value={timeEdit.value} onClose={() => setTimeEdit(null)} onConfirm={async (value) => { timeEdit.onConfirm(value); return true; }} />}</>;
}
