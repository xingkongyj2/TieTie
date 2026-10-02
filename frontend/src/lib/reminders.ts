import type { Reminder } from '../types';

interface Lifecycle {
  status?: Reminder['status'];
  taskStatus?: Reminder['taskStatus'];
  taskCompletedAt?: string;
  deliveredAt?: string;
  completed?: boolean;
}

/** Latest due time first in every section; untimed entries come last. */
export function compareReminderTime(a: Reminder, b: Reminder): number {
  const time = (value?: string) => {
    if (!value) return Number.NEGATIVE_INFINITY;
    const stamp = Date.parse(value);
    return Number.isNaN(stamp) ? Number.NEGATIVE_INFINITY : stamp;
  };
  const left = time(a.time), right = time(b.time);
  return left === right ? a.id.localeCompare(b.id) : left > right ? -1 : 1;
}

export function hasCompletedDelivery(reminder: Lifecycle): boolean {
  return reminder.status === 'delivered' || reminder.taskStatus === 'completed'
    || !!reminder.taskCompletedAt || !!reminder.deliveredAt;
}

/** One classification shared by counters, sections and card actions. */
export function reminderPhase(reminder: Lifecycle): 'pending' | 'completed' | 'cancelled' {
  if (reminder.status === 'cancelled') return 'cancelled';
  if (reminder.status === 'completed' || hasCompletedDelivery(reminder)
    || !reminder.status && reminder.completed) return 'completed';
  return 'pending';
}

export function canCancelReminder(reminder: Lifecycle): boolean {
  return reminderPhase(reminder) === 'pending'
    && ['scheduled', 'uncertain', 'failed'].includes(reminder.status ?? '');
}

export function canToggleReminder(reminder: Reminder): boolean {
  if (!reminder.status) return true;
  if (reminderPhase(reminder) === 'cancelled' || hasCompletedDelivery(reminder)) return false;
  if (reminder.status === 'completed') return !!reminder.time && Date.parse(reminder.time) > Date.now();
  return ['scheduled', 'uncertain'].includes(reminder.status);
}

export function reminderStatusText(reminder: Lifecycle): string {
  const phase = reminderPhase(reminder);
  if (phase === 'cancelled') return '已取消';
  if (phase === 'completed') return hasCompletedDelivery(reminder) ? '已提醒' : '已完成';
  return reminder.status ? { scheduled: '待提醒', dispatching: '正在提醒', failed: '提醒失败', uncertain: '提醒状态待确认', delivered: '', completed: '', cancelled: '' }[reminder.status] : '';
}
