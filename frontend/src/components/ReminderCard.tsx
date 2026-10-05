import { Check, Clock3, Repeat2, X } from './Icons';
import { useEffect, useRef, useState, type TouchEvent as ReactTouchEvent } from 'react';
import type { Member, Reminder } from '../types';
import { Avatar } from './Avatar';
import { formatReminderTime } from '../lib/format';
import { canCancelReminder, canToggleReminder, hasCompletedDelivery, reminderPhase, reminderRecurrenceText, reminderStatusText } from '../lib/reminders';

interface Props { reminder: Reminder; members: Member[]; onToggle: (id: string) => Promise<void>; onCancel?: (id: string) => Promise<void>; onDelete?: (id: string) => Promise<void>; onError: (text: string) => void }
const SWIPE_ACTION_WIDTH = 76;
const swipeClosers = new Set<() => void>();
interface SwipeGesture { touchId: number; x: number; y: number; startOffset: number; offset: number; width: number; horizontal: boolean }

export function ReminderCard({ reminder, members, onToggle, onCancel, onDelete, onError }: Props) {
  const [action, setAction] = useState<'toggle' | 'cancel' | 'delete' | null>(null);
  const [swipeOpen, setSwipeOpen] = useState(false);
  const [swipeOffset, setSwipeOffset] = useState(0);
  const [dragging, setDragging] = useState(false);
  const actionLock = useRef(false);
  const ownClose = useRef<(() => void) | null>(null);
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
    gestureRef.current = null;
    suppressClick.current = false;
    setSwipeOpen(false);
    setSwipeOffset(0);
    setDragging(false);
  }, [swipeWidth, showCancel, showDelete]);
  useEffect(() => {
    const close = () => {
      gestureRef.current = null;
      suppressClick.current = false;
      setSwipeOpen(false);
      setSwipeOffset(0);
      setDragging(false);
    };
    ownClose.current = close;
    swipeClosers.add(close);
    return () => { swipeClosers.delete(close); };
  }, []);
  const settleSwipe = (open: boolean) => {
    setSwipeOpen(open && hasSwipeActions);
    setSwipeOffset(open ? swipeWidth : 0);
    setDragging(false);
  };
  const startSwipe = (event: ReactTouchEvent<HTMLDivElement>) => {
    // Touch events are available in WeChat; retain the same gesture thresholds and card animation.
    const touch = event.touches[0];
    if (!touch || event.touches.length !== 1 || !hasSwipeActions || busy) return;
    swipeClosers.forEach((close) => { if (close !== ownClose.current) close(); });
    suppressClick.current = false;
    gestureRef.current = { touchId: touch.identifier, x: touch.clientX, y: touch.clientY,
      startOffset: Math.min(swipeOffset, swipeWidth), offset: Math.min(swipeOffset, swipeWidth), width: swipeWidth, horizontal: false };
  };
  const moveSwipe = (event: ReactTouchEvent<HTMLDivElement>) => {
    const gesture = gestureRef.current;
    const touch = Array.from(event.touches).find((item) => item.identifier === gesture?.touchId);
    if (!gesture || !touch || busy) return;
    const dx = touch.clientX - gesture.x;
    const dy = touch.clientY - gesture.y;
    if (!gesture.horizontal) {
      if (Math.max(Math.abs(dx), Math.abs(dy)) < 8) return;
      if (Math.abs(dx) <= Math.abs(dy) * 1.2) { gestureRef.current = null; return; }
      gesture.horizontal = true;
      setDragging(true);
    }
    event.preventDefault?.();
    gesture.offset = Math.max(0, Math.min(gesture.width, gesture.startOffset - dx));
    setSwipeOffset(gesture.offset);
  };
  const endSwipe = (_event: ReactTouchEvent<HTMLDivElement>, cancelled = false) => {
    const gesture = gestureRef.current;
    if (!gesture) return;
    gestureRef.current = null;
    if (gesture.horizontal) {
      suppressClick.current = !cancelled;
      settleSwipe(cancelled ? gesture.startOffset >= gesture.width / 2 : gesture.offset >= gesture.width / 2);
    }
  };
  const toggle = async () => {
    if (actionLock.current || !canToggle) return;
    actionLock.current = true;
    setAction('toggle');
    try { await onToggle(reminder.id); } catch (error) { onError(error instanceof Error ? error.message : '这条提醒还没保存成功，再点一次试试。'); }
    finally { actionLock.current = false; setAction(null); }
  };
  const cancel = async () => {
    if (!onCancel || actionLock.current || !canCancel) return;
    actionLock.current = true;
    setAction('cancel');
    try { await onCancel(reminder.id); settleSwipe(false); } catch (error) { onError(error instanceof Error ? error.message : '取消失败，请再试一次。'); }
    finally { actionLock.current = false; setAction(null); }
  };
  const remove = async () => {
    if (!onDelete || actionLock.current) return;
    actionLock.current = true;
    setAction('delete');
    try { await onDelete(reminder.id); } catch (error) { onError(error instanceof Error ? error.message : '删除失败，请再试一次。'); }
    finally { actionLock.current = false; setAction(null); }
  };
  return <div className={`reminder-swipe${hasSwipeActions ? ' has-actions' : ''}${swipeOpen ? ' is-open' : ''}${dragging ? ' is-dragging' : ''}`} role="group" aria-label={`提醒：${reminder.title}`} tabIndex={hasSwipeActions ? busy ? -1 : 0 : undefined} aria-keyshortcuts={hasSwipeActions ? 'ArrowLeft ArrowRight Escape' : undefined}
    onTouchStart={startSwipe} onTouchMove={moveSwipe} onTouchEnd={(event) => endSwipe(event)} onTouchCancel={(event) => endSwipe(event, true)} onKeyDown={(event) => {
      if (!hasSwipeActions || busy) return;
      if (event.key === 'ArrowLeft') { event.preventDefault(); settleSwipe(true); }
      else if (event.key === 'ArrowRight' || event.key === 'Escape') { event.preventDefault(); settleSwipe(false); }
    }}>
    {hasSwipeActions && <div className="reminder-swipe-actions" style={{ width: swipeWidth }} aria-hidden={!swipeOpen || dragging}>
      {showCancel && <button type="button" className="reminder-swipe-cancel" aria-label={`${recurrenceText ? '停止重复提醒' : '取消提醒'}：${reminder.title}`} aria-busy={action === 'cancel'} tabIndex={swipeOpen && !dragging ? 0 : -1} disabled={busy || !swipeOpen || dragging} onTouchStart={(event) => { suppressClick.current = false; event.stopPropagation(); }} onClick={(event) => { event.stopPropagation(); void cancel(); }}>{action === 'cancel' ? <><span className="spinner" aria-hidden="true" />{recurrenceText ? '停止中' : '取消中'}</> : recurrenceText ? '停止重复' : '取消'}</button>}
      {showDelete && <button type="button" className="reminder-swipe-delete" aria-label={`删除提醒：${reminder.title}`} aria-busy={action === 'delete'} tabIndex={swipeOpen && !dragging ? 0 : -1} disabled={busy || !swipeOpen || dragging} onTouchStart={(event) => { suppressClick.current = false; event.stopPropagation(); }} onClick={(event) => { event.stopPropagation(); void remove(); }}>{action === 'delete' ? <><span className="spinner" aria-hidden="true" />删除中</> : '删除'}</button>}
    </div>}
    <article onClick={(event) => {
      if (suppressClick.current) { suppressClick.current = false; event.preventDefault(); event.stopPropagation(); return; }
      if (swipeOpen) settleSwipe(false);
    }} className={`reminder-card ${phase !== 'pending' ? 'is-completed' : ''} ${delivered ? 'is-delivered' : ''} ${busy ? 'is-updating' : ''}`} style={{ transform: `translateX(-${Math.min(swipeOffset, swipeWidth)}px)` }}>
    <div className="reminder-main"><div><div className="reminder-title-row"><h3>{reminder.title}</h3>{phase !== 'pending' && statusText && <span className={`reminder-status status-${status} ${phase === 'cancelled' ? 'is-cancelled' : delivered ? 'is-delivered' : 'is-done'}`}>{statusText}</span>}</div>{reminder.time && <p><Clock3 size={12} />{formatReminderTime(reminder.time)}</p>}{recurrenceText && <div className="reminder-recurrence-row"><p className="reminder-recurrence"><Repeat2 size={12} aria-hidden="true" />{recurrenceText}</p></div>}</div>
      {phase === 'cancelled' ? <span className="reminder-check is-cancelled-mark" role="img" aria-label={`已取消：${reminder.title}`}><X size={18} strokeWidth={2.6} /></span> : (completed && !canToggle
        ? <span className="reminder-check is-readonly" role="img" aria-label={`${delivered ? '已提醒，不可反选' : '已完成，不可反选'}：${reminder.title}`}><Check size={17} strokeWidth={2.8} /></span>
        : <button type="button" className={`reminder-check${action === 'toggle' ? ' is-busy' : ''}`} title={!isRecipient ? '只有提醒接收者可以标记完成' : undefined} aria-label={completed ? `撤销完成：${reminder.title}` : `完成提醒：${reminder.title}`} aria-pressed={completed} aria-busy={action === 'toggle'} disabled={busy || !canToggle} onTouchStart={(event) => { suppressClick.current = false; event.stopPropagation(); }} onClick={(event) => {
          event.stopPropagation();
          if (suppressClick.current) { suppressClick.current = false; return; }
          if (swipeOpen) { settleSwipe(false); return; }
          void toggle();
        }}>{action === 'toggle' ? <span className="spinner" aria-hidden="true" /> : completed && <Check size={17} strokeWidth={2.8} />}</button>)}
    </div>
    {action && <div className="reminder-action-feedback" role="status"><span className="spinner" aria-hidden="true" />{action === 'delete' ? '正在删除…' : action === 'cancel' ? '正在取消…' : completed ? '正在恢复…' : '正在完成…'}</div>}
    <div className="reminder-footer"><div className="mini-avatars">{assignees.map((m) => <Avatar member={m} size="tiny" key={m.id} />)}</div><span className="reminder-assignees">{assignees.map((member) => `@${member.name}`).join(' ')}</span>{phase === 'pending' && status !== 'dispatching' && statusText && <span className={`reminder-status status-${status}`}>{statusText}</span>}</div>
  </article></div>;
}
