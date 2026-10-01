import { CalendarHeart, Check, ChevronDown, Heart, Plus } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import type { RelationshipState, Reminder } from '../types';
import { daysTogether } from '../lib/format';
import { Avatar } from './Avatar';
import { ReminderCard } from './ReminderCard';
import { Sheet } from './Sheet';

export type ToolName = 'reminders' | 'anniversary';
interface Props { tool: ToolName; state: RelationshipState; onClose: () => void; onAdd: (input: Omit<Reminder, 'id' | 'completed'>) => Promise<void>; notify: (text: string) => void }
interface ReminderBoardProps { state: RelationshipState; onToggle: (id: string) => Promise<void>; notify: (text: string) => void; pendingAssignee?: 'both' | 'self' | 'partner' | null }

export function ReminderBoard({ state, onToggle, notify, pendingAssignee = null }: ReminderBoardProps) {
  const pending = state.reminders.filter((reminder) => !reminder.completed && (pendingAssignee === null || reminder.assignee === pendingAssignee));
  const completed = state.reminders.filter((reminder) => reminder.completed);
  const emptyNote = pendingAssignee === null
    ? completed.length ? '待完成的提醒都处理好啦。' : '还没有提醒，先添加一条吧。'
    : `暂时没有提醒${{ both: '我们', self: '我', partner: '他' }[pendingAssignee]}的待完成事项。`;
  return <div className="reminder-board">
    <section id="things-pending-list" aria-label="待完成" aria-live="polite">
      <div className="reminders-list">{pending.length ? pending.map((reminder) => <ReminderCard reminder={reminder} key={reminder.id} members={state.members} onToggle={onToggle} onError={notify} />) : <p className="empty-note">{emptyNote}</p>}</div>
    </section>
    <section aria-label="已完成">
      <div className="reminder-completed-divider"><h2><span className="reminder-title-lettering">已完成</span></h2></div>
      <div className="reminders-list">{completed.length ? completed.map((reminder) => <ReminderCard reminder={reminder} key={reminder.id} members={state.members} onToggle={onToggle} onError={notify} />) : <p className="empty-note">还没有已完成的提醒。</p>}</div>
    </section>
  </div>;
}

function ReminderCompose({ onAdd, onClose, notify }: Pick<Props, 'onAdd' | 'onClose' | 'notify'>) {
  const [content, setContent] = useState('');
  const [assignee, setAssignee] = useState<'both' | 'self' | 'partner'>('both');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const title = content.trim();
    if (!title || busy) return;
    setBusy(true);
    setError('');
    try { await onAdd({ title, assignee }); notify('提醒已添加'); onClose(); }
    catch (error) { setError(error instanceof Error ? error.message : '提醒添加失败，请再试一次。'); }
    finally { setBusy(false); }
  };
  return <form className="reminder-compose" onSubmit={(event) => void submit(event)}>
    <div className="reminder-editor">
      <label className="sr-only" htmlFor="quick-reminder-content">提醒内容</label>
      <textarea id="quick-reminder-content" rows={5} maxLength={500} placeholder="写下提醒内容…" value={content} onChange={(event) => { setContent(event.target.value); setError(''); }} />
      <div className="reminder-editor-footer">
        <label className="reminder-mention"><span className="sr-only">提醒对象</span><select value={assignee} onChange={(event) => setAssignee(event.target.value as typeof assignee)}><option value="both">@全部</option><option value="self">@我</option><option value="partner">@他</option></select><ChevronDown size={14} aria-hidden="true" /></label>
      </div>
    </div>
    {error && <p className="form-error" role="alert">{error}</p>}
    <button className="primary-button" type="submit" disabled={busy || !content.trim()}>{busy ? '正在添加…' : '添加提醒'}<Plus size={17} /></button>
  </form>;
}

export function Anniversary({ state, onClose }: { state: RelationshipState; onClose?: () => void }) {
  const humans = state.members.filter((m) => m.id !== 'ai');
  return <div className="tool-content"><div className="anniversary-postcard"><CalendarHeart size={27} /><div className="anniversary-number">{daysTogether(state.togetherSince)}<span>天</span></div><div className="postcard-avatars"><Avatar member={humans[0]} size="large" /><Heart size={20} /><Avatar member={humans[1]} size="large" /></div><p>{humans.map((m) => m.name).join(' 和 ')}<br /><span>从 {state.togetherSince.replaceAll('-', '.')} 开始</span></p></div>{onClose && <button className="primary-button" onClick={onClose}>关闭<Check size={17} /></button>}</div>;
}

export function Tools(props: Props) {
  const { tool, onClose } = props;
  const labels = { reminders: '添加提醒', anniversary: '我们的小纪念' };
  return <Sheet title={labels[tool]} onClose={onClose}>{(close) => tool === 'reminders' ? <ReminderCompose onAdd={props.onAdd} onClose={close} notify={props.notify} /> : <Anniversary state={props.state} onClose={close} />}</Sheet>;
}
