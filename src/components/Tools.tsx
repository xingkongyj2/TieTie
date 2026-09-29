import { CalendarHeart, Check, Heart, Plus } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import type { RelationshipState, Reminder } from '../types';
import { defaultReminderTime, daysTogether } from '../lib/format';
import { Avatar } from './Avatar';
import { ReminderCard } from './ReminderCard';
import { Sheet } from './Sheet';

export type ToolName = 'reminders' | 'anniversary';
interface Props { tool: ToolName; state: RelationshipState; onClose: () => void; onAdd: (input: Omit<Reminder, 'id' | 'completed'>) => Promise<void>; onToggle: (id: string) => Promise<void>; notify: (text: string) => void }

function Reminders({ state, onAdd, onToggle, notify }: Pick<Props, 'state' | 'onAdd' | 'onToggle' | 'notify'>) {
  const [title, setTitle] = useState('');
  const [time, setTime] = useState(defaultReminderTime);
  const [assignee, setAssignee] = useState<Reminder['assignee']>('both');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (new Date(time).getTime() <= Date.now()) { setError('选一个还没到的时间吧，好给心意留点余地。'); return; }
    setBusy(true);
    try { await onAdd({ title: title.trim(), time, assignee }); setTitle(''); setError(''); notify('小事已保存到此设备的提醒板 🐾'); }
    catch { setError('没能保存这件小事，请再试一次。'); }
    finally { setBusy(false); }
  };
  return <div className="tool-content"><div className="reminders-list">{state.reminders.length ? state.reminders.map((reminder) => <ReminderCard reminder={reminder} key={reminder.id} members={state.members} onToggle={onToggle} onError={notify} />) : <p className="empty-note">还没有小事，先记一件吧。</p>}</div>
    <form className="new-reminder-form" onSubmit={(event) => void submit(event)}><h3><Plus size={15} />再记一件小事</h3><label className="sr-only" htmlFor="reminder-title">提醒内容</label><input id="reminder-title" className="tool-input" placeholder="例如：一起去超市买水果 🍊" value={title} onChange={(event) => setTitle(event.target.value)} maxLength={60} required /><div className="reminder-fields"><label>什么时候<input aria-label="提醒时间" type="datetime-local" required value={time} onChange={(event) => setTime(event.target.value)} /></label><label>和谁一起<select aria-label="提醒对象" value={assignee} onChange={(event) => setAssignee(event.target.value as Reminder['assignee'])}><option value="both">我们一起</option>{state.members.filter((m) => m.id !== 'ai').map((m) => <option key={m.id} value={m.id}>{m.name}</option>)}</select></label></div>{error && <p className="form-error" role="alert">{error}</p>}<button className="primary-button" disabled={busy || !title.trim()} type="submit">{busy ? '正在记下来…' : '交给贴贴记着'}<Plus size={17} /></button></form><p className="settings-note">本地提醒板演示 · 暂不发送实际通知</p></div>;
}

function Anniversary({ state, onClose }: Pick<Props, 'state' | 'onClose'>) {
  const humans = state.members.filter((m) => m.id !== 'ai');
  return <div className="tool-content"><div className="anniversary-postcard"><CalendarHeart size={27} /><span className="eyebrow">BETTER, TOGETHER</span><div className="anniversary-number">{daysTogether(state.togetherSince)}<span>天</span></div><h3>把普通的日子，过成我们的日子。</h3><div className="postcard-avatars"><Avatar member={humans[0]} size="large" /><Heart size={20} /><Avatar member={humans[1]} size="large" /></div><p>{humans.map((m) => m.name).join(' & ')}<br /><span>从 {state.togetherSince.replaceAll('-', '.')} 开始，还要一起走好远。</span></p></div><p className="anniversary-quote">“喜欢是，连今天吃什么，<br />都想和你一起决定。”</p><button className="primary-button" onClick={onClose}>收好今天的小幸福<Check size={17} /></button></div>;
}

export function Tools(props: Props) {
  const { tool, onClose } = props;
  const labels = { reminders: ['一起记的小事', '把「别忘了」变成「有我呢」。'], anniversary: ['我们的小纪念', '每个平凡的今天，都很值得。'] };
  return <Sheet title={labels[tool][0]} subtitle={labels[tool][1]} onClose={onClose}>{(close) => tool === 'reminders' ? <Reminders {...props} /> : <Anniversary {...props} onClose={close} />}</Sheet>;
}
