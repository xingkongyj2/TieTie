import { Bell, Check, Clock3 } from 'lucide-react';
import { useState } from 'react';
import type { Member, Reminder } from '../types';
import { Avatar } from './Avatar';
import { formatReminderTime } from '../lib/format';

interface Props { reminder: Reminder; members: Member[]; onToggle: (id: string) => Promise<void>; onError: (text: string) => void }

export function ReminderCard({ reminder, members, onToggle, onError }: Props) {
  const [busy, setBusy] = useState(false);
  const assignees = members.filter((member) => member.id !== 'ai' && (reminder.assignee === 'both' || member.id === reminder.assignee));
  const toggle = async () => {
    setBusy(true);
    try { await onToggle(reminder.id); } catch { onError('这件小事还没保存成功，再点一次试试。'); }
    finally { setBusy(false); }
  };
  return <article className={`reminder-card ${reminder.completed ? 'is-completed' : ''}`}>
    <div className="reminder-eyebrow"><Bell size={13} /><span>一起记的小事</span><span className="reminder-badge">{reminder.completed ? '已完成' : '待完成'}</span></div>
    <div className="reminder-main"><div><h3>{reminder.title}</h3><p><Clock3 size={12} />{formatReminderTime(reminder.time)}</p></div>
      <button className="reminder-check" aria-label={reminder.completed ? `撤销完成：${reminder.title}` : `完成提醒：${reminder.title}`} aria-pressed={reminder.completed} disabled={busy} onClick={() => void toggle()}>{reminder.completed && <Check size={17} strokeWidth={2.8} />}</button>
    </div>
    <div className="reminder-footer"><div className="mini-avatars">{assignees.map((m) => <Avatar member={m} size="tiny" key={m.id} />)}</div><span>{reminder.assignee === 'both' ? '我们一起' : assignees[0]?.name} · {reminder.completed ? '又搞定一件小事 ♡' : '一起把日子安排好'}</span><span className="tiny-sparkle">✧</span></div>
  </article>;
}
