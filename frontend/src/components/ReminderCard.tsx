import { Check, Clock3 } from 'lucide-react';
import { useState } from 'react';
import type { Member, Reminder } from '../types';
import { Avatar } from './Avatar';
import { formatReminderTime } from '../lib/format';

interface Props { reminder: Reminder; members: Member[]; onToggle: (id: string) => Promise<void>; onCancel?: (id: string) => Promise<void>; onError: (text: string) => void }

export function ReminderCard({ reminder, members, onToggle, onCancel, onError }: Props) {
  const [busy, setBusy] = useState(false);
  const assignees = members.filter((member) => member.id !== 'ai' && (reminder.recipientIds ? member.userId !== undefined && reminder.recipientIds.includes(member.userId) : reminder.assignee === 'both' || member.id === reminder.assignee));
  const status = reminder.status;
  const statusText = status ? { scheduled: '待提醒', dispatching: '正在准备提醒', delivered: '已提醒 · 定时任务完成', completed: '已完成', cancelled: '已取消', failed: '提醒失败', uncertain: '发送状态待确认' }[status] : '';
  const viewerId = members.find((member) => member.id === 'self')?.userId;
  const isRecipient = !reminder.recipientIds || viewerId !== undefined && reminder.recipientIds.includes(viewerId);
  const canToggle = isRecipient && (!status || ['scheduled', 'delivered', 'uncertain', 'completed'].includes(status)) && (!reminder.completed || !reminder.deliveredAt && (!reminder.time || Date.parse(reminder.time) > Date.now()));
  const canCancel = status && ['scheduled', 'delivered', 'uncertain', 'failed'].includes(status);
  const toggle = async () => {
    setBusy(true);
    try { await onToggle(reminder.id); } catch (error) { onError(error instanceof Error ? error.message : '这条提醒还没保存成功，再点一次试试。'); }
    finally { setBusy(false); }
  };
  const cancel = async () => {
    if (!onCancel || busy) return;
    setBusy(true);
    try { await onCancel(reminder.id); } catch (error) { onError(error instanceof Error ? error.message : '取消失败，请再试一次。'); }
    finally { setBusy(false); }
  };
  return <article className={`reminder-card ${reminder.completed || status === 'cancelled' ? 'is-completed' : ''}`}>
    <div className="reminder-main"><div><div className="reminder-title-row"><h3>{reminder.title}</h3></div>{reminder.time && <p><Clock3 size={12} />{formatReminderTime(reminder.time)}</p>}</div>
      <button className="reminder-check" title={!isRecipient ? '只有提醒接收者可以标记完成' : reminder.completed && !canToggle ? '已到期或已提醒的事项无法撤销完成' : undefined} aria-label={reminder.completed ? `撤销完成：${reminder.title}` : `完成提醒：${reminder.title}`} aria-pressed={reminder.completed} disabled={busy || !canToggle} onClick={() => void toggle()}>{reminder.completed && <Check size={17} strokeWidth={2.8} />}</button>
    </div>
    <div className="reminder-footer"><div className="mini-avatars">{assignees.map((m) => <Avatar member={m} size="tiny" key={m.id} />)}</div><span>{assignees.map((member) => `@${member.name}`).join(' ')}</span>{statusText && <span className={`reminder-status status-${status}`}>{statusText}</span>}{onCancel && canCancel && <button type="button" className="reminder-cancel" disabled={busy} onClick={() => void cancel()}>取消</button>}</div>
  </article>;
}
