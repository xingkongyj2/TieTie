import { Check, Clock3, Repeat2, X } from 'lucide-react';
import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react';
import type { Member, Reminder } from '../types';
import { Avatar } from './Avatar';
import { formatReminderTime } from '../lib/format';
import { canCancelReminder, canToggleReminder, hasCompletedDelivery, reminderPhase, reminderRecurrenceText, reminderStatusText } from '../lib/reminders';

interface Props { reminder: Reminder; members: Member[]; onToggle: (id: string) => Promise<void>; onCancel?: (id: string) => Promise<void>; onDelete?: (id: string) => Promise<void>; onError: (text: string) => void }
const SWIPE_ACTION_WIDTH = 76;
interface SwipeGesture { pointerId: number; x: number; y: number; startOffset: number; offset: number; width: number; horizontal: boolean }

export function ReminderCard({ reminder, members, onToggle, onCancel, onDelete, onError }: Props) {
  const [action, setAction] = useState<'toggle' | 'cancel' | 'delete' | null>(null);
  const [swipeOpen, setSwipeOpen] = useState(false);
  const [swipeOffset, setSwipeOffset] = useState(0);
  const [dragging, setDragging] = useState(false);
  const swipeRef = useRef<HTMLDivElement>(null);
  const gestureRef = useRef<SwipeGesture | null>(null);
  const suppressClick = useRef(false);
  const busy = action !== null;
  const assignees = members.filter((member) => member.id !== 'ai' && (reminder.recipientIds ? member.userId !== undefined && reminder.recipientIds.includes(member.userId) : reminder.assignee === 'both' || member.id === reminder.assignee));
  const status = reminder.status;
  const phase = reminderPhase(reminder);
  const completed = phase === 'completed';
  const delivered = completed && hasCompletedDelivery(reminder);
  const statusText = reminderStatusText(reminder);
  const recurrenceText = reminderRecurrenceText(reminder.recurrence);
  const viewerId = members.find((member) => member.id === 'self')?.userId;
  const isRecipient = !reminder.recipientIds || viewerId !== undefined && reminder.recipientIds.includes(viewerId);
  const canToggle = isRecipient && canToggleReminder(reminder);
  const canCancel = canCancelReminder(reminder);
  const showCancel = !!onCancel && canCancel;
  const showDelete = !!onDelete;
  const swipeWidth = (Number(showCancel) + Number(showDelete)) * SWIPE_ACTION_WIDTH;
  const hasSwipeActions = swipeWidth > 0;
  useEffect(() => {
    const gesture = gestureRef.current;
    gestureRef.current = null;
    if (gesture && swipeRef.current?.hasPointerCapture(gesture.pointerId)) swipeRef.current.releasePointerCapture(gesture.pointerId);
    suppressClick.current = false;
    setSwipeOpen(false);
    setSwipeOffset(0);
    setDragging(false);
  }, [swipeWidth, showCancel, showDelete]);
  const settleSwipe = (open: boolean) => {
    setSwipeOpen(open && hasSwipeActions);
    setSwipeOffset(open ? swipeWidth : 0);
    setDragging(false);
  };
  const startSwipe = (event: ReactPointerEvent<HTMLDivElement>) => {
    suppressClick.current = false;
    if (!hasSwipeActions || busy || !event.isPrimary || event.button !== 0
      || event.target instanceof Element && event.target.closest('button, a, input, textarea, select, [role="button"]')) return;
    const offset = Math.min(swipeOffset, swipeWidth);
    gestureRef.current = { pointerId: event.pointerId, x: event.clientX, y: event.clientY, startOffset: offset, offset, width: swipeWidth, horizontal: false };
  };
  const moveSwipe = (event: ReactPointerEvent<HTMLDivElement>) => {
    const gesture = gestureRef.current;
    if (!gesture || gesture.pointerId !== event.pointerId || busy) return;
    const dx = event.clientX - gesture.x;
    const dy = event.clientY - gesture.y;
    if (!gesture.horizontal) {
      if (Math.max(Math.abs(dx), Math.abs(dy)) < 8) return;
      if (Math.abs(dx) <= Math.abs(dy) * 1.2) { gestureRef.current = null; return; }
      gesture.horizontal = true;
      setDragging(true);
      event.currentTarget.setPointerCapture(event.pointerId);
    }
    event.preventDefault();
    gesture.offset = Math.max(0, Math.min(gesture.width, gesture.startOffset - dx));
    setSwipeOffset(gesture.offset);
  };
  const endSwipe = (event: ReactPointerEvent<HTMLDivElement>, cancelled = false) => {
    const gesture = gestureRef.current;
    if (!gesture || gesture.pointerId !== event.pointerId) return;
    gestureRef.current = null;
    if (gesture.horizontal) {
      suppressClick.current = !cancelled;
      settleSwipe(cancelled ? gesture.startOffset >= gesture.width / 2 : gesture.offset >= gesture.width / 2);
    }
    if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId);
  };
  const toggle = async () => {
    if (busy) return;
    setAction('toggle');
    try { await onToggle(reminder.id); } catch (error) { onError(error instanceof Error ? error.message : '这条提醒还没保存成功，再点一次试试。'); }
    finally { setAction(null); }
  };
  const cancel = async () => {
    if (!onCancel || busy) return;
    setAction('cancel');
    try { await onCancel(reminder.id); settleSwipe(false); swipeRef.current?.focus(); } catch (error) { onError(error instanceof Error ? error.message : '取消失败，请再试一次。'); }
    finally { setAction(null); }
  };
  const remove = async () => {
    if (!onDelete || busy) return;
    setAction('delete');
    try { await onDelete(reminder.id); } catch (error) { onError(error instanceof Error ? error.message : '删除失败，请再试一次。'); }
    finally { setAction(null); }
  };
  return <div ref={swipeRef} className={`reminder-swipe${hasSwipeActions ? ' has-actions' : ''}${swipeOpen ? ' is-open' : ''}${dragging ? ' is-dragging' : ''}`} role="group" aria-label={`提醒：${reminder.title}`} tabIndex={hasSwipeActions ? busy ? -1 : 0 : undefined} aria-keyshortcuts={hasSwipeActions ? 'ArrowLeft ArrowRight Escape' : undefined}
    onPointerDown={startSwipe} onPointerMove={moveSwipe} onPointerUp={(event) => endSwipe(event)} onPointerCancel={(event) => endSwipe(event, true)} onLostPointerCapture={(event) => { if (event.target === event.currentTarget) endSwipe(event, true); }}
    onDragStart={(event) => { if (hasSwipeActions) event.preventDefault(); }}
    onClickCapture={(event) => { if (suppressClick.current) { suppressClick.current = false; event.preventDefault(); event.stopPropagation(); } }}
    onKeyDown={(event) => {
      if (!hasSwipeActions || busy) return;
      if (event.key === 'ArrowLeft' && event.target === event.currentTarget) { event.preventDefault(); settleSwipe(true); }
      else if (swipeOpen && (event.key === 'Escape' || event.key === 'ArrowRight' && (event.target === event.currentTarget || event.target instanceof Element && event.target.closest('.reminder-swipe-actions')))) {
        event.preventDefault(); settleSwipe(false); swipeRef.current?.focus();
      }
    }}>
    {hasSwipeActions && <div className="reminder-swipe-actions" style={{ width: swipeWidth }} aria-hidden={!swipeOpen || dragging} inert={!swipeOpen || dragging}>
      {showCancel && <button type="button" className="reminder-swipe-cancel" aria-label={`${recurrenceText ? '停止重复提醒' : '取消提醒'}：${reminder.title}`} aria-busy={action === 'cancel'} tabIndex={swipeOpen && !dragging ? 0 : -1} disabled={busy || !swipeOpen || dragging} onClick={() => void cancel()}>{action === 'cancel' ? <><span className="spinner" aria-hidden="true" />{recurrenceText ? '停止中' : '取消中'}</> : recurrenceText ? '停止重复' : '取消'}</button>}
      {showDelete && <button type="button" className="reminder-swipe-delete" aria-label={`删除提醒：${reminder.title}`} aria-busy={action === 'delete'} tabIndex={swipeOpen && !dragging ? 0 : -1} disabled={busy || !swipeOpen || dragging} onClick={() => void remove()}>{action === 'delete' ? <><span className="spinner" aria-hidden="true" />删除中</> : '删除'}</button>}
    </div>}
    <article className={`reminder-card ${phase !== 'pending' ? 'is-completed' : ''} ${delivered ? 'is-delivered' : ''} ${busy ? 'is-updating' : ''}`} style={{ transform: `translateX(-${Math.min(swipeOffset, swipeWidth)}px)` }}>
    <div className="reminder-main"><div><div className="reminder-title-row"><h3>{reminder.title}</h3>{phase !== 'pending' && statusText && <span className={`reminder-status status-${status} ${phase === 'cancelled' ? 'is-cancelled' : delivered ? 'is-delivered' : 'is-done'}`}>{statusText}</span>}</div>{reminder.time && <p><Clock3 size={12} />{formatReminderTime(reminder.time)}</p>}{recurrenceText && <div className="reminder-recurrence-row"><p className="reminder-recurrence"><Repeat2 size={12} aria-hidden="true" />{recurrenceText}</p></div>}</div>
      {phase === 'cancelled' ? <span className="reminder-check is-cancelled-mark" role="img" aria-label={`已取消：${reminder.title}`}><X size={18} strokeWidth={2.6} /></span> : (completed && !canToggle
        ? <span className="reminder-check is-readonly" role="img" aria-label={`${delivered ? '已提醒，不可反选' : '已完成，不可反选'}：${reminder.title}`}><Check size={17} strokeWidth={2.8} /></span>
        : <button type="button" className="reminder-check" title={!isRecipient ? '只有提醒接收者可以标记完成' : undefined} aria-label={completed ? `撤销完成：${reminder.title}` : `完成提醒：${reminder.title}`} aria-pressed={completed} aria-busy={action === 'toggle'} disabled={busy || !canToggle} onClick={() => void toggle()}>{action === 'toggle' ? <span className="spinner" aria-hidden="true" /> : completed && <Check size={17} strokeWidth={2.8} />}</button>)}
    </div>
    {action && <div className="reminder-action-feedback" role="status"><span className="spinner" aria-hidden="true" />{action === 'delete' ? '正在删除…' : action === 'cancel' ? '正在取消…' : completed ? '正在恢复…' : '正在完成…'}</div>}
    <div className="reminder-footer"><div className="mini-avatars">{assignees.map((m) => <Avatar member={m} size="tiny" key={m.id} />)}</div><span className="reminder-assignees">{assignees.map((member) => `@${member.name}`).join(' ')}</span>{phase === 'pending' && status !== 'dispatching' && statusText && <span className={`reminder-status status-${status}`}>{statusText}</span>}</div>
  </article></div>;
}
