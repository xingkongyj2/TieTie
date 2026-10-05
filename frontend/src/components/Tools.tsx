import { Input, Textarea, Select, Form, SubmitButton } from './Fields';
import { ChevronDown, Clock3, Plus, Repeat2, X } from './Icons';
import { useRef, useState, type FormEvent } from 'react';
import type { RelationshipState, Reminder, ReminderRecurrence } from '../types';
import { Anniversaries } from './Anniversaries';
import type { AnniversaryState } from '../hooks/useAnniversaries';
import { ReminderCard } from './ReminderCard';
import { Sheet } from './Sheet';
import { DatePicker } from './DatePicker';
import { TimePicker } from './TimePicker';
import { compareReminderTime, reminderPhase } from '../lib/reminders';

export type ToolName = 'reminders' | 'anniversary';
interface Props { tool: ToolName; state: RelationshipState; anniversaries: AnniversaryState; onClose: () => void; onAdd: (input: Omit<Reminder, 'id' | 'completed'>) => Promise<void>; notify: (text: string) => void }
interface ReminderBoardProps { state: RelationshipState; onToggle: (id: string) => Promise<void>; onCancel?: (id: string) => Promise<void>; onDelete?: (id: string) => Promise<void>; notify: (text: string) => void; pendingAssignee?: 'both' | 'self' | 'partner' | null }

type RepeatMode = 'none' | ReminderRecurrence['type'];
const weekdayOptions = ['周一', '周二', '周三', '周四', '周五', '周六', '周日'];
const shanghaiOffsetMs = 8 * 60 * 60_000;

function dateInShanghai(date: Date): string {
  return new Date(date.getTime() + shanghaiOffsetMs).toISOString().slice(0, 10);
}

function shanghaiDateTime(day: string, time: string): Date {
  return new Date(`${day}T${time}:00+08:00`);
}

function isoWeekday(day: string): number {
  return new Date(`${day}T12:00:00Z`).getUTCDay() || 7;
}

function addCalendarDays(day: string, count: number): string {
  const date = new Date(`${day}T00:00:00Z`);
  date.setUTCDate(date.getUTCDate() + count);
  return date.toISOString().slice(0, 10);
}

function finishedTime(reminder: Reminder): number {
  const value = reminder.taskCompletedAt || reminder.deliveredAt || reminder.updatedAt || reminder.time;
  const time = Date.parse(value ?? '');
  return Number.isNaN(time) ? Number.NEGATIVE_INFINITY : time;
}

export function ReminderBoard({ state, onToggle, onCancel, onDelete, notify, pendingAssignee = null }: ReminderBoardProps) {
  const pending = state.reminders.filter((reminder) => reminderPhase(reminder) === 'pending' && (pendingAssignee === null || reminder.assignee === pendingAssignee)).sort(compareReminderTime);
  const completed = state.reminders.filter((reminder) => reminderPhase(reminder) !== 'pending').sort((a, b) => {
    const left = finishedTime(a), right = finishedTime(b);
    return left === right ? a.id.localeCompare(b.id) : left > right ? -1 : 1;
  });
  const completedDays: { key: string; label: string; reminders: Reminder[] }[] = [];
  for (const reminder of completed) {
    const time = finishedTime(reminder);
    const date = Number.isFinite(time) ? new Date(time + shanghaiOffsetMs) : null;
    const key = date ? `${date.getUTCFullYear()}-${date.getUTCMonth() + 1}-${date.getUTCDate()}` : 'unknown';
    let day = completedDays.find((item) => item.key === key);
    if (!day) {
      day = { key, label: date ? `${date.getUTCFullYear()}年${date.getUTCMonth() + 1}月${date.getUTCDate()}日` : '日期未记录', reminders: [] };
      completedDays.push(day);
    }
    day.reminders.push(reminder);
  }
  const emptyNote = pendingAssignee === null
    ? completed.length ? '待完成的提醒都处理好啦。' : '还没有提醒，先添加一条吧。'
    : `暂时没有提醒${{ both: '我们', self: '我', partner: 'TA' }[pendingAssignee]}的待完成事项。`;
  return <div className="reminder-board">
    <section id="things-pending-list" aria-label="待完成" aria-live="polite">
      <div className="reminders-list">{pending.length ? pending.map((reminder) => <ReminderCard reminder={reminder} key={reminder.id} members={state.members} onToggle={onToggle} onCancel={onCancel} onDelete={onDelete} onError={notify} />) : <p className="empty-note">{emptyNote}</p>}</div>
    </section>
    <section aria-label="已完成">
      <div className="reminder-completed-divider"><h2><span className="reminder-title-lettering">已完成</span></h2></div>
      {completedDays.length ? completedDays.map((day) => <div className="reminder-day-group" key={day.key}>
        <h3 className="reminder-day-heading"><span className="reminder-day-date">{day.label}</span><span className="reminder-day-count">{day.reminders.length} 条</span></h3>
        <div className="reminders-list">{day.reminders.map((reminder) => <ReminderCard reminder={reminder} key={reminder.id} members={state.members} onToggle={onToggle} onDelete={onDelete} onError={notify} />)}</div>
      </div>) : <p className="empty-note">还没有已完成的提醒。</p>}
    </section>
  </div>;
}

function ReminderCompose({ state, onAdd, onClose, notify, onPickTime }: Pick<Props, 'state' | 'onAdd' | 'onClose' | 'notify'> & { onPickTime: (value: string, onConfirm: (time: string) => void) => void }) {
  const [content, setContent] = useState('');
  const [assignee, setAssignee] = useState<'both' | 'self' | 'partner'>('both');
  const [time, setTime] = useState('');
  const [date, setDate] = useState('');
  const [repeatMode, setRepeatMode] = useState<RepeatMode>('none');
  const [intervalDays, setIntervalDays] = useState(2);
  const [weekdays, setWeekdays] = useState<number[]>([]);
  const [selectedDates, setSelectedDates] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const submitting = useRef(false);
  const [error, setError] = useState('');
  const chooseRepeatMode = (mode: RepeatMode) => {
    setRepeatMode(mode);
    if (mode === 'weekdays' && !weekdays.length && date) setWeekdays([isoWeekday(date.replaceAll('.', '-'))]);
    if (mode === 'dates' && date && !selectedDates.length) setSelectedDates([date.replaceAll('.', '-')]);
    setError('');
  };
  const chooseDate = (value: string) => {
    if (repeatMode === 'dates' && value) {
      const day = value.replaceAll('.', '-');
      if (!selectedDates.includes(day) && selectedDates.length >= 100) { setError('最多指定 100 个日期。'); return; }
      setSelectedDates((current) => current.includes(day) ? current : [...current, day].sort());
    }
    setDate(value);
    setError('');
  };
  const toggleWeekday = (day: number) => {
    setWeekdays((current) => current.includes(day) ? current.filter((item) => item !== day) : [...current, day].sort((a, b) => a - b));
    setError('');
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const title = content.trim();
    if (!title || submitting.current) return;
    const isoDate = date.replaceAll('.', '-');
    if (!/^\d{2}:\d{2}$/.test(time)) { setError('请选择提醒时刻。'); return; }
    if (repeatMode === 'interval' && (!Number.isInteger(intervalDays) || intervalDays < 1 || intervalDays > 3650)) { setError('间隔天数请选择 1 到 3650 天。'); return; }
    if (repeatMode === 'weekdays' && !weekdays.length) { setError('请至少选择一个星期。'); return; }
    if (repeatMode === 'dates' && !selectedDates.length) { setError('请至少添加一个指定日期。'); return; }
    if (repeatMode !== 'dates' && !/^\d{4}-\d{2}-\d{2}$/.test(isoDate)) { setError('请选择提醒日期。'); return; }
    let firstDay = repeatMode === 'dates' ? selectedDates[0] : isoDate;
    if (repeatMode === 'weekdays') {
      const today = dateInShanghai(new Date());
      const start = isoDate > today ? isoDate : today;
      for (let offset = 0; offset <= 7; offset++) {
        const candidate = addCalendarDays(start, offset);
        if (weekdays.includes(isoWeekday(candidate)) && shanghaiDateTime(candidate, time).getTime() > Date.now()) { firstDay = candidate; break; }
      }
    }
    if (repeatMode === 'dates' && selectedDates.some((day) => shanghaiDateTime(day, time).getTime() <= Date.now())) { setError('有指定日期已经过去，请调整或移除。'); return; }
    const due = shanghaiDateTime(firstDay, time);
    if (Number.isNaN(due.getTime()) || dateInShanghai(due) !== firstDay || due.getTime() <= Date.now()) { setError('请选择一个有效且未来的提醒时间。'); return; }
    const recurrence: ReminderRecurrence | undefined = repeatMode === 'none' ? undefined
      : repeatMode === 'interval' ? { type: 'interval', intervalDays }
        : repeatMode === 'weekdays' ? { type: 'weekdays', weekdays }
          : repeatMode === 'dates' ? { type: 'dates', dates: selectedDates }
            : { type: repeatMode };
    submitting.current = true;
    setBusy(true);
    setError('');
    try { await onAdd({ title, assignee, time: due.toISOString(), recurrence }); notify(recurrence ? '重复提醒已安排，到点会在聊天里提醒' : '共享提醒已安排，到点会在聊天里提醒'); onClose(); }
    catch (error) { setError(error instanceof Error ? error.message : '提醒添加失败，请再试一次。'); }
    finally { submitting.current = false; setBusy(false); }
  };
  return <Form className="reminder-compose" onSubmit={(event) => void submit(event)}>
    <div className="reminder-editor">
      <label className="sr-only" htmlFor="quick-reminder-content">提醒内容</label>
      <Textarea id="quick-reminder-content" rows={5} maxLength={500} placeholder="写下提醒内容…" value={content} disabled={busy} onChange={(event) => { setContent(event.target.value); setError(''); }} />
      <div className="reminder-editor-footer">
        <label className="reminder-mention"><span className="sr-only">提醒对象</span><Select value={assignee} disabled={busy} onChange={(event) => setAssignee(event.target.value as typeof assignee)}><option value="both">@我们两人</option><option value="self">@我</option><option value="partner">@{state.members.find((member) => member.id === 'partner')?.name || '另一位成员'}</option></Select><ChevronDown size={14} aria-hidden="true" /></label>
      </div>
    </div>
    <div className="reminder-time-field"><span>{repeatMode === 'dates' ? '指定日期与时间' : '提醒时间'}</span><div className="reminder-date-time">
      <div><label className="sr-only" htmlFor="quick-reminder-date">{repeatMode === 'dates' ? '添加指定日期' : repeatMode === 'weekdays' ? '开始日期' : '提醒日期'}</label><DatePicker id="quick-reminder-date" title={repeatMode === 'dates' ? '添加指定日期' : repeatMode === 'weekdays' ? '选择开始日期' : '选择提醒日期'} placeholder={repeatMode === 'dates' ? '点这里添加日期' : 'YYYY.MM.DD'} value={date} allowFuture disabled={busy} onChange={chooseDate} /></div>
      <button type="button" className="reminder-clock" disabled={busy} aria-label={time ? `提醒时刻 ${time}` : '选择提醒时刻'} aria-haspopup="dialog" onClick={() => {
        const initial = time || new Date(Date.now() + 60 * 60_000 + shanghaiOffsetMs).toISOString().slice(11, 16);
        onPickTime(initial, (value) => { setTime(value); setError(''); });
      }}><Clock3 size={15} aria-hidden="true" /><span>{time || '选择时间'}</span></button>
    </div><small>按北京时间设置，到点会在共享聊天里提醒所选的人。</small></div>
    <div className="reminder-repeat-field">
      <label htmlFor="quick-reminder-repeat"><Repeat2 size={15} aria-hidden="true" />重复提醒</label>
      <div className="reminder-repeat-select"><Select id="quick-reminder-repeat" value={repeatMode} disabled={busy} onChange={(event) => chooseRepeatMode(event.target.value as RepeatMode)}>
        <option value="none">不重复</option><option value="daily">每天</option><option value="interval">每隔几天</option><option value="weekly">每周</option><option value="monthly">每月</option><option value="yearly">每年</option><option value="weekdays">指定每周星期</option><option value="dates">指定几个日期</option>
      </Select><ChevronDown size={15} aria-hidden="true" /></div>
      {repeatMode === 'interval' && <label className="reminder-interval">每隔 <Input type="number" min={1} max={3650} step={1} value={intervalDays} disabled={busy} onChange={(event) => { setIntervalDays(Number(event.target.value)); setError(''); }} /> 天提醒一次</label>}
      {repeatMode === 'weekdays' && <div className="reminder-weekday-options" role="group" aria-label="指定每周星期">{weekdayOptions.map((label, index) => <button type="button" key={label} aria-pressed={weekdays.includes(index + 1)} className={weekdays.includes(index + 1) ? 'is-selected' : ''} disabled={busy} onClick={() => toggleWeekday(index + 1)}>{label}</button>)}</div>}
      {repeatMode === 'dates' && <div className="reminder-date-options" aria-label="已选指定日期">{selectedDates.length ? selectedDates.map((day) => <span className="reminder-date-chip" key={day}>{day.replaceAll('-', '.')}<button type="button" disabled={busy} aria-label={`移除 ${day}`} onClick={() => { const remaining = selectedDates.filter((item) => item !== day); setSelectedDates(remaining); if (date.replaceAll('.', '-') === day) setDate(remaining.at(-1)?.replaceAll('-', '.') ?? ''); setError(''); }}><X size={13} aria-hidden="true" /></button></span>) : <small>从上方日历添加日期，可选多个。</small>}</div>}
      {repeatMode === 'weekdays' && <small>从开始日期起，按选中的星期提醒。</small>}
      {repeatMode === 'dates' && selectedDates.length > 0 && <small>点上方日期可继续添加；每个日期都会在选定时间提醒。</small>}
    </div>
    {error && <p className="form-error" role="alert">{error}</p>}
    <SubmitButton className="primary-button"  disabled={busy || !content.trim() || !time || (repeatMode === 'dates' ? !selectedDates.length : !date)}>{busy ? '正在添加…' : '添加提醒'}<Plus size={17} /></SubmitButton>
  </Form>;
}

export function Tools(props: Props) {
  const [timeEdit, setTimeEdit] = useState<{ value: string; onConfirm: (time: string) => void } | null>(null);
  const { tool, onClose } = props;
  const labels = { reminders: '添加提醒', anniversary: '我们的小纪念' };
  return <><Sheet title={labels[tool]} onClose={onClose}>{(close) => tool === 'reminders' ? <ReminderCompose state={props.state} onAdd={props.onAdd} onClose={close} notify={props.notify} onPickTime={(value, onConfirm) => setTimeEdit({ value, onConfirm })} /> : <Anniversaries state={props.anniversaries} compact notify={props.notify} />}</Sheet>{timeEdit && <TimePicker title="提醒时刻" value={timeEdit.value} onClose={() => setTimeEdit(null)} onConfirm={async (value) => { timeEdit.onConfirm(value); return true; }} />}</>;
}
