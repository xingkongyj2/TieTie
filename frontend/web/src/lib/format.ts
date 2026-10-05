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
  const shanghaiTime = new Date(date.getTime() + 8 * 60 * 60_000);
  const todayInShanghai = new Date(Date.now() + 8 * 60 * 60_000);
  const isToday = shanghaiTime.toISOString().slice(0, 10) === todayInShanghai.toISOString().slice(0, 10);
  const prefix = isToday ? '今天' : `${shanghaiTime.getUTCMonth() + 1}月${shanghaiTime.getUTCDate()}日`;
  return `${prefix} ${String(shanghaiTime.getUTCHours()).padStart(2, '0')}:${String(shanghaiTime.getUTCMinutes()).padStart(2, '0')}`;
}
