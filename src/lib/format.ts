/** 以自然日计算纪念天数，避免夏令时和不同时刻带来的半天误差。 */
export function daysTogether(since: string) {
  const [year, month, day] = since.split('-').map(Number);
  const now = new Date();
  const start = Date.UTC(year, month - 1, day);
  const today = Date.UTC(now.getFullYear(), now.getMonth(), now.getDate());
  return Math.max(1, Math.floor((today - start) / 86_400_000));
}

export function formatReminderTime(value: string) {
  if (/^\d{2}:\d{2}$/.test(value)) return `今天 ${value}`;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const now = new Date();
  const isToday = date.toDateString() === now.toDateString();
  const prefix = isToday ? '今天' : `${date.getMonth() + 1}月${date.getDate()}日`;
  return `${prefix} ${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}`;
}

export function defaultReminderTime() {
  const date = new Date(Date.now() + 60 * 60 * 1000);
  date.setMinutes(0);
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
  return local.toISOString().slice(0, 16);
}
