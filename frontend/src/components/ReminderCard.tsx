import { Check, Clock3 } from 'lucide-react';
import { useState } from 'react';
import type { Member, Reminder } from '../types';
import { Avatar } from './Avatar';
import { formatReminderTime } from '../lib/format';
import { canCancelReminder, canToggleReminder, reminderPhase, reminderStatusText } from '../lib/reminders';

interface Props { reminder: Reminder; members: Member[]; onToggle: (id: string) => Promise<void>; onCancel?: (id: string) => Promise<void>; onError: (text: string) => void }

export function ReminderCard({ reminder, members, onToggle, onCancel, onError }: Props) {
  const [action, setAction] = useState<'toggle' | 'cancel' | null>(null);
  const busy = action !== null;
  const assignees = members.filter((member) => member.id !== 'ai' && (reminder.recipientIds ? member.userId !== undefined && reminder.recipientIds.includes(member.userId) : reminder.assignee === 'both' || member.id === reminder.assignee));
  const status = reminder.status;
  const phase = reminderPhase(reminder);
  const completed = phase === 'completed';
  const statusText = reminderStatusText(reminder);
  const viewerId = members.find((member) => member.id === 'self')?.userId;
  const isRecipient = !reminder.recipientIds || viewerId !== undefined && reminder.recipientIds.includes(viewerId);
  const canToggle = isRecipient && canToggleReminder(reminder);
  const canCancel = canCancelReminder(reminder);
  const toggle = async () => {
    if (busy) return;
    setAction('toggle');
    try { await onToggle(reminder.id); } catch (error) { onError(error instanceof Error ? error.message : '这条提醒还没保存成功，再点一次试试。'); }
    finally { setAction(null); }
  };
  const cancel = async () => {
    if (!onCancel || busy) return;
    setAction('cancel');
    try { await onCancel(reminder.id); } catch (error) { onError(error instanceof Error ? error.message : '取消失败，请再试一次。'); }
    finally { setAction(null); }
  };
  return <article className={`reminder-card ${phase !== 'pending' ? 'is-completed' : ''} ${busy ? 'is-updating' : ''}`}>
    <div className="reminder-main"><div><div className="reminder-title-row"><h3>{reminder.title}</h3></div>{reminder.time && <p><Clock3 size={12} />{formatReminderTime(reminder.time)}</p>}</div>
      {phase !== 'cancelled' && (completed && !canToggle
        ? <span className="reminder-check" role="img" aria-label={`已完成：${reminder.title}`}><Check size={17} strokeWidth={2.8} /></span>
        : <button type="button" className="reminder-check" title={!isRecipient ? '只有提醒接收者可以标记完成' : undefined} aria-label={completed ? `撤销完成：${reminder.title}` : `完成提醒：${reminder.title}`} aria-pressed={completed} aria-busy={action === 'toggle'} disabled={busy || !canToggle} onClick={() => void toggle()}>{action === 'toggle' ? <span className="spinner" aria-hidden="true" /> : completed && <Check size={17} strokeWidth={2.8} />}</button>)}
    </div>
    {action && <div className="reminder-action-feedback" role="status"><span className="spinner" aria-hidden="true" />{action === 'cancel' ? '正在取消…' : completed ? '正在恢复…' : '正在完成…'}</div>}
    <div className="reminder-footer"><div className="mini-avatars">{assignees.map((m) => <Avatar member={m} size="tiny" key={m.id} />)}</div><span>{assignees.map((member) => `@${member.name}`).join(' ')}</span>{statusText && <span className={`reminder-status status-${status} ${phase === 'cancelled' ? 'is-cancelled' : completed ? 'is-done' : ''}`}>{statusText}</span>}{onCancel && canCancel && <button type="button" className="reminder-cancel" aria-busy={action === 'cancel'} disabled={busy} onClick={() => void cancel()}>{action === 'cancel' ? '取消中…' : '取消'}</button>}</div>
  </article>;
}
